package psql

import (
	"errors"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB.
func TestDebugRepositorySiteDebug(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDebugRepository(db)

	t.Run("with an icon", func(t *testing.T) {
		siteID := newTestSite(t, db)
		checked := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		if _, err := db.Exec(`UPDATE sites SET icon_checked_at = $2, icon_next_check_at = $3 WHERE id = $1`,
			siteID, checked, checked.Add(24*time.Hour)); err != nil {
			t.Fatalf("update site: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO site_icons (site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at)
			VALUES ($1, 'https://example.com/favicon.png', 'image/png', 32, 16, $2, 'abc', '"e"', 'Mon', $3)`,
			siteID, []byte("12345"), checked); err != nil {
			t.Fatalf("insert icon: %v", err)
		}

		got, err := repo.SiteDebug(t.Context(), siteID)
		if err != nil {
			t.Fatalf("SiteDebug: %v", err)
		}
		if !got.IconCheckedAt.Equal(checked) || !got.IconNextCheckAt.Equal(checked.Add(24*time.Hour)) {
			t.Errorf("got checked %v, next %v; want %v, %v", got.IconCheckedAt, got.IconNextCheckAt, checked, checked.Add(24*time.Hour))
		}
		i := got.Icon
		if i.SiteID != siteID || i.SourceURL != "https://example.com/favicon.png" || i.ContentType != "image/png" ||
			i.Width != 32 || i.Height != 16 || i.Hash != "abc" || i.ETag != `"e"` || i.LastModified != "Mon" || !i.FetchedAt.Equal(checked) {
			t.Errorf("got icon %+v", i)
		}
		if i.Data != nil {
			t.Errorf("got icon data %q; want none", i.Data)
		}
		if got.IconBytes != 5 {
			t.Errorf("got IconBytes %d; want 5", got.IconBytes)
		}
	})

	t.Run("without an icon", func(t *testing.T) {
		siteID := newTestSite(t, db)
		got, err := repo.SiteDebug(t.Context(), siteID)
		if err != nil {
			t.Fatalf("SiteDebug: %v", err)
		}
		if got.Icon.SiteID != "" || got.IconBytes != 0 {
			t.Errorf("got icon %+v, %d bytes; want none", got.Icon, got.IconBytes)
		}
		if !got.IconCheckedAt.IsZero() {
			t.Errorf("got IconCheckedAt %v; want zero", got.IconCheckedAt)
		}
		if got.IconNextCheckAt.IsZero() {
			t.Error("got zero IconNextCheckAt; want the column's default")
		}
	})

	t.Run("unknown site", func(t *testing.T) {
		_, err := repo.SiteDebug(t.Context(), "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("got %v; want rss.ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestDebugRepositoryFeedDebug(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDebugRepository(db)

	feed := newTestFeed(t, db)
	empty := newTestFeed(t, db)

	older := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	var articleIDs []string
	for i, at := range []time.Time{older, newer} {
		var id string
		err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, timeline_at, created_at) VALUES ($1, $2, $3, $3) RETURNING id`,
			feed.ID, string(rune('a'+i)), at).Scan(&id)
		if err != nil {
			t.Fatalf("insert article: %v", err)
		}
		articleIDs = append(articleIDs, id)
	}
	if _, err := db.Exec(`INSERT INTO enrich_jobs (article_id) VALUES ($1) ON CONFLICT DO NOTHING`, articleIDs[0]); err != nil {
		t.Fatalf("insert enrich job: %v", err)
	}

	missing := "00000000-0000-4000-8000-000000000000"
	got, err := repo.FeedDebug(t.Context(), []string{feed.ID, empty.ID, missing})
	if err != nil {
		t.Fatalf("FeedDebug: %v", err)
	}

	d, ok := got[feed.ID]
	if !ok {
		t.Fatalf("no entry for the feed in %v", got)
	}
	if d.Articles != 2 || d.EnrichJobs != 1 || !d.NewestCreatedAt.Equal(newer) || !d.NewestTimelineAt.Equal(newer) {
		t.Errorf("got %+v; want 2 articles, 1 enrich job, newest %v", d, newer)
	}

	if e, ok := got[empty.ID]; !ok || e != (rss.FeedDebug{}) {
		t.Errorf("got %+v (present %v) for a feed with no articles; want zero values", e, ok)
	}

	if _, ok := got[missing]; ok {
		t.Errorf("got an entry for a feed that doesn't exist")
	}

	t.Run("empty slice", func(t *testing.T) {
		got, err := repo.FeedDebug(t.Context(), nil)
		if err != nil {
			t.Fatalf("FeedDebug: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("got %v; want an empty map", got)
		}
	})
}
