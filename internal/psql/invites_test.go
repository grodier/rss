package psql

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newInvite stores inv with a new token and returns it with the token's
// hash; it's deleted when the test ends.
func newInvite(t *testing.T, db *sql.DB, inv rss.Invite) (rss.Invite, []byte) {
	t.Helper()
	hash := newWaitlistHash(t)
	if inv.ExpiresAt.IsZero() {
		inv.ExpiresAt = time.Now().Add(14 * 24 * time.Hour)
	}
	got, err := NewInviteRepository(db).Create(t.Context(), inv, hash)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM invites WHERE id = $1`, got.ID); err != nil {
			t.Errorf("cleanup invite: %v", err)
		}
	})
	return got, hash
}

func TestInviteRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewInviteRepository(db)
	waitlist := NewWaitlistRepository(db)

	email := newWaitlistEmail(t, db)
	entry, err := waitlist.Join(t.Context(), rss.WaitlistEntry{Email: email, Name: "Ada"}, newWaitlistHash(t))
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	expires := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	bound, boundHash := newInvite(t, db, rss.Invite{Email: email, WaitlistID: entry.ID, Wave: "wave-1", ExpiresAt: expires})
	open, openHash := newInvite(t, db, rss.Invite{Note: "for Grace"})

	t.Run("Create returns the stored invite", func(t *testing.T) {
		if bound.ID == "" || bound.CreatedAt.IsZero() {
			t.Errorf("Create = %+v, want an ID and created_at", bound)
		}
		if bound.Email != email || bound.WaitlistID != entry.ID || bound.Name != "Ada" || bound.Wave != "wave-1" || !bound.ExpiresAt.Equal(expires) {
			t.Errorf("Create = %+v", bound)
		}
		if st := bound.Status(time.Now()); st != rss.InvitePending {
			t.Errorf("Status = %q, want pending", st)
		}
		if open.Email != "" || open.WaitlistID != "" || open.Name != "" || open.Note != "for Grace" {
			t.Errorf("open invite = %+v", open)
		}
	})

	t.Run("GetByToken", func(t *testing.T) {
		got, err := repo.GetByToken(t.Context(), boundHash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if got.ID != bound.ID || got.Name != "Ada" || got.Email != email {
			t.Errorf("GetByToken = %+v, want %+v", got, bound)
		}
		if _, err := repo.GetByToken(t.Context(), newWaitlistHash(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByToken(unknown) error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("List has both, newest first", func(t *testing.T) {
		all, err := repo.List(t.Context())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ib := slices.IndexFunc(all, func(i rss.Invite) bool { return i.ID == bound.ID })
		io := slices.IndexFunc(all, func(i rss.Invite) bool { return i.ID == open.ID })
		if ib < 0 || io < 0 || io > ib {
			t.Errorf("List: bound at %d, open at %d; want both, open first", ib, io)
		}
	})

	t.Run("Revoke", func(t *testing.T) {
		if err := repo.Revoke(t.Context(), open.ID); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		got, err := repo.GetByToken(t.Context(), openHash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if st := got.Status(time.Now()); st != rss.InviteRevoked {
			t.Errorf("Status = %q, want revoked", st)
		}
		// Revoking again keeps it revoked.
		if err := repo.Revoke(t.Context(), open.ID); err != nil {
			t.Errorf("Revoke again: %v", err)
		}
		if err := repo.Revoke(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Revoke(unknown) error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("leaving the waitlist unlinks the invite", func(t *testing.T) {
		inv, hash := newInvite(t, db, rss.Invite{Email: email, WaitlistID: entry.ID})
		if _, err := db.Exec(`DELETE FROM waitlist WHERE id = $1`, entry.ID); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByToken(t.Context(), hash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if got.ID != inv.ID || got.WaitlistID != "" || got.Name != "" || got.Email != email {
			t.Errorf("GetByToken = %+v, want no entry and the email kept", got)
		}
	})
}
