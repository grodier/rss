package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// testActor returns a command-line actor whose admin_email is unique to it;
// the rows it's recorded in are deleted when the test ends.
func testActor(t *testing.T, db *sql.DB) rss.AuditActor {
	t.Helper()
	email := fmt.Sprintf("audit-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM admin_audit WHERE admin_email = $1`, email); err != nil {
			t.Errorf("cleanup audit: %v", err)
		}
	})
	return rss.AuditActor{AdminEmail: email, Via: rss.AuditViaCLI}
}

// brokenActor returns an actor whose rows break admin_audit's CHECK on via,
// so recording by it fails.
func brokenActor(t *testing.T, db *sql.DB) rss.AuditActor {
	t.Helper()
	by := testActor(t, db)
	by.Via = "bogus"
	return by
}

// auditBy returns the audit log's entries by, newest first.
func auditBy(t *testing.T, db *sql.DB, by rss.AuditActor) []rss.AuditEvent {
	t.Helper()
	all, err := NewAuditRepository(db).List(t.Context(), rss.AuditFilter{}, 10_000)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return slices.DeleteFunc(all, func(e rss.AuditEvent) bool { return e.AdminEmail != by.AdminEmail })
}

// cleanupExec runs stmt with args when the test ends.
func cleanupExec(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Errorf("cleanup %q: %v", stmt, err)
		}
	})
}

func TestAuditRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAuditRepository(db)
	action := fmt.Sprintf("test.%d", time.Now().UnixNano())
	target := fmt.Sprintf("target-%d", time.Now().UnixNano())

	first := rss.AuditEntry{AuditActor: testActor(t, db), Action: action, TargetType: "user", TargetID: target,
		Before: map[string]any{"name": "<b>Ada</b>", "admin": false},
		After:  map[string]any{"name": "Grace", "admin": true}}
	first.Via, first.IP = rss.AuditViaWeb, "203.0.113.7"
	second := rss.AuditEntry{AuditActor: testActor(t, db), Action: action}
	third := rss.AuditEntry{AuditActor: testActor(t, db), Action: action, TargetType: "user", TargetID: target}
	for _, e := range []rss.AuditEntry{first, second, third} {
		if err := repo.Record(t.Context(), e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	t.Run("List is newest first and keeps the fields", func(t *testing.T) {
		got, err := repo.List(t.Context(), rss.AuditFilter{Action: action}, 10)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("List = %d entries, want 3", len(got))
		}
		emails := []string{got[0].AdminEmail, got[1].AdminEmail, got[2].AdminEmail}
		if !slices.Equal(emails, []string{third.AdminEmail, second.AdminEmail, first.AdminEmail}) {
			t.Errorf("List order = %v, want newest first", emails)
		}
		e := got[2]
		if e.ID == 0 || e.OccurredAt.IsZero() || e.Via != "web" || e.IP != "203.0.113.7" || e.TargetType != "user" || e.TargetID != target || e.AdminID != "" {
			t.Errorf("entry = %+v", e)
		}
		if e.Before["name"] != "<b>Ada</b>" || e.Before["admin"] != false || e.After["name"] != "Grace" || e.After["admin"] != true {
			t.Errorf("before, after = %v, %v", e.Before, e.After)
		}
		if got[1].Before != nil || got[1].After != nil {
			t.Errorf("an entry without changes = %v, %v; want nil", got[1].Before, got[1].After)
		}
	})

	t.Run("List filters by target and pages with BeforeID", func(t *testing.T) {
		got, err := repo.List(t.Context(), rss.AuditFilter{TargetType: "user", TargetID: target}, 10)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 || got[0].AdminEmail != third.AdminEmail || got[1].AdminEmail != first.AdminEmail {
			t.Fatalf("List by target = %+v, want third and first", got)
		}
		page, err := repo.List(t.Context(), rss.AuditFilter{Action: action}, 1)
		if err != nil || len(page) != 1 {
			t.Fatalf("List one = %v, %v", page, err)
		}
		next, err := repo.List(t.Context(), rss.AuditFilter{Action: action, BeforeID: page[0].ID}, 10)
		if err != nil {
			t.Fatalf("List before: %v", err)
		}
		if len(next) != 2 || next[0].AdminEmail != second.AdminEmail {
			t.Errorf("List before the newest = %+v, want second and first", next)
		}
	})

	t.Run("Record fills the admin's email from its ID", func(t *testing.T) {
		email := fmt.Sprintf("admin-%d@example.com", time.Now().UnixNano())
		cleanupExec(t, db, `DELETE FROM admins WHERE email = $1`, email)
		cleanupExec(t, db, `DELETE FROM admin_audit WHERE admin_email = $1`, email)
		id, err := NewAdminRepository(db).Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), testActor(t, db))
		if err != nil {
			t.Fatalf("Create admin: %v", err)
		}
		login := action + ".login"
		if err := repo.Record(t.Context(), rss.AuditEntry{AuditActor: rss.AuditActor{AdminID: id, Via: rss.AuditViaWeb}, Action: login}); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := repo.List(t.Context(), rss.AuditFilter{Action: login}, 10)
		if err != nil || len(got) != 1 {
			t.Fatalf("List = %v, %v", got, err)
		}
		if got[0].AdminID != id || got[0].AdminEmail != email {
			t.Errorf("entry admin = %q, %q; want %q, %q", got[0].AdminID, got[0].AdminEmail, id, email)
		}
	})
}

// TestAuditInTransaction checks that each method that makes an admin's
// change records it, by its actor, and that the change and its row commit
// together: neither does when recording fails.
func TestAuditInTransaction(t *testing.T) {
	db := psqltest.NewDB(t)

	// recorded checks that by has exactly one entry: want, but for the
	// fields only the database knows.
	recorded := func(t *testing.T, by rss.AuditActor, want rss.AuditEntry) rss.AuditEvent {
		t.Helper()
		got := auditBy(t, db, by)
		if len(got) != 1 {
			t.Fatalf("entries by the actor = %+v, want one", got)
		}
		e := got[0]
		if e.AuditActor != by || e.Action != want.Action || e.TargetType != want.TargetType || e.TargetID != want.TargetID {
			t.Errorf("entry = %+v, want %+v", e.AuditEntry, want)
		}
		return e
	}

	t.Run("InviteRepository.Create", func(t *testing.T) {
		repo := NewInviteRepository(db)
		hash := newWaitlistHash(t)
		if _, err := repo.Create(t.Context(), rss.Invite{Note: "never stored", ExpiresAt: time.Now().Add(time.Hour)}, hash, brokenActor(t, db)); err == nil {
			t.Fatal("Create by a broken actor succeeded")
		}
		if _, err := repo.GetByToken(t.Context(), hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the invite was stored without its record (GetByToken error = %v)", err)
		}

		by := testActor(t, db)
		got, err := repo.Create(t.Context(), rss.Invite{Note: "stored", ExpiresAt: time.Now().Add(time.Hour)}, newWaitlistHash(t), by)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		cleanupExec(t, db, `DELETE FROM invites WHERE id = $1`, got.ID)
		e := recorded(t, by, rss.AuditEntry{Action: "invite.create", TargetType: "invite", TargetID: got.ID})
		if _, hasEmail := e.After["email"]; e.Before != nil || e.After["note"] != "stored" || e.After["expires_at"] == nil || hasEmail {
			t.Errorf("before, after = %v, %v; want the note and expiry, no email for an open invite", e.Before, e.After)
		}
	})

	t.Run("InviteRepository.Revoke", func(t *testing.T) {
		inv, hash := newInvite(t, db, rss.Invite{Note: "to revoke"})
		repo := NewInviteRepository(db)

		if err := repo.Revoke(t.Context(), inv.ID, brokenActor(t, db)); err == nil {
			t.Fatal("Revoke by a broken actor succeeded")
		}
		if got, _ := repo.GetByToken(t.Context(), hash); got.Status(time.Now()) != rss.InvitePending {
			t.Errorf("the invite was revoked without its record: %q", got.Status(time.Now()))
		}

		by := testActor(t, db)
		if err := repo.Revoke(t.Context(), inv.ID, by); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		// Revoking again changes, and records, nothing.
		if err := repo.Revoke(t.Context(), inv.ID, by); err != nil {
			t.Fatalf("Revoke again: %v", err)
		}
		e := recorded(t, by, rss.AuditEntry{Action: "invite.revoke", TargetType: "invite", TargetID: inv.ID})
		if v, ok := e.Before["revoked_at"]; !ok || v != nil {
			t.Errorf("before = %v, want revoked_at null", e.Before)
		}
		if v, _ := e.After["revoked_at"].(string); v == "" {
			t.Errorf("after = %v, want revoked_at's time", e.After)
		}
	})

	t.Run("AdminRepository.Create and Reset", func(t *testing.T) {
		email := fmt.Sprintf("admin-%d@example.com", time.Now().UnixNano())
		cleanupExec(t, db, `DELETE FROM admins WHERE email = $1`, email)
		repo := NewAdminRepository(db)

		if _, err := repo.Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), brokenActor(t, db)); err == nil {
			t.Fatal("Create by a broken actor succeeded")
		}
		if _, err := repo.GetByEmail(t.Context(), email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the admin was stored without its record (GetByEmail error = %v)", err)
		}

		by := testActor(t, db)
		id, err := repo.Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), by)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if e := recorded(t, by, rss.AuditEntry{Action: "admin.create", TargetType: "admin", TargetID: id}); e.After["email"] != email {
			t.Errorf("after = %v, want the email", e.After)
		}

		if err := repo.Reset(t.Context(), email, newAdminCredentials(t, "new pw", "s2"), brokenActor(t, db)); err == nil {
			t.Fatal("Reset by a broken actor succeeded")
		}
		if _, err := repo.Authenticate(t.Context(), email, "pw"); err != nil {
			t.Errorf("the admin was reset without its record (Authenticate with the old password: %v)", err)
		}
		by = testActor(t, db)
		if err := repo.Reset(t.Context(), email, newAdminCredentials(t, "new pw", "s2"), by); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		recorded(t, by, rss.AuditEntry{Action: "admin.reset", TargetType: "admin", TargetID: id})
	})

	t.Run("UserRepository.Create", func(t *testing.T) {
		email := fmt.Sprintf("user-%d@example.com", time.Now().UnixNano())
		cleanupExec(t, db, `DELETE FROM users WHERE email = $1`, email)
		repo := NewUserRepository(db)

		bad := brokenActor(t, db)
		if _, _, err := repo.Create(t.Context(), "Audited", email, "correct-horse-battery", &bad); err == nil {
			t.Fatal("Create by a broken actor succeeded")
		}
		if _, err := repo.GetByEmail(t.Context(), email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the user was stored without its record (GetByEmail error = %v)", err)
		}

		by := testActor(t, db)
		id, _, err := repo.Create(t.Context(), "Audited", email, "correct-horse-battery", &by)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		e := recorded(t, by, rss.AuditEntry{Action: "user.create", TargetType: "user", TargetID: id})
		if e.After["name"] != "Audited" || e.After["email"] != email {
			t.Errorf("after = %v, want the name and email", e.After)
		}
	})
}
