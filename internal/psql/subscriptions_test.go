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

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionRepositoryListByUser(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	// newSite inserts a site with the given title; empty means no title.
	newSite := func(t *testing.T, tag, title string) string {
		t.Helper()
		host := fmt.Sprintf("%s-%d.example.com", tag, time.Now().UnixNano())
		id, err := NewSiteRepository(db).Upsert(ctx, rss.Site{Host: host, URL: "https://" + host + "/", Title: title})
		if err != nil {
			t.Fatalf("Upsert site: %v", err)
		}
		t.Cleanup(func() { db.Exec(`DELETE FROM sites WHERE id = $1`, id) })
		return id
	}
	newFeed := func(t *testing.T, siteID, title string) string {
		t.Helper()
		id, err := NewFeedRepository(db).Upsert(ctx, rss.Feed{
			SiteID: siteID, Title: title, NextFetch: time.Now().Add(time.Hour),
			Url: fmt.Sprintf("https://example.com/%s-%d.xml", title, time.Now().UnixNano()),
		})
		if err != nil {
			t.Fatalf("Upsert feed: %v", err)
		}
		return id
	}
	subscribe := func(t *testing.T, user string, feeds ...string) {
		t.Helper()
		for _, f := range feeds {
			if err := repo.Subscribe(ctx, user, f); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}

	t.Run("returns only this user's subscriptions", func(t *testing.T) {
		user, other := newTestUser(t, db), newTestUser(t, db)
		site := newSite(t, "own", "Own")
		mine, theirs := newFeed(t, site, "mine"), newFeed(t, site, "theirs")
		subscribe(t, user, mine)
		subscribe(t, other, theirs)

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 1 || got[0].Feed.ID != mine || got[0].Site.ID != site || got[0].Site.Title != "Own" {
			t.Errorf("got %+v, want only feed %s on site %s", got, mine, site)
		}
	})

	t.Run("ordered by site title or host, then feed title", func(t *testing.T) {
		user := newTestUser(t, db)
		// Hosts start with the tag, so an untitled site sorts by its host.
		bravo := newSite(t, "bravo", "")
		alpha := newSite(t, "alpha", "Zulu title")
		mike := newSite(t, "mike", "mike title")
		f1, f2 := newFeed(t, bravo, "b-second"), newFeed(t, bravo, "a-first")
		f3, f4 := newFeed(t, alpha, "x"), newFeed(t, mike, "y")
		subscribe(t, user, f1, f2, f3, f4)

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		var ids []string
		for _, s := range got {
			ids = append(ids, s.Feed.ID)
		}
		// bravo-host (a-first, b-second), then "mike title", then "Zulu title".
		want := []string{f2, f1, f4, f3}
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Errorf("order = %v, want %v", ids, want)
		}
	})

	t.Run("LatestArticleAt", func(t *testing.T) {
		user := newTestUser(t, db)
		site := newSite(t, "latest", "Latest")
		withPublished, withoutDate, empty := newFeed(t, site, "a-pub"), newFeed(t, site, "b-nodate"), newFeed(t, site, "c-empty")
		subscribe(t, user, withPublished, withoutDate, empty)
		insert := func(feed, ext, published string) {
			t.Helper()
			var pub any
			if published != "" {
				pub = published
			}
			if _, err := db.Exec(`INSERT INTO articles (feed_id, external_id, published_at) VALUES ($1, $2, $3)`, feed, ext, pub); err != nil {
				t.Fatalf("insert article: %v", err)
			}
		}
		insert(withPublished, "old", "2020-01-01T00:00:00Z")
		insert(withPublished, "new", "2021-06-01T00:00:00Z")
		insert(withoutDate, "nodate", "")
		var createdAt time.Time
		if err := db.QueryRow(`SELECT created_at FROM articles WHERE feed_id = $1`, withoutDate).Scan(&createdAt); err != nil {
			t.Fatal(err)
		}

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d subscriptions, want 3", len(got))
		}
		if want := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC); !got[0].LatestArticleAt.Equal(want) {
			t.Errorf("published: got %v, want %v", got[0].LatestArticleAt, want)
		}
		if !got[1].LatestArticleAt.Equal(createdAt) {
			t.Errorf("no published_at: got %v, want created_at %v", got[1].LatestArticleAt, createdAt)
		}
		if !got[2].LatestArticleAt.IsZero() {
			t.Errorf("no articles: got %v, want zero", got[2].LatestArticleAt)
		}
	})

	t.Run("no subscriptions is an empty result", func(t *testing.T) {
		got, err := repo.ListByUser(ctx, newTestUser(t, db))
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}
