package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/lib/pq"
)

// newWaitlistEmail returns a unique address whose entry is deleted when the
// test ends.
func newWaitlistEmail(t *testing.T, db *sql.DB) string {
	t.Helper()
	email := fmt.Sprintf("test-waitlist-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM waitlist WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup waitlist: %v", err)
		}
	})
	return email
}

func newWaitlistHash(t *testing.T) []byte {
	t.Helper()
	_, hash, err := token.New()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestWaitlistRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewWaitlistRepository(db)
	ctx := t.Context()

	join := func(t *testing.T, e rss.WaitlistEntry) []byte {
		t.Helper()
		hash := newWaitlistHash(t)
		if _, err := repo.Join(ctx, e, hash); err != nil {
			t.Fatalf("Join: %v", err)
		}
		return hash
	}

	t.Run("Join stores an unconfirmed entry", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		e := rss.WaitlistEntry{Email: email, Name: "Ada", Sites: "kottke.org", Source: "spring"}
		hash := newWaitlistHash(t)
		got, err := repo.Join(ctx, e, hash)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID == "" || got.CreatedAt.IsZero() || !got.ConfirmedAt.IsZero() {
			t.Errorf("Join = %+v, want an ID, a created_at and no confirmed_at", got)
		}
		e.ID, e.CreatedAt = got.ID, got.CreatedAt
		if got != e {
			t.Errorf("Join = %+v, want %+v", got, e)
		}
		byToken, err := repo.GetByToken(ctx, hash)
		if err != nil || byToken != got {
			t.Errorf("GetByToken = %+v, %v; want %+v", byToken, err, got)
		}
	})

	t.Run("Join again while unconfirmed replaces the entry's details and token", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		first := rss.WaitlistEntry{Email: email, Name: "Ada", Sites: "kottke.org", Source: "spring"}
		oldHash := join(t, first)
		before, _ := repo.GetByToken(ctx, oldHash)

		newHash := newWaitlistHash(t)
		got, err := repo.Join(ctx, rss.WaitlistEntry{Email: strings.ToUpper(email), Name: "Ada King"}, newHash)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != before.ID || !got.CreatedAt.Equal(before.CreatedAt) {
			t.Errorf("Join made a new entry: %+v, was %+v", got, before)
		}
		if got.Name != "Ada King" || got.Sites != "" || got.Source != "" {
			t.Errorf("Join = %+v, want the new details", got)
		}
		if _, err := repo.GetByToken(ctx, oldHash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByToken(old) error = %v, want ErrNoRecord", err)
		}
		if _, err := repo.GetByToken(ctx, newHash); err != nil {
			t.Errorf("GetByToken(new): %v", err)
		}
	})

	t.Run("Join with a confirmed address is a duplicate and changes nothing", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		hash := join(t, rss.WaitlistEntry{Email: email, Name: "Ada"})
		if err := repo.Confirm(ctx, hash); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Join(ctx, rss.WaitlistEntry{Email: email, Name: "Mallory"}, newWaitlistHash(t))
		if !errors.Is(err, rss.ErrDuplicateEmail) {
			t.Fatalf("Join error = %v, want ErrDuplicateEmail", err)
		}
		got, err := repo.GetByToken(ctx, hash)
		if err != nil || got.Name != "Ada" {
			t.Errorf("GetByToken = %+v, %v; want Ada, with the old token", got, err)
		}
	})

	t.Run("Confirm sets confirmed_at once", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		hash := join(t, rss.WaitlistEntry{Email: email, Name: "Ada"})
		if err := repo.Confirm(ctx, hash); err != nil {
			t.Fatal(err)
		}
		first, _ := repo.GetByToken(ctx, hash)
		if first.ConfirmedAt.IsZero() {
			t.Fatal("not confirmed")
		}
		if err := repo.Confirm(ctx, hash); err != nil {
			t.Fatalf("Confirm again: %v", err)
		}
		again, _ := repo.GetByToken(ctx, hash)
		if !again.ConfirmedAt.Equal(first.ConfirmedAt) {
			t.Errorf("confirmed_at changed from %v to %v", first.ConfirmedAt, again.ConfirmedAt)
		}
	})

	t.Run("Leave deletes the entry", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		hash := join(t, rss.WaitlistEntry{Email: email, Name: "Ada"})
		if err := repo.Leave(ctx, hash); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM waitlist WHERE email = $1`, email).Scan(&n); err != nil || n != 0 {
			t.Errorf("%d rows left (%v), want 0", n, err)
		}
	})

	t.Run("unknown tokens", func(t *testing.T) {
		hash := newWaitlistHash(t)
		if _, err := repo.GetByToken(ctx, hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByToken error = %v, want ErrNoRecord", err)
		}
		if err := repo.Confirm(ctx, hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Confirm error = %v, want ErrNoRecord", err)
		}
		if err := repo.Leave(ctx, hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Leave error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("CHECK on the length of sites", func(t *testing.T) {
		email := newWaitlistEmail(t, db)
		if _, err := repo.Join(ctx, rss.WaitlistEntry{Email: email, Name: "Ada", Sites: strings.Repeat("é", 500)}, newWaitlistHash(t)); err != nil {
			t.Fatalf("Join with 500 characters: %v", err)
		}
		_, err := repo.Join(ctx, rss.WaitlistEntry{Email: email, Name: "Ada", Sites: strings.Repeat("é", 501)}, newWaitlistHash(t))
		var pqErr *pq.Error
		if !errors.As(err, &pqErr) || pqErr.Code != "23514" {
			t.Errorf("Join error = %v, want a check violation", err)
		}
	})
}
