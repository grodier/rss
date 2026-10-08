package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestUser inserts a user and deletes it (and its subscriptions) on cleanup.
func newTestUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	email := fmt.Sprintf("subs-test-%d@example.com", time.Now().UnixNano())
	id, _, err := NewUserRepository(db).Create(t.Context(), "Test User", email, "correct-horse-battery")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	return id
}

func subscriptionCount(t *testing.T, db *sql.DB, userID, feedID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, userID, feedID).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	t.Run("subscribe then SubscribedFeedIDs reports the feed", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		got, err := repo.SubscribedFeedIDs(ctx, user, []string{feed.ID})
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if !got[feed.ID] || len(got) != 1 {
			t.Errorf("SubscribedFeedIDs = %v, want only %s", got, feed.ID)
		}
	})

	t.Run("subscribing twice is not an error and leaves one row", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		for range 2 {
			if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
		if n := subscriptionCount(t, db, user, feed.ID); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})

	t.Run("unsubscribe removes it", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)
		if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}

		if err := repo.Unsubscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if n := subscriptionCount(t, db, user, feed.ID); n != 0 {
			t.Errorf("rows = %d, want 0", n)
		}
	})

	t.Run("unsubscribing when not subscribed is not an error", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		if err := repo.Unsubscribe(ctx, user, feed.ID); err != nil {
			t.Errorf("Unsubscribe: %v", err)
		}
	})

	t.Run("SubscribedFeedIDs excludes other users and feeds not asked about", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		mine := newTestFeed(t, db)
		theirs := newTestFeed(t, db)
		unasked := newTestFeed(t, db)
		for _, s := range []struct{ user, feed string }{
			{user, mine.ID}, {user, unasked.ID}, {other, theirs.ID},
		} {
			if err := repo.Subscribe(ctx, s.user, s.feed); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}

		got, err := repo.SubscribedFeedIDs(ctx, user, []string{mine.ID, theirs.ID})
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if !got[mine.ID] || len(got) != 1 {
			t.Errorf("SubscribedFeedIDs = %v, want only %s", got, mine.ID)
		}
	})

	t.Run("SubscribedFeedIDs with no feed IDs returns an empty map", func(t *testing.T) {
		got, err := repo.SubscribedFeedIDs(ctx, newTestUser(t, db), nil)
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("SubscribedFeedIDs = %v, want an empty non-nil map", got)
		}
	})

	t.Run("subscribing to a nonexistent feed returns ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.Subscribe(ctx, user, "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}
