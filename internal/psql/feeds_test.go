package psql

import (
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
)

// Requires a migrated database; see psqltest.NewDB.
func TestFeedRepositoryCreateDuplicate(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)

	url := fmt.Sprintf("https://example.com/feed-%d.xml", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
			t.Errorf("cleanup feed: %v", err)
		}
	})

	if _, err := repo.Create(t.Context(), rss.Feed{Url: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.Create(t.Context(), rss.Feed{Url: url})
	if !errors.Is(err, rss.ErrDuplicateFeed) {
		t.Fatalf("got %v, want rss.ErrDuplicateFeed", err)
	}
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

	var id string
	if err := db.QueryRow(`INSERT INTO feeds (url, title) VALUES ($1, 't') RETURNING id`, url).Scan(&id); err != nil {
		t.Fatalf("insert feed: %v", err)
	}

	feed, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if feed.SiteUrl != "" || feed.Description != "" {
		t.Errorf("got site_url %q, description %q; want empty strings", feed.SiteUrl, feed.Description)
	}

	if _, err := repo.GetLatest(t.Context()); err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestFeedsRejectNullOptionalColumns(t *testing.T) {
	db := psqltest.NewDB(t)

	for _, col := range []string{"site_url", "description"} {
		url := fmt.Sprintf("https://example.com/feed-%s-%d.xml", col, time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
				t.Errorf("cleanup feed: %v", err)
			}
		})

		_, err := db.Exec(fmt.Sprintf(`INSERT INTO feeds (url, title, %s) VALUES ($1, 't', NULL)`, col), url)
		if err == nil {
			t.Errorf("inserting NULL %s succeeded; want a not-null violation", col)
		}
	}
}
