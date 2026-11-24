package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// newTestSite inserts a site and deletes it (and its feeds) on cleanup.
func newTestSite(t *testing.T, db *sql.DB) string {
	t.Helper()
	host := fmt.Sprintf("feeds-test-%d.example.com", psqltest.Unique())
	id, err := upsertSite(t.Context(), db, rss.Site{Host: host, URL: "https://" + host + "/"}, true)
	if err != nil {
		t.Fatalf("Upsert site: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM sites WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
	})
	return id
}

// dbNow returns the database's now(); compare stored timestamps against it,
// not the test's clock.
func dbNow(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	var now time.Time
	if err := db.QueryRow(`SELECT now()`).Scan(&now); err != nil {
		t.Fatalf("select now(): %v", err)
	}
	return now
}

// setNextFetch sets feed id's next_fetch_at.
func setNextFetch(t *testing.T, db *sql.DB, id string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE feeds SET next_fetch_at = $2 WHERE id = $1`, id, at); err != nil {
		t.Fatalf("set next_fetch_at: %v", err)
	}
}

// setRefreshRequested sets feed id's refresh_requested_at to now().
func setRefreshRequested(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE feeds SET refresh_requested_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("set refresh_requested_at: %v", err)
	}
}

// newSubscribedFeed is newTestFeed with a subscription by userID, so
// ClaimDue considers it.
func newSubscribedFeed(t *testing.T, db *sql.DB, userID string) rss.Feed {
	t.Helper()
	f := newTestFeed(t, db)
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), userID, f.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return f
}

// addToDiscover puts feed id in Discover's catalog, under its first
// section. Deleting the feed deletes the row.
func addToDiscover(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO discover_feeds (feed_id, section_id)
		SELECT $1, id FROM discover_sections ORDER BY position LIMIT 1`, id); err != nil {
		t.Fatalf("insert discover_feeds: %v", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryClaimDue(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	userID := newTestUser(t, db)
	const lease = 10 * time.Minute
	// Other tests share the database, so make this test's feeds due longest.
	longAgo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	// claim claims a feed and fails unless it is want.
	claim := func(t *testing.T, want rss.Feed) {
		t.Helper()
		got, err := repo.ClaimDue(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimDue: %v", err)
		}
		if got.ID != want.ID {
			t.Fatalf("claimed %q; want %q", got.ID, want.ID)
		}
	}

	t.Run("claims the feed due longest and leases it", func(t *testing.T) {
		first, second := newSubscribedFeed(t, db, userID), newSubscribedFeed(t, db, userID)
		setNextFetch(t, db, first.ID, longAgo)
		setNextFetch(t, db, second.ID, longAgo.Add(time.Hour))

		before := dbNow(t, db)
		got, err := repo.ClaimDue(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimDue: %v", err)
		}
		if got.ID != first.ID {
			t.Fatalf("claimed %q; want %q", got.ID, first.ID)
		}
		if lo, hi := before.Add(lease), dbNow(t, db).Add(lease); got.NextFetch.Before(lo) || got.NextFetch.After(hi) {
			t.Errorf("got NextFetch %v; want within [%v, %v]", got.NextFetch, lo, hi)
		}
		if stored, err := repo.GetByID(t.Context(), first.ID); err != nil || !stored.NextFetch.Equal(got.NextFetch) {
			t.Errorf("stored NextFetch %v, err %v; want %v", stored.NextFetch, err, got.NextFetch)
		}

		// The claimed feed is leased, so the next claim takes the other one.
		got, err = repo.ClaimDue(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimDue again: %v", err)
		}
		if got.ID != second.ID {
			t.Errorf("second claim got %q; want %q", got.ID, second.ID)
		}
	})

	t.Run("skips gone feeds", func(t *testing.T) {
		gone, live := newSubscribedFeed(t, db, userID), newSubscribedFeed(t, db, userID)
		setNextFetch(t, db, gone.ID, longAgo.Add(-time.Hour))
		setNextFetch(t, db, live.ID, longAgo)
		if err := repo.MarkGone(t.Context(), gone.ID); err != nil {
			t.Fatalf("MarkGone: %v", err)
		}

		got, err := repo.ClaimDue(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimDue: %v", err)
		}
		if got.ID != live.ID {
			t.Errorf("claimed %q; want %q (not the gone feed %q)", got.ID, live.ID, gone.ID)
		}
	})

	t.Run("concurrent claims never return the same feed", func(t *testing.T) {
		var ids []string
		for range 2 {
			f := newSubscribedFeed(t, db, userID)
			setNextFetch(t, db, f.ID, longAgo)
			ids = append(ids, f.ID)
		}

		var wg sync.WaitGroup
		got := make([]rss.Feed, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() {
				got[i], errs[i] = repo.ClaimDue(t.Context(), lease)
			})
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("ClaimDue %d: %v", i, err)
			}
		}
		if got[0].ID == got[1].ID {
			t.Errorf("both claims returned %q", got[0].ID)
		}
		// Hand back anything claimed that this test didn't create, so other
		// tests' feeds aren't left leased.
		for _, f := range got {
			if f.ID != ids[0] && f.ID != ids[1] {
				if _, err := db.Exec(`UPDATE feeds SET next_fetch_at = now() WHERE id = $1`, f.ID); err != nil {
					t.Errorf("hand back feed: %v", err)
				}
			}
		}
	})

	// Each subtest below makes the feed that must be skipped due longer than
	// the one that must be claimed.

	t.Run("skips a due feed with no subscriber and no request", func(t *testing.T) {
		unfollowed, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		setNextFetch(t, db, unfollowed.ID, longAgo.Add(-time.Hour))
		setNextFetch(t, db, subscribed.ID, longAgo)

		claim(t, subscribed)
	})

	t.Run("claims a requested feed with no subscriber", func(t *testing.T) {
		requested := newTestFeed(t, db)
		setNextFetch(t, db, requested.ID, longAgo)
		setRefreshRequested(t, db, requested.ID)

		claim(t, requested)
	})

	t.Run("claims a requested feed before a subscribed feed due longer", func(t *testing.T) {
		subscribed, requested := newSubscribedFeed(t, db, userID), newTestFeed(t, db)
		setNextFetch(t, db, subscribed.ID, longAgo.Add(-time.Hour))
		setNextFetch(t, db, requested.ID, longAgo)
		setRefreshRequested(t, db, requested.ID)

		claim(t, requested)
		claim(t, subscribed)
	})

	t.Run("skips a gone feed with a request", func(t *testing.T) {
		gone, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		setNextFetch(t, db, gone.ID, longAgo.Add(-time.Hour))
		setNextFetch(t, db, subscribed.ID, longAgo)
		if err := repo.MarkGone(t.Context(), gone.ID); err != nil {
			t.Fatalf("MarkGone: %v", err)
		}
		// MarkGone clears requests, so set it afterwards.
		setRefreshRequested(t, db, gone.ID)

		claim(t, subscribed)
	})

	t.Run("claims a catalog feed with no subscriber", func(t *testing.T) {
		catalog, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		addToDiscover(t, db, catalog.ID)
		setNextFetch(t, db, catalog.ID, longAgo)
		setNextFetch(t, db, subscribed.ID, longAgo.Add(time.Hour))

		claim(t, catalog)
	})

	t.Run("skips the catalog feed's site's other feeds", func(t *testing.T) {
		catalog, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		addToDiscover(t, db, catalog.ID)
		sibling, err := upsertFeed(t.Context(), db, rss.Feed{
			SiteID: catalog.SiteID,
			Url:    fmt.Sprintf("https://example.com/sibling-%d.xml", psqltest.Unique()),
		})
		if err != nil {
			t.Fatalf("Upsert sibling: %v", err)
		}
		setNextFetch(t, db, sibling, longAgo.Add(-time.Hour))
		setNextFetch(t, db, catalog.ID, time.Now().Add(time.Hour)) // not due
		setNextFetch(t, db, subscribed.ID, longAgo)

		claim(t, subscribed)
	})

	t.Run("skips a catalog feed that isn't due yet", func(t *testing.T) {
		catalog, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		addToDiscover(t, db, catalog.ID)
		setNextFetch(t, db, catalog.ID, time.Now().Add(time.Hour))
		setNextFetch(t, db, subscribed.ID, longAgo)

		claim(t, subscribed)
	})

	t.Run("skips a requested feed that isn't due yet", func(t *testing.T) {
		backoff, subscribed := newTestFeed(t, db), newSubscribedFeed(t, db, userID)
		setNextFetch(t, db, backoff.ID, time.Now().Add(time.Hour))
		setRefreshRequested(t, db, backoff.ID)
		setNextFetch(t, db, subscribed.ID, longAgo)

		claim(t, subscribed)
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryRequestRefresh(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()
	past := time.Now().Add(-time.Hour)

	requestedAt := func(t *testing.T, id string) time.Time {
		t.Helper()
		f, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return f.RefreshRequestedAt
	}

	t.Run("requests a due feed", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setNextFetch(t, db, feed.ID, past)

		before := dbNow(t, db)
		if err := repo.RequestRefresh(ctx, feed.ID); err != nil {
			t.Fatalf("RequestRefresh: %v", err)
		}
		if got, after := requestedAt(t, feed.ID), dbNow(t, db); got.Before(before) || got.After(after) {
			t.Errorf("got RefreshRequestedAt %v; want within [%v, %v]", got, before, after)
		}
	})

	t.Run("ignores a feed that isn't due", func(t *testing.T) {
		feed := newTestFeed(t, db) // next_fetch_at an hour from now
		if err := repo.RequestRefresh(ctx, feed.ID); err != nil {
			t.Fatalf("RequestRefresh: %v", err)
		}
		if got := requestedAt(t, feed.ID); !got.IsZero() {
			t.Errorf("got RefreshRequestedAt %v; want zero", got)
		}
	})

	t.Run("ignores a gone feed", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setNextFetch(t, db, feed.ID, past)
		if err := repo.MarkGone(ctx, feed.ID); err != nil {
			t.Fatalf("MarkGone: %v", err)
		}
		if err := repo.RequestRefresh(ctx, feed.ID); err != nil {
			t.Fatalf("RequestRefresh: %v", err)
		}
		if got := requestedAt(t, feed.ID); !got.IsZero() {
			t.Errorf("got RefreshRequestedAt %v; want zero", got)
		}
	})

	t.Run("keeps an existing request", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setNextFetch(t, db, feed.ID, past)
		first := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
		if _, err := db.Exec(`UPDATE feeds SET refresh_requested_at = $2 WHERE id = $1`, feed.ID, first); err != nil {
			t.Fatalf("set refresh_requested_at: %v", err)
		}
		if err := repo.RequestRefresh(ctx, feed.ID); err != nil {
			t.Fatalf("RequestRefresh: %v", err)
		}
		if got := requestedAt(t, feed.ID); !got.Equal(first) {
			t.Errorf("got RefreshRequestedAt %v; want unchanged %v", got, first)
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		if err := repo.RequestRefresh(ctx, "00000000-0000-4000-8000-000000000000"); err != nil {
			t.Errorf("got %v; want nil", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryOutcomesClearRefreshRequest(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()
	next := time.Now().Add(time.Hour)

	tests := []struct {
		name   string
		record func(f rss.Feed) error
	}{
		{"SaveFetch", func(f rss.Feed) error { _, err := repo.SaveFetch(ctx, f, nil, f.NextFetch); return err }},
		{"RecordFailure", func(f rss.Feed) error { return repo.RecordFailure(ctx, f.ID, "boom", next) }},
		{"RecordNotModified", func(f rss.Feed) error { return repo.RecordNotModified(ctx, f.ID, next) }},
		{"MarkGone", func(f rss.Feed) error { return repo.MarkGone(ctx, f.ID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feed := newTestFeed(t, db)
			setRefreshRequested(t, db, feed.ID)

			if err := tt.record(feed); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			got, err := repo.GetByID(ctx, feed.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if !got.RefreshRequestedAt.IsZero() {
				t.Errorf("got RefreshRequestedAt %v; want zero", got.RefreshRequestedAt)
			}
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryRecordFailure(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	t.Run("records each failure", func(t *testing.T) {
		feed := newTestFeed(t, db)
		if _, err := repo.SaveFetch(ctx, feed, nil, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		fetched, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}

		for i, msg := range []string{"first failure", "second failure"} {
			next := time.Date(2030, 1, 2, 3, i, 0, 0, time.UTC)
			before := dbNow(t, db)
			if err := repo.RecordFailure(ctx, feed.ID, msg, next); err != nil {
				t.Fatalf("RecordFailure %d: %v", i, err)
			}
			got, err := repo.GetByID(ctx, feed.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ConsecutiveFailures != i+1 || got.LastError != msg {
				t.Errorf("got ConsecutiveFailures %d, LastError %q; want %d, %q", got.ConsecutiveFailures, got.LastError, i+1, msg)
			}
			if !got.NextFetch.Equal(next) {
				t.Errorf("got NextFetch %v; want %v", got.NextFetch, next)
			}
			if got.LastAttempt.Before(before) {
				t.Errorf("got LastAttempt %v; want at or after %v", got.LastAttempt, before)
			}
			if !got.LastFetched.Equal(fetched.LastFetched) {
				t.Errorf("got LastFetched %v; want unchanged %v", got.LastFetched, fetched.LastFetched)
			}
		}
	})

	t.Run("truncates a long message", func(t *testing.T) {
		feed := newTestFeed(t, db)
		if err := repo.RecordFailure(ctx, feed.ID, strings.Repeat("é", maxErrorLen), time.Now()); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
		got, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if len(got.LastError) != maxErrorLen {
			t.Errorf("len(LastError) = %d; want %d", len(got.LastError), maxErrorLen)
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		err := repo.RecordFailure(ctx, "00000000-0000-4000-8000-000000000000", "boom", time.Now())
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("got %v; want rss.ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryRecordNotModified(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	t.Run("records a successful attempt and clears failures", func(t *testing.T) {
		feed := newTestFeed(t, db)
		feed.ETag = `"v1"`
		feed.Title = "Saved title"
		if _, err := repo.SaveFetch(ctx, feed, nil, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if err := repo.RecordFailure(ctx, feed.ID, "boom", time.Now()); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}

		next := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		before := dbNow(t, db)
		if err := repo.RecordNotModified(ctx, feed.ID, next); err != nil {
			t.Fatalf("RecordNotModified: %v", err)
		}

		got, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !got.NextFetch.Equal(next) {
			t.Errorf("got NextFetch %v; want %v", got.NextFetch, next)
		}
		if got.LastAttempt.Before(before) || !got.LastAttempt.Equal(got.LastFetched) {
			t.Errorf("got LastAttempt %v, LastFetched %v; want both the same, not before %v", got.LastAttempt, got.LastFetched, before)
		}
		if got.LastError != "" || got.ConsecutiveFailures != 0 {
			t.Errorf("got LastError %q, ConsecutiveFailures %d; want cleared", got.LastError, got.ConsecutiveFailures)
		}
		if got.ETag != feed.ETag || got.Title != feed.Title {
			t.Errorf("got ETag %q, Title %q; want unchanged %q, %q", got.ETag, got.Title, feed.ETag, feed.Title)
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		err := repo.RecordNotModified(ctx, "00000000-0000-4000-8000-000000000000", time.Now())
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("got %v; want rss.ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchMarksCopies(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()
	feedA, feedB, feedC := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)

	nano := psqltest.Unique()
	post := fmt.Sprintf("https://example.com/copies-%d", nano)
	other := fmt.Sprintf("https://example.com/copies-other-%d", nano)
	moved := fmt.Sprintf("https://example.com/copies-moved-%d", nano)

	save := func(t *testing.T, feed rss.Feed, articles ...rss.Article) {
		t.Helper()
		if _, err := repo.SaveFetch(ctx, feed, articles, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
	}
	// hasCopies returns has_copies of the articles with externalIDs in feed.
	hasCopies := func(t *testing.T, feed rss.Feed, externalIDs ...string) []bool {
		t.Helper()
		var got []bool
		for _, ext := range externalIDs {
			var flagged bool
			if err := db.QueryRow(`SELECT has_copies FROM articles WHERE feed_id = $1 AND external_id = $2`, feed.ID, ext).Scan(&flagged); err != nil {
				t.Fatalf("select has_copies of %s: %v", ext, err)
			}
			got = append(got, flagged)
		}
		return got
	}
	check := func(t *testing.T, got []bool, want bool) {
		t.Helper()
		for i, g := range got {
			if g != want {
				t.Errorf("has_copies[%d] = %v, want %v", i, g, want)
			}
		}
	}
	article := func(ext, url string) rss.Article {
		return rss.Article{ExternalID: ext, URL: url, CanonicalURL: url, Title: ext}
	}

	t.Run("a canonical URL no other article has", func(t *testing.T) {
		save(t, feedA, article("p1", post))
		check(t, hasCopies(t, feedA, "p1"), false)
	})

	t.Run("a copy in another feed flags both", func(t *testing.T) {
		save(t, feedB, article("p2", post))
		check(t, append(hasCopies(t, feedA, "p1"), hasCopies(t, feedB, "p2")...), true)
	})

	t.Run("a third copy flags all three", func(t *testing.T) {
		save(t, feedC, article("p3", post))
		got := append(hasCopies(t, feedA, "p1"), hasCopies(t, feedB, "p2")...)
		check(t, append(got, hasCopies(t, feedC, "p3")...), true)
	})

	t.Run("two copies in the same feed", func(t *testing.T) {
		save(t, feedA, article("o1", other), article("o2", other))
		check(t, hasCopies(t, feedA, "o1", "o2"), true)
	})

	t.Run("articles with no canonical URL are never flagged", func(t *testing.T) {
		save(t, feedB, rss.Article{ExternalID: "e1", Title: "e1"}, rss.Article{ExternalID: "e2", Title: "e2"})
		save(t, feedC, rss.Article{ExternalID: "e3", Title: "e3"})
		check(t, append(hasCopies(t, feedB, "e1", "e2"), hasCopies(t, feedC, "e3")...), false)
	})

	t.Run("an update to another article's canonical URL flags both", func(t *testing.T) {
		save(t, feedB, article("m1", moved))
		save(t, feedC, article("m2", fmt.Sprintf("https://example.com/copies-before-%d", nano)))
		check(t, hasCopies(t, feedC, "m2"), false)
		save(t, feedC, article("m2", moved))
		check(t, append(hasCopies(t, feedB, "m1"), hasCopies(t, feedC, "m2")...), true)
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchCatchUp(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	articles := func(ids ...string) []rss.Article {
		var as []rss.Article
		for _, id := range ids {
			as = append(as, rss.Article{ExternalID: id, Title: id})
		}
		return as
	}
	tests := []struct {
		name       string
		first      []rss.Article // saved first, unless nil
		save       []rss.Article
		wantBehind bool
	}{
		{"later save, every article new", articles("a"), articles("b", "c", "d"), true},
		{"later save, one already known", articles("a"), articles("a", "b", "c"), false},
		{"first save", nil, articles("a", "b", "c"), false},
		{"a single new article", articles("a"), articles("b"), false},
	}
	// Followed, so the schedule isn't held to a day (see
	// TestFeedRepositoryUnfollowedSchedule).
	userID := newTestUser(t, db)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feed := newSubscribedFeed(t, db, userID)
			if tt.first != nil {
				if _, err := repo.SaveFetch(ctx, feed, tt.first, feed.NextFetch); err != nil {
					t.Fatalf("first SaveFetch: %v", err)
				}
			}

			now := time.Now().Truncate(time.Second)
			feed.NextFetch = now.Add(time.Hour)
			catchUp := now.Add(15 * time.Minute)
			res, err := repo.SaveFetch(ctx, feed, tt.save, catchUp)
			if err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
			if res.Behind != tt.wantBehind {
				t.Errorf("Behind = %v, want %v (result %+v)", res.Behind, tt.wantBehind, res)
			}

			want := feed.NextFetch
			if tt.wantBehind {
				want = catchUp
			}
			got, err := repo.GetByID(ctx, feed.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if !got.NextFetch.Equal(want) {
				t.Errorf("NextFetch = %v, want %v", got.NextFetch, want)
			}
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryCategories(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	stored := func(id string) []string {
		t.Helper()
		var got []string
		if err := db.QueryRow(`SELECT categories FROM feeds WHERE id = $1`, id).Scan(pq.Array(&got)); err != nil {
			t.Fatalf("select categories: %v", err)
		}
		return got
	}
	check := func(step, id string, want []string) {
		t.Helper()
		if got := stored(id); !slices.Equal(got, want) {
			t.Errorf("%s: categories = %q, want %q", step, got, want)
		}
	}

	// Upsert, the discovery save, stores them and replaces them, also
	// with none.
	f := newTestFeed(t, db)
	check("new feed without categories", f.ID, []string{})
	f.Categories = []string{"Tech", "News"}
	if _, err := upsertFeed(ctx, db, f); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	check("Upsert", f.ID, []string{"Tech", "News"})
	f.Categories = nil
	if _, err := upsertFeed(ctx, db, f); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	check("Upsert with none", f.ID, []string{})

	// SaveFetch, like the feed's title, replaces them on every fetch,
	// also with none.
	f.Categories = []string{"Go", "Postgres"}
	if _, err := repo.SaveFetch(ctx, f, nil, f.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	check("SaveFetch", f.ID, []string{"Go", "Postgres"})
	f.Categories = nil
	if _, err := repo.SaveFetch(ctx, f, nil, f.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	check("SaveFetch with none", f.ID, []string{})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryUnfollowedSchedule(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()
	userID := newTestUser(t, db)
	later := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	// Each records an outcome scheduling feed's next fetch at next.
	record := map[string]func(t *testing.T, feed rss.Feed, next time.Time){
		"SaveFetch": func(t *testing.T, feed rss.Feed, next time.Time) {
			feed.NextFetch = next
			if _, err := repo.SaveFetch(ctx, feed, nil, next); err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
		},
		"SaveFetch catching up": func(t *testing.T, feed rss.Feed, next time.Time) {
			// A first article, then a later save whose items are all new.
			feed.NextFetch = next
			if _, err := repo.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "a", Title: "A"}}, next); err != nil {
				t.Fatalf("first SaveFetch: %v", err)
			}
			res, err := repo.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "b", Title: "B"}, {ExternalID: "c", Title: "C"}}, next)
			if err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
			if !res.Behind {
				t.Fatalf("got %+v; want Behind", res)
			}
		},
		"RecordNotModified": func(t *testing.T, feed rss.Feed, next time.Time) {
			if err := repo.RecordNotModified(ctx, feed.ID, next); err != nil {
				t.Fatalf("RecordNotModified: %v", err)
			}
		},
		"RecordFailure": func(t *testing.T, feed rss.Feed, next time.Time) {
			if err := repo.RecordFailure(ctx, feed.ID, "boom", next); err != nil {
				t.Fatalf("RecordFailure: %v", err)
			}
		},
	}
	nextFetch := func(t *testing.T, id string) time.Time {
		t.Helper()
		f, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return f.NextFetch
	}

	for name, record := range record {
		t.Run(name, func(t *testing.T) {
			t.Run("unfollowed waits a day", func(t *testing.T) {
				feed := newTestFeed(t, db)
				addToDiscover(t, db, feed.ID)
				before := dbNow(t, db)
				record(t, feed, time.Now().Add(15*time.Minute))
				if got, after := nextFetch(t, feed.ID), dbNow(t, db); got.Before(before.Add(rss.UnfollowedRefreshInterval)) || got.After(after.Add(rss.UnfollowedRefreshInterval)) {
					t.Errorf("NextFetch = %v; want now + %v, within [%v, %v]", got, rss.UnfollowedRefreshInterval, before, after)
				}
			})

			t.Run("unfollowed keeps a later time", func(t *testing.T) {
				feed := newTestFeed(t, db)
				record(t, feed, later)
				if got := nextFetch(t, feed.ID); !got.Equal(later) {
					t.Errorf("NextFetch = %v; want %v", got, later)
				}
			})

			t.Run("followed is unchanged", func(t *testing.T) {
				feed := newSubscribedFeed(t, db, userID)
				addToDiscover(t, db, feed.ID)
				next := time.Now().Add(15 * time.Minute).Truncate(time.Second)
				record(t, feed, next)
				if got := nextFetch(t, feed.ID); !got.Equal(next) {
					t.Errorf("NextFetch = %v; want %v", got, next)
				}
			})
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchPostsPerWeek(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()
	feed := newTestFeed(t, db)

	// Six articles in the last 30 days (one undated, counted by created_at)
	// and two older ones.
	now := time.Now()
	var articles []rss.Article
	for i := range 5 {
		articles = append(articles, rss.Article{ExternalID: fmt.Sprintf("recent-%d", i), Title: "Recent", PublishedAt: now.Add(-time.Duration(i+1) * 24 * time.Hour)})
	}
	articles = append(articles,
		rss.Article{ExternalID: "undated", Title: "Undated"},
		rss.Article{ExternalID: "old-1", Title: "Old", PublishedAt: now.Add(-40 * 24 * time.Hour)},
		rss.Article{ExternalID: "old-2", Title: "Old", PublishedAt: now.Add(-90 * 24 * time.Hour)},
	)
	if _, err := repo.SaveFetch(ctx, feed, articles, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}

	checkRate := func(t *testing.T, want float64) {
		t.Helper()
		got, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if math.Abs(got.PostsPerWeek-want) > 1e-4 {
			t.Errorf("got PostsPerWeek %v; want %v", got.PostsPerWeek, want)
		}
	}
	checkRate(t, 6*7.0/30)

	t.Run("RecordNotModified recomputes it", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE articles SET published_at = now() - interval '31 days' WHERE feed_id = $1`, feed.ID); err != nil {
			t.Fatalf("backdate articles: %v", err)
		}
		if err := repo.RecordNotModified(ctx, feed.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("RecordNotModified: %v", err)
		}
		checkRate(t, 0)
	})
}

// A feed with less than 30 days of articles is measured over the time they
// cover, at least 7 days.
// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchPostsPerWeekShortHistory(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	checkRate := func(t *testing.T, feedID string, want float64) {
		t.Helper()
		got, err := repo.GetByID(ctx, feedID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if math.Abs(got.PostsPerWeek-want) > 1e-4 {
			t.Errorf("got PostsPerWeek %v; want %v", got.PostsPerWeek, want)
		}
	}
	// articlesAgo returns an article published ago before now for each of ago.
	articlesAgo := func(ago ...time.Duration) []rss.Article {
		now := time.Now()
		var articles []rss.Article
		for i, d := range ago {
			articles = append(articles, rss.Article{ExternalID: fmt.Sprintf("a-%d", i), Title: "Article", PublishedAt: now.Add(-d)})
		}
		return articles
	}

	tests := []struct {
		name     string
		articles []rss.Article
		want     float64
	}{
		{"five today", articlesAgo(time.Hour, 3*time.Hour, 6*time.Hour, 9*time.Hour, 12*time.Hour), 5},
		{"two weeks of history", articlesAgo(24*time.Hour, 5*24*time.Hour, 10*24*time.Hour, 14*24*time.Hour), 4 * 7.0 / 14},
		{"one recent post", articlesAgo(time.Hour), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feed := newTestFeed(t, db)
			if _, err := repo.SaveFetch(ctx, feed, tt.articles, feed.NextFetch); err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
			checkRate(t, feed.ID, tt.want)
		})
	}

	t.Run("no articles", func(t *testing.T) {
		feed := newTestFeed(t, db)
		if err := repo.RecordNotModified(ctx, feed.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("RecordNotModified: %v", err)
		}
		checkRate(t, feed.ID, 0)
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryMarkGone(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	t.Run("records the gone feed", func(t *testing.T) {
		feed := newTestFeed(t, db)
		stored, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !stored.GoneAt.IsZero() {
			t.Fatalf("new feed has GoneAt %v; want zero", stored.GoneAt)
		}

		before := dbNow(t, db)
		if err := repo.MarkGone(ctx, feed.ID); err != nil {
			t.Fatalf("MarkGone: %v", err)
		}
		got, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.GoneAt.Before(before) || !got.GoneAt.Equal(got.LastAttempt) {
			t.Errorf("got GoneAt %v, LastAttempt %v; want both the same, not before %v", got.GoneAt, got.LastAttempt, before)
		}
		if got.LastError != "status 410" {
			t.Errorf("got LastError %q; want %q", got.LastError, "status 410")
		}
		if !got.NextFetch.Equal(stored.NextFetch) || got.ConsecutiveFailures != stored.ConsecutiveFailures {
			t.Errorf("got NextFetch %v, ConsecutiveFailures %d; want unchanged %v, %d", got.NextFetch, got.ConsecutiveFailures, stored.NextFetch, stored.ConsecutiveFailures)
		}
	})

	t.Run("Upsert clears it", func(t *testing.T) {
		feed := newTestFeed(t, db)
		if err := repo.MarkGone(ctx, feed.ID); err != nil {
			t.Fatalf("MarkGone: %v", err)
		}
		if _, err := upsertFeed(ctx, db, feed); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		got, err := repo.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !got.GoneAt.IsZero() {
			t.Errorf("got GoneAt %v; want zero", got.GoneAt)
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		err := repo.MarkGone(ctx, "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("got %v; want rss.ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryOmittedOptionalColumns(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)

	url := fmt.Sprintf("https://example.com/feed-%d.xml", psqltest.Unique())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
			t.Errorf("cleanup feed: %v", err)
		}
	})

	siteID := newTestSite(t, db)

	var id string
	if err := db.QueryRow(`INSERT INTO feeds (url, title, site_id) VALUES ($1, 't', $2) RETURNING id`, url, siteID).Scan(&id); err != nil {
		t.Fatalf("insert feed: %v", err)
	}

	feed, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if feed.SiteUrl != "" || feed.Description != "" {
		t.Errorf("got site_url %q, description %q; want empty strings", feed.SiteUrl, feed.Description)
	}
	if feed.SiteID != siteID {
		t.Errorf("got site_id %q; want %q", feed.SiteID, siteID)
	}
	if !feed.LastFetched.IsZero() {
		t.Errorf("got LastFetched %v for a never-fetched feed; want zero", feed.LastFetched)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryGetByURL(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	f := newTestFeed(t, db)

	got, err := repo.GetByURL(t.Context(), f.Url)
	if err != nil {
		t.Fatalf("GetByURL: %v", err)
	}
	if got.ID != f.ID || got.Url != f.Url || got.SiteID != f.SiteID || got.Title != f.Title {
		t.Errorf("GetByURL = %+v; want %+v", got, f)
	}

	if _, err := repo.GetByURL(t.Context(), f.Url+"?missing"); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByURL missing = %v; want ErrNoRecord", err)
	}
}

// newTestFeedOn is newTestFeed on the site siteID, which deletes it on
// cleanup.
func newTestFeedOn(t *testing.T, db *sql.DB, siteID string) rss.Feed {
	t.Helper()
	f := rss.Feed{
		SiteID:    siteID,
		Url:       fmt.Sprintf("https://example.com/site-feed-%d.xml", psqltest.Unique()),
		Title:     "Feed",
		NextFetch: time.Now().Add(time.Hour),
	}
	id, err := upsertFeed(t.Context(), db, f)
	if err != nil {
		t.Fatalf("Upsert feed: %v", err)
	}
	f.ID = id
	return f
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositorySiteFeedFacts(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	sec := newTestSection(t, db, "Site facts", 1)
	siteID := newTestSite(t, db)
	date := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	picked := newTestFeedOn(t, db, siteID)
	putInSection(t, db, picked.ID, sec)
	saveDated(t, db, picked, date, date.Add(time.Hour), date.Add(-time.Hour))
	other := newTestFeedOn(t, db, siteID)
	saveDated(t, db, other, date.Add(-48*time.Hour))
	empty := newTestFeedOn(t, db, siteID)
	elsewhere := newTestFeed(t, db) // on another site

	facts, err := repo.SiteFeedFacts(t.Context(), siteID)
	if err != nil {
		t.Fatalf("SiteFeedFacts: %v", err)
	}

	if len(facts) != 3 {
		t.Errorf("got facts for %d feeds, want the site's 3: %+v", len(facts), facts)
	}
	if _, ok := facts[elsewhere.ID]; ok {
		t.Errorf("got facts for another site's feed")
	}
	if got := facts[picked.ID]; got.Section != sec || !got.LatestAt.Equal(date.Add(time.Hour)) {
		t.Errorf("catalog feed's facts = %+v, want section %+v and its newest article's time %v", got, sec, date.Add(time.Hour))
	}
	if got := facts[other.ID]; got.Section != (rss.Section{}) || !got.LatestAt.Equal(date.Add(-48*time.Hour)) {
		t.Errorf("other feed's facts = %+v, want no section and %v", got, date.Add(-48*time.Hour))
	}
	if got, ok := facts[empty.ID]; !ok || got != (rss.FeedFacts{}) {
		t.Errorf("empty feed's facts = %+v (present %v), want zero", got, ok)
	}

	t.Run("site without feeds", func(t *testing.T) {
		facts, err := repo.SiteFeedFacts(t.Context(), newTestSite(t, db))
		if err != nil || len(facts) != 0 {
			t.Errorf("SiteFeedFacts = %v, %v; want none", facts, err)
		}
	})
}
