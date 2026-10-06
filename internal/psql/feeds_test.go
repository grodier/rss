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

	if _, err := repo.GetLatest(t.Context()); err != nil {
		t.Fatalf("GetLatest: %v", err)
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
