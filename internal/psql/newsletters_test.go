package psql

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func TestNewsletterRepository(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	repo := NewNewsletterRepository(db)

	newUser := func(t *testing.T) string {
		t.Helper()
		email := fmt.Sprintf("test-newsletter-%d@example.com", psqltest.Unique())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		id, _, err := users.Create(t.Context(), "Newsletter Reader", email, "correct-horse-battery", nil)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return id
	}
	localPart := func(prefix string) string { return fmt.Sprintf("%s.t%d", prefix, psqltest.Unique()) }
	// age moves the user's current address's created_at back a day and a
	// minute, so a new one is allowed.
	age := func(t *testing.T, userID string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE newsletter_addresses SET created_at = now() - interval '1 day 1 minute'
			WHERE user_id = $1 AND retired_at IS NULL`, userID); err != nil {
			t.Fatalf("age address: %v", err)
		}
	}

	t.Run("no address", func(t *testing.T) {
		t.Parallel()
		if _, err := repo.CurrentAddress(t.Context(), newUser(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("CurrentAddress = %v, want ErrNoRecord", err)
		}
	})

	t.Run("first address", func(t *testing.T) {
		t.Parallel()
		userID := newUser(t)
		lp := localPart("first")
		a, err := repo.NewAddress(t.Context(), userID, lp)
		if err != nil {
			t.Fatalf("NewAddress: %v", err)
		}
		if a.UserID != userID || a.LocalPart != lp || a.ID == "" || a.CreatedAt.IsZero() || !a.RetiredAt.IsZero() {
			t.Errorf("NewAddress = %+v", a)
		}
		got, err := repo.CurrentAddress(t.Context(), userID)
		if err != nil || got.ID != a.ID {
			t.Errorf("CurrentAddress = %+v, %v; want %s", got, err, a.ID)
		}
	})

	t.Run("a new address retires the current one", func(t *testing.T) {
		t.Parallel()
		userID := newUser(t)
		first, err := repo.NewAddress(t.Context(), userID, localPart("old"))
		if err != nil {
			t.Fatal(err)
		}
		age(t, userID)
		second, err := repo.NewAddress(t.Context(), userID, localPart("new"))
		if err != nil {
			t.Fatalf("NewAddress: %v", err)
		}
		got, err := repo.CurrentAddress(t.Context(), userID)
		if err != nil || got.ID != second.ID {
			t.Errorf("CurrentAddress = %+v, %v; want %s", got, err, second.ID)
		}
		var retired bool
		if err := db.QueryRow(`SELECT retired_at IS NOT NULL FROM newsletter_addresses WHERE id = $1`, first.ID).Scan(&retired); err != nil {
			t.Fatal(err)
		}
		if !retired {
			t.Error("the first address isn't retired")
		}
	})

	t.Run("at most one a day", func(t *testing.T) {
		t.Parallel()
		userID := newUser(t)
		first, err := repo.NewAddress(t.Context(), userID, localPart("daily"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.NewAddress(t.Context(), userID, localPart("again")); !errors.Is(err, rss.ErrAddressTooRecent) {
			t.Fatalf("NewAddress = %v, want ErrAddressTooRecent", err)
		}
		got, err := repo.CurrentAddress(t.Context(), userID)
		if err != nil || got.ID != first.ID {
			t.Errorf("CurrentAddress = %+v, %v; want the first, unchanged", got, err)
		}
	})

	t.Run("two at once make one", func(t *testing.T) {
		t.Parallel()
		userID := newUser(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = repo.NewAddress(t.Context(), userID, localPart(fmt.Sprintf("race%d", i)))
			}()
		}
		wg.Wait()
		ok, tooRecent := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, rss.ErrAddressTooRecent):
				tooRecent++
			default:
				t.Errorf("NewAddress: %v", err)
			}
		}
		if ok != 1 || tooRecent != 1 {
			t.Errorf("%d made and %d refused as too recent, want 1 and 1", ok, tooRecent)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM newsletter_addresses WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%d addresses, want 1", n)
		}
	})

	t.Run("local parts are unique ignoring case", func(t *testing.T) {
		t.Parallel()
		lp := localPart("taken")
		if _, err := repo.NewAddress(t.Context(), newUser(t), lp); err != nil {
			t.Fatal(err)
		}
		other := newUser(t)
		if _, err := repo.NewAddress(t.Context(), other, strings.ToUpper(lp)); !errors.Is(err, rss.ErrDuplicateAddress) {
			t.Errorf("NewAddress = %v, want ErrDuplicateAddress", err)
		}
		if _, err := repo.CurrentAddress(t.Context(), other); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("CurrentAddress after a duplicate = %v, want ErrNoRecord", err)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		t.Parallel()
		if _, err := repo.NewAddress(t.Context(), "00000000-0000-0000-0000-000000000000", localPart("nobody")); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("NewAddress = %v, want ErrNoRecord", err)
		}
	})
}
