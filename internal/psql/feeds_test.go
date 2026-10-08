package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestSite inserts a site and deletes it (and its feeds) on cleanup.
func newTestSite(t *testing.T, db *sql.DB) string {
	t.Helper()
	host := fmt.Sprintf("feeds-test-%d.example.com", time.Now().UnixNano())
	id, err := NewSiteRepository(db).Upsert(t.Context(), rss.Site{Host: host, URL: "https://" + host + "/"})
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

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryClaimDue(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	const lease = 10 * time.Minute
	// Other tests share the database, so make this test's feeds due longest.
	longAgo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("claims the feed due longest and leases it", func(t *testing.T) {
		first, second := newTestFeed(t, db), newTestFeed(t, db)
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
		gone, live := newTestFeed(t, db), newTestFeed(t, db)
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
			f := newTestFeed(t, db)
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
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryRecordFailure(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	ctx := t.Context()

	t.Run("records each failure", func(t *testing.T) {
		feed := newTestFeed(t, db)
		if _, err := repo.SaveFetch(ctx, feed, nil); err != nil {
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
		if _, err := repo.SaveFetch(ctx, feed, nil); err != nil {
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
		if _, err := repo.Upsert(ctx, feed); err != nil {
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

	url := fmt.Sprintf("https://example.com/feed-%d.xml", time.Now().UnixNano())
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
func TestFeedsRejectNullOptionalColumns(t *testing.T) {
	db := psqltest.NewDB(t)
	siteID := newTestSite(t, db)

	for _, col := range []string{"site_url", "description"} {
		url := fmt.Sprintf("https://example.com/feed-%s-%d.xml", col, time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
				t.Errorf("cleanup feed: %v", err)
			}
		})

		_, err := db.Exec(fmt.Sprintf(`INSERT INTO feeds (url, title, site_id, %s) VALUES ($1, 't', $2, NULL)`, col), url, siteID)
		if err == nil {
			t.Errorf("inserting NULL %s succeeded; want a not-null violation", col)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedsRequireSite(t *testing.T) {
	db := psqltest.NewDB(t)

	url := fmt.Sprintf("https://example.com/feed-nosite-%d.xml", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
			t.Errorf("cleanup feed: %v", err)
		}
	})

	if _, err := db.Exec(`INSERT INTO feeds (url, title) VALUES ($1, 't')`, url); err == nil {
		t.Error("inserting a feed without site_id succeeded; want a not-null violation")
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestDeletingSiteDeletesItsFeeds(t *testing.T) {
	db := psqltest.NewDB(t)
	siteID := newTestSite(t, db)

	url := fmt.Sprintf("https://example.com/feed-cascade-%d.xml", time.Now().UnixNano())
	var feedID string
	if err := db.QueryRow(`INSERT INTO feeds (url, title, site_id) VALUES ($1, 't', $2) RETURNING id`, url, siteID).Scan(&feedID); err != nil {
		t.Fatalf("insert feed: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM sites WHERE id = $1`, siteID); err != nil {
		t.Fatalf("delete site: %v", err)
	}

	if _, err := NewFeedRepository(db).GetByID(t.Context(), feedID); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByID after site delete: got %v, want rss.ErrNoRecord", err)
	}
}
