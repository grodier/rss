package psql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)

	host := fmt.Sprintf("site-%d.example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM sites WHERE host = $1`, host); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
	})

	want := rss.Site{
		Host:        host,
		URL:         "https://www." + host + "/",
		Title:       "Example",
		Description: "An example site",
	}
	id, err := repo.Upsert(t.Context(), want)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if id == "" {
		t.Fatal("Upsert returned an empty id")
	}

	check := func(t *testing.T, got rss.Site, want rss.Site) {
		t.Helper()
		if got.ID != id || got.Host != want.Host || got.URL != want.URL || got.Title != want.Title || got.Description != want.Description {
			t.Errorf("got %+v; want id %q and fields of %+v", got, id, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Errorf("got created_at %v, updated_at %v; want non-zero", got.CreatedAt, got.UpdatedAt)
		}
		if got.UpdatedAt.Before(got.CreatedAt) {
			t.Errorf("updated_at %v is before created_at %v", got.UpdatedAt, got.CreatedAt)
		}
	}

	t.Run("GetByID and GetByHost return the inserted site", func(t *testing.T) {
		got, err := repo.GetByID(t.Context(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		check(t, got, want)

		got, err = repo.GetByHost(t.Context(), host)
		if err != nil {
			t.Fatalf("GetByHost: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Upsert same host updates fields", func(t *testing.T) {
		want.URL = "https://" + host + "/"
		want.Title = "New title"
		gotID, err := repo.Upsert(t.Context(), want)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if gotID != id {
			t.Errorf("id = %q; want %q", gotID, id)
		}

		got, err := repo.GetByHost(t.Context(), host)
		if err != nil {
			t.Fatalf("GetByHost: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Upsert with empty title and description keeps previous values", func(t *testing.T) {
		gotID, err := repo.Upsert(t.Context(), rss.Site{Host: host, URL: want.URL})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if gotID != id {
			t.Errorf("id = %q; want %q", gotID, id)
		}

		got, err := repo.GetByID(t.Context(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Feed with site scans SiteID", func(t *testing.T) {
		url := fmt.Sprintf("https://%s/feed-%d.xml", host, time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
				t.Errorf("cleanup feed: %v", err)
			}
		})

		var feedID string
		if err := db.QueryRow(`INSERT INTO feeds (url, title, site_id) VALUES ($1, 't', $2) RETURNING id`, url, id).Scan(&feedID); err != nil {
			t.Fatalf("insert feed: %v", err)
		}

		feed, err := NewFeedRepository(db).GetByID(t.Context(), feedID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if feed.SiteID != id {
			t.Errorf("SiteID = %q; want %q", feed.SiteID, id)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepositoryMissing(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)

	if _, err := repo.GetByID(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByID err = %v; want rss.ErrNoRecord", err)
	}

	host := fmt.Sprintf("missing-%d.example.com", time.Now().UnixNano())
	if _, err := repo.GetByHost(t.Context(), host); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByHost err = %v; want rss.ErrNoRecord", err)
	}
}
