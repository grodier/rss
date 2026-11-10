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

// testAuditEntry returns a CLI entry for action whose admin_email is unique
// to it; its rows are deleted when the test ends.
func testAuditEntry(t *testing.T, db *sql.DB, action string) rss.AuditEntry {
	t.Helper()
	email := fmt.Sprintf("audit-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM admin_audit WHERE admin_email = $1`, email); err != nil {
			t.Errorf("cleanup audit: %v", err)
		}
	})
	return rss.AuditEntry{AdminEmail: email, Via: rss.AuditViaCLI, Action: action}
}

// auditRows returns the audit rows of the entry testAuditEntry returned.
func auditRows(t *testing.T, db *sql.DB, e rss.AuditEntry) []rss.AuditEvent {
	t.Helper()
	all, err := NewAuditRepository(db).List(t.Context(), rss.AuditFilter{Action: e.Action, Limit: 1000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var mine []rss.AuditEvent
	for _, ev := range all {
		if ev.AdminEmail == e.AdminEmail {
			mine = append(mine, ev)
		}
	}
	return mine
}

func TestAuditRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAuditRepository(db)
	action := fmt.Sprintf("test.%d", time.Now().UnixNano())
	target := fmt.Sprintf("target-%d", time.Now().UnixNano())

	first := testAuditEntry(t, db, action)
	first.Via, first.IP = rss.AuditViaWeb, "203.0.113.7"
	first.TargetType, first.TargetID = "user", target
	first.Before = map[string]any{"name": "<b>Ada</b>", "admin": false}
	first.After = map[string]any{"name": "Grace", "admin": true}
	if err := repo.Record(t.Context(), first); err != nil {
		t.Fatalf("Record: %v", err)
	}
	second := testAuditEntry(t, db, action)
	if err := repo.Record(t.Context(), second); err != nil {
		t.Fatalf("Record: %v", err)
	}
	third := testAuditEntry(t, db, action)
	third.TargetType, third.TargetID = "user", target
	if err := repo.Record(t.Context(), third); err != nil {
		t.Fatalf("Record: %v", err)
	}

	t.Run("List is newest first and keeps the fields", func(t *testing.T) {
		got, err := repo.List(t.Context(), rss.AuditFilter{Action: action, Limit: 10})
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

	t.Run("List filters by target and pages with Before", func(t *testing.T) {
		got, err := repo.List(t.Context(), rss.AuditFilter{TargetType: "user", TargetID: target, Limit: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 || got[0].AdminEmail != third.AdminEmail || got[1].AdminEmail != first.AdminEmail {
			t.Fatalf("List by target = %+v, want third and first", got)
		}
		page, err := repo.List(t.Context(), rss.AuditFilter{Action: action, Limit: 1})
		if err != nil || len(page) != 1 {
			t.Fatalf("List one = %v, %v", page, err)
		}
		next, err := repo.List(t.Context(), rss.AuditFilter{Action: action, Before: page[0].ID, Limit: 10})
		if err != nil {
			t.Fatalf("List before: %v", err)
		}
		if len(next) != 2 || next[0].AdminEmail != second.AdminEmail {
			t.Errorf("List before the newest = %+v, want second and first", next)
		}
	})

	t.Run("Record fills the admin's email from its ID", func(t *testing.T) {
		email := fmt.Sprintf("admin-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM admin_audit WHERE admin_email = $1`, email); err != nil {
				t.Errorf("cleanup audit: %v", err)
			}
			if _, err := db.Exec(`DELETE FROM admins WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup admin: %v", err)
			}
		})
		id, err := NewAdminRepository(db).Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), testAuditEntry(t, db, "admin.create"))
		if err != nil {
			t.Fatalf("Create admin: %v", err)
		}
		loginAction := action + ".login"
		if err := repo.Record(t.Context(), rss.AuditEntry{AdminID: id, Via: rss.AuditViaWeb, Action: loginAction}); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := repo.List(t.Context(), rss.AuditFilter{Action: loginAction, Limit: 10})
		if err != nil || len(got) != 1 {
			t.Fatalf("List = %v, %v", got, err)
		}
		if got[0].AdminID != id || got[0].AdminEmail != email {
			t.Errorf("entry admin = %q, %q; want %q, %q", got[0].AdminID, got[0].AdminEmail, id, email)
		}
	})
}

// TestAuditInTransaction checks that an action's change and its audit row
// commit together, and that neither does when recording fails: an entry
// with an invalid Via breaks the table's CHECK.
func TestAuditInTransaction(t *testing.T) {
	db := psqltest.NewDB(t)

	t.Run("InviteRepository.Create", func(t *testing.T) {
		bad := testAuditEntry(t, db, "invite.create")
		bad.Via = "bogus"
		hash := newWaitlistHash(t)
		if _, err := NewInviteRepository(db).Create(t.Context(), rss.Invite{Note: "never stored", ExpiresAt: time.Now().Add(time.Hour)}, hash, bad); err == nil {
			t.Fatal("Create with a broken audit entry succeeded")
		}
		if _, err := NewInviteRepository(db).GetByToken(t.Context(), hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the invite was stored without its record (GetByToken error = %v)", err)
		}

		e := testAuditEntry(t, db, "invite.create")
		e.TargetType = "invite"
		hash = newWaitlistHash(t)
		got, err := NewInviteRepository(db).Create(t.Context(), rss.Invite{Note: "stored", ExpiresAt: time.Now().Add(time.Hour)}, hash, e)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Cleanup(func() { db.Exec(`DELETE FROM invites WHERE id = $1`, got.ID) })
		rows := auditRows(t, db, e)
		if len(rows) != 1 || rows[0].TargetType != "invite" || rows[0].TargetID != got.ID || rows[0].Via != "cli" {
			t.Errorf("audit rows = %+v, want one for invite %s", rows, got.ID)
		}
	})

	t.Run("InviteRepository.Revoke", func(t *testing.T) {
		inv, hash := newInvite(t, db, rss.Invite{Note: "to revoke"})
		repo := NewInviteRepository(db)

		bad := testAuditEntry(t, db, "invite.revoke")
		bad.Via = "bogus"
		if err := repo.Revoke(t.Context(), inv.ID, bad); err == nil {
			t.Fatal("Revoke with a broken audit entry succeeded")
		}
		if got, _ := repo.GetByToken(t.Context(), hash); got.Status(time.Now()) != rss.InvitePending {
			t.Errorf("the invite was revoked without its record: %q", got.Status(time.Now()))
		}

		e := testAuditEntry(t, db, "invite.revoke")
		e.TargetType = "invite"
		if err := repo.Revoke(t.Context(), inv.ID, e); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		// Revoking again changes, and records, nothing.
		if err := repo.Revoke(t.Context(), inv.ID, e); err != nil {
			t.Fatalf("Revoke again: %v", err)
		}
		rows := auditRows(t, db, e)
		if len(rows) != 1 {
			t.Fatalf("audit rows = %+v, want one", rows)
		}
		r := rows[0]
		if r.TargetType != "invite" || r.TargetID != inv.ID {
			t.Errorf("target = %s %s, want invite %s", r.TargetType, r.TargetID, inv.ID)
		}
		if v, ok := r.Before["revoked_at"]; !ok || v != nil {
			t.Errorf("before = %v, want revoked_at null", r.Before)
		}
		if v, _ := r.After["revoked_at"].(string); v == "" {
			t.Errorf("after = %v, want revoked_at's time", r.After)
		}
	})

	t.Run("AdminRepository.Create", func(t *testing.T) {
		email := fmt.Sprintf("admin-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() { db.Exec(`DELETE FROM admins WHERE email = $1`, email) })
		repo := NewAdminRepository(db)

		bad := testAuditEntry(t, db, "admin.create")
		bad.Via = "bogus"
		if _, err := repo.Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), bad); err == nil {
			t.Fatal("Create with a broken audit entry succeeded")
		}
		if _, err := repo.GetByEmail(t.Context(), email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the admin was stored without its record (GetByEmail error = %v)", err)
		}

		e := testAuditEntry(t, db, "admin.create")
		id, err := repo.Create(t.Context(), email, newAdminCredentials(t, "pw", "s"), e)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if rows := auditRows(t, db, e); len(rows) != 1 || rows[0].TargetID != id {
			t.Errorf("audit rows = %+v, want one for admin %s", rows, id)
		}
	})

	t.Run("UserRepository.Create", func(t *testing.T) {
		email := fmt.Sprintf("user-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE email = $1`, email) })
		repo := NewUserRepository(db)

		bad := testAuditEntry(t, db, "user.create")
		bad.Via = "bogus"
		if _, _, err := repo.Create(t.Context(), "Audited", email, "correct-horse-battery", &bad); err == nil {
			t.Fatal("Create with a broken audit entry succeeded")
		}
		if _, err := repo.GetByEmail(t.Context(), email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("the user was stored without its record (GetByEmail error = %v)", err)
		}

		e := testAuditEntry(t, db, "user.create")
		id, _, err := repo.Create(t.Context(), "Audited", email, "correct-horse-battery", &e)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if rows := auditRows(t, db, e); len(rows) != 1 || rows[0].TargetID != id {
			t.Errorf("audit rows = %+v, want one for user %s", rows, id)
		}
	})
}
