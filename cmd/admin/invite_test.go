package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// runInviteTest runs admin invite with args against the test database.
func runInviteTest(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := envFrom(map[string]string{"RSS_DB_DSN": psqltest.DSN(t)})
	code = run(append([]string{"invite"}, args...), env, nil, &out, &errOut)
	return code, out.String(), errOut.String()
}

var inviteLinkRX = regexp.MustCompile(`^https://app\.example\.com/signup\?invite=([A-Za-z0-9_-]+)\n$`)

func TestInvite(t *testing.T) {
	db := psqltest.NewDB(t)
	invites := psql.NewInviteRepository(db)
	email := fmt.Sprintf("invite-cli-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM waitlist WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup waitlist: %v", err)
		}
	})

	// create runs admin invite create with args and returns the stored
	// invite, which it deletes when the test ends.
	create := func(t *testing.T, args ...string) rss.Invite {
		t.Helper()
		code, stdout, stderr := runInviteTest(t, append([]string{"create", "-app-url", "https://app.example.com/"}, args...)...)
		if code != 0 {
			t.Fatalf("create = %d, want 0; stderr = %q", code, stderr)
		}
		m := inviteLinkRX.FindStringSubmatch(stdout)
		if m == nil {
			t.Fatalf("stdout = %q, want the invite's link", stdout)
		}
		inv, err := invites.GetByToken(t.Context(), token.Hash(m[1]))
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM invites WHERE id = $1`, inv.ID); err != nil {
				t.Errorf("cleanup invite: %v", err)
			}
		})
		if got := cliAudit(t, db, "invite", inv.ID); !slices.Equal(got, []string{"invite.create"}) {
			t.Errorf("audit log = %v, want invite.create from the command line", got)
		}
		return inv
	}

	open := create(t, "-note", "for Grace", "-expires", "48h")
	t.Run("create an open invite", func(t *testing.T) {
		if open.Email != "" || open.Note != "for Grace" || open.Status(time.Now()) != rss.InvitePending {
			t.Errorf("invite = %+v", open)
		}
		if d := time.Until(open.ExpiresAt); d < 47*time.Hour || d > 48*time.Hour {
			t.Errorf("expires in %v, want 48h", d)
		}
	})

	t.Run("create a bound invite", func(t *testing.T) {
		waitlist := psql.NewWaitlistRepository(db)
		_, hash, err := token.New()
		if err != nil {
			t.Fatal(err)
		}
		entry, err := waitlist.Join(t.Context(), rss.WaitlistEntry{Email: email, Name: "Ada"}, hash)
		if err != nil {
			t.Fatal(err)
		}

		// Unconfirmed: no bound invite.
		code, _, stderr := runInviteTest(t, "create", "-email", email)
		if code != 1 || !strings.Contains(stderr, "no confirmed waitlist entry for "+email) {
			t.Errorf("create for an unconfirmed entry = %d, stderr %q; want 1 and no confirmed entry", code, stderr)
		}

		if err := waitlist.Confirm(t.Context(), hash); err != nil {
			t.Fatal(err)
		}
		inv := create(t, "-email", strings.ToUpper(email))
		if inv.Email != email || inv.WaitlistID != entry.ID || inv.Name != "Ada" {
			t.Errorf("invite = %+v, want bound to %s", inv, entry.ID)
		}
		if d := time.Until(inv.ExpiresAt); d < defaultInviteExpiry-time.Minute || d > defaultInviteExpiry {
			t.Errorf("expires in %v, want %v", d, defaultInviteExpiry)
		}
	})

	t.Run("list", func(t *testing.T) {
		code, stdout, stderr := runInviteTest(t, "list")
		if code != 0 {
			t.Fatalf("list = %d, want 0; stderr = %q", code, stderr)
		}
		line := lineWith(stdout, open.ID)
		for _, want := range []string{"for Grace", "pending", open.ExpiresAt.UTC().Format("2006-01-02 15:04")} {
			if !strings.Contains(line, want) {
				t.Errorf("line %q doesn't contain %q", line, want)
			}
		}
	})

	t.Run("revoke", func(t *testing.T) {
		code, stdout, stderr := runInviteTest(t, "revoke", open.ID)
		if code != 0 || !strings.Contains(stdout, "revoked invite "+open.ID) {
			t.Fatalf("revoke = %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		_, stdout, _ = runInviteTest(t, "list")
		if line := lineWith(stdout, open.ID); !strings.Contains(line, "revoked") {
			t.Errorf("line %q, want revoked", line)
		}
		if got := cliAudit(t, db, "invite", open.ID); !slices.Equal(got, []string{"invite.create", "invite.revoke"}) {
			t.Errorf("audit log = %v, want invite.create and invite.revoke from the command line", got)
		}

		code, _, stderr = runInviteTest(t, "revoke", "00000000-0000-0000-0000-000000000000")
		if code != 1 || !strings.Contains(stderr, "no invite with ID") {
			t.Errorf("revoke(unknown) = %d, stderr %q; want 1 and no invite", code, stderr)
		}
	})
}

// cliAudit returns the actions the audit log recorded from the command line
// for the target, oldest first, and deletes their rows when the test ends.
func cliAudit(t *testing.T, db *sql.DB, targetType, targetID string) []string {
	t.Helper()
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM admin_audit WHERE target_type = $1 AND target_id = $2`, targetType, targetID); err != nil {
			t.Errorf("cleanup audit: %v", err)
		}
	})
	rows, err := db.QueryContext(t.Context(), `SELECT action FROM admin_audit
		WHERE target_type = $1 AND target_id = $2 AND via = 'cli' ORDER BY id`, targetType, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return actions
}

// lineWith returns the line of s that contains sub, or "".
func lineWith(s, sub string) string {
	for line := range strings.Lines(s) {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

func TestInviteInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no subcommand", nil, nil, "Usage:"},
		{"unknown subcommand", []string{"send"}, nil, `unknown command "send"`},
		{"create without DSN", []string{"create"}, nil, "database DSN must not be empty"},
		{"create with a bad app URL", []string{"create", "-app-url", "app.example.com"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "invalid app URL"},
		{"create with an app URL path", []string{"create"}, map[string]string{"RSS_DB_DSN": unreachableDSN, "RSS_APP_URL": "https://example.com/app"}, "invalid app URL"},
		{"create with a bad email", []string{"create", "-email", "not-an-email"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, `invalid email "not-an-email"`},
		{"create with no expiry", []string{"create", "-expires", "0s"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "-expires must be positive"},
		{"list without DSN", []string{"list"}, nil, "database DSN must not be empty"},
		{"revoke without ID", []string{"revoke"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "give the ID of one invite"},
		{"revoke a bad ID", []string{"revoke", "abc"}, map[string]string{"RSS_DB_DSN": unreachableDSN}, "give the ID of one invite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(append([]string{"invite"}, tt.args...), envFrom(tt.env), nil, io.Discard, &stderr); code != 2 {
				t.Fatalf("run() = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestWriteInvites(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	writeInvites(&out, []rss.Invite{
		{ID: "b", Email: "ada@example.com", Note: "ignored", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: "a", Note: "for Grace", CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{ID: "c", CreatedAt: now, ExpiresAt: now.Add(time.Hour), RedeemedAt: now},
	}, now)
	want := `ID  CREATED           FOR              STATUS    EXPIRES
b   2026-11-09 11:00  ada@example.com  pending   2026-11-09 13:00
a   2026-11-07 12:00  for Grace        expired   2026-11-09 11:00
c   2026-11-09 12:00  -                redeemed  2026-11-09 13:00
`
	if out.String() != want {
		t.Errorf("writeInvites =\n%s\nwant\n%s", out.String(), want)
	}
}
