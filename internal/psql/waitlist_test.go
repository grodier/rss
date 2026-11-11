package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
		byEmail, err := repo.GetByEmail(ctx, strings.ToUpper(email))
		if err != nil || byEmail != got {
			t.Errorf("GetByEmail = %+v, %v; want %+v", byEmail, err, got)
		}
		if _, err := repo.GetByEmail(ctx, "nobody-"+email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByEmail(unknown) error = %v, want ErrNoRecord", err)
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

// confirmedEntry joins and confirms a unique entry named name, joined at
// joined; it's deleted when the test ends, with its invites and emails.
func confirmedEntry(t *testing.T, db *sql.DB, name string, joined time.Time) rss.WaitlistEntry {
	t.Helper()
	repo := NewWaitlistRepository(db)
	email := newWaitlistEmail(t, db)
	cleanupExec(t, db, `DELETE FROM invites WHERE email = $1`, email)
	t.Cleanup(func() { deleteEmailsTo(t, db, email) })
	hash := newWaitlistHash(t)
	e, err := repo.Join(t.Context(), rss.WaitlistEntry{Email: email, Name: name}, hash)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := repo.Confirm(t.Context(), hash); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := db.Exec(`UPDATE waitlist SET created_at = $2 WHERE id = $1`, e.ID, joined); err != nil {
		t.Fatal(err)
	}
	e, err = repo.GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWaitlistAdminQueries(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewWaitlistRepository(db)
	ctx := t.Context()

	before, err := repo.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Joined in 1990, so they're older than any other test's entries.
	day := func(d int) time.Time { return time.Date(1990, 1, d, 0, 0, 0, 0, time.UTC) }
	waiting := confirmedEntry(t, db, "Waiting", day(1))
	expired := confirmedEntry(t, db, "Expired", day(2))
	invited := confirmedEntry(t, db, "Invited", day(3))
	revoked := confirmedEntry(t, db, "Revoked", day(4))
	unconfirmedEmail := newWaitlistEmail(t, db)
	unconfirmed, err := repo.Join(ctx, rss.WaitlistEntry{Email: unconfirmedEmail, Name: "Unconfirmed"}, newWaitlistHash(t))
	if err != nil {
		t.Fatal(err)
	}

	newInvite(t, db, rss.Invite{Email: expired.Email, WaitlistID: expired.ID, ExpiresAt: time.Now().Add(-time.Hour)})
	// An older, expired invite, then the pending one: the row shows the latest.
	newInvite(t, db, rss.Invite{Email: invited.Email, WaitlistID: invited.ID, ExpiresAt: time.Now().Add(-time.Hour)})
	pending, _ := newInvite(t, db, rss.Invite{Email: invited.Email, WaitlistID: invited.ID, Wave: "W", ExpiresAt: time.Now().Add(time.Hour)})
	r, _ := newInvite(t, db, rss.Invite{Email: revoked.Email, WaitlistID: revoked.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err := NewInviteRepository(db).Revoke(ctx, r.ID, testActor(t, db)); err != nil {
		t.Fatal(err)
	}

	t.Run("Counts", func(t *testing.T) {
		got, err := repo.Counts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := rss.WaitlistCounts{Waiting: before.Waiting + 3, Invited: before.Invited + 1, Unconfirmed: before.Unconfirmed + 1}
		if got != want {
			t.Errorf("Counts = %+v, want %+v", got, want)
		}
	})

	ours := map[string]bool{waiting.ID: true, expired.ID: true, invited.ID: true, revoked.ID: true, unconfirmed.ID: true}
	list := func(t *testing.T, tab string) []rss.WaitlistRow {
		t.Helper()
		rows, err := repo.List(ctx, tab, 100_000, 0)
		if err != nil {
			t.Fatalf("List(%s): %v", tab, err)
		}
		return slices.DeleteFunc(rows, func(r rss.WaitlistRow) bool { return !ours[r.ID] })
	}

	t.Run("List's tabs, oldest first, with the latest invite", func(t *testing.T) {
		names := func(rows []rss.WaitlistRow) []string {
			var n []string
			for _, r := range rows {
				n = append(n, r.Name)
			}
			return n
		}
		if got := names(list(t, rss.WaitlistWaiting)); !slices.Equal(got, []string{"Waiting", "Expired", "Revoked"}) {
			t.Errorf("waiting = %v", got)
		}
		inv := list(t, rss.WaitlistInvited)
		if got := names(inv); !slices.Equal(got, []string{"Invited"}) {
			t.Fatalf("invited = %v", got)
		}
		if inv[0].Invite.ID != pending.ID || inv[0].Invite.Wave != "W" || inv[0].Invite.Status(time.Now()) != rss.InvitePending {
			t.Errorf("invited row's invite = %+v, want the pending one", inv[0].Invite)
		}
		if got := names(list(t, rss.WaitlistUnconfirmed)); !slices.Equal(got, []string{"Unconfirmed"}) {
			t.Errorf("unconfirmed = %v", got)
		}
		rows := list(t, rss.WaitlistWaiting)
		if rows[0].Invite.ID != "" || rows[1].Invite.Status(time.Now()) != rss.InviteExpired || rows[2].Invite.Status(time.Now()) != rss.InviteRevoked {
			t.Errorf("waiting rows' invites = %+v, %+v, %+v; want none, expired, revoked", rows[0].Invite, rows[1].Invite, rows[2].Invite)
		}
		if rows[0].Email != waiting.Email || rows[0].ConfirmedAt.IsZero() || !rows[0].CreatedAt.Equal(day(1)) {
			t.Errorf("row = %+v", rows[0])
		}
	})

	t.Run("List pages", func(t *testing.T) {
		first, err := repo.List(ctx, rss.WaitlistWaiting, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.List(ctx, rss.WaitlistWaiting, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 2 || first[0].ID != waiting.ID || first[1].ID != expired.ID || len(second) == 0 || second[0].ID != revoked.ID {
			t.Errorf("pages = %v, %v", first, second)
		}
		if _, err := repo.List(ctx, "everyone", 1, 0); err == nil {
			t.Error("List(unknown tab) succeeded")
		}
	})

	t.Run("ListWaiting keeps only the waiting", func(t *testing.T) {
		got, err := repo.ListWaiting(ctx, []string{revoked.ID, invited.ID, unconfirmed.ID, waiting.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != waiting.ID || got[1].ID != revoked.ID {
			t.Errorf("ListWaiting = %+v, want Waiting then Revoked", got)
		}
	})

	t.Run("Oldest", func(t *testing.T) {
		got, err := repo.Oldest(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != waiting.ID || got[1].ID != expired.ID {
			t.Errorf("Oldest(2) = %+v, want Waiting then Expired", got)
		}
	})
}
