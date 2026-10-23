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

// Requires a migrated database; see psqltest.NewDB.
func TestDebugRepositoryArticlesByExternalID(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDebugRepository(db)
	feeds := NewFeedRepository(db)

	feed := newTestFeed(t, db)
	other := newTestFeed(t, db)
	if _, err := feeds.SaveFetch(t.Context(), feed, []rss.Article{
		{ExternalID: "a", URL: "https://example.com/a", Title: "A", Summary: "<p>sa</p>"},
		{ExternalID: "b", Title: "B"},
	}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if _, err := feeds.SaveFetch(t.Context(), other, []rss.Article{{ExternalID: "c", Title: "Other C"}, {ExternalID: "a", Title: "Other A"}}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}

	got, err := repo.ArticlesByExternalID(t.Context(), feed.ID, []string{"a", "c", "missing"})
	if err != nil {
		t.Fatalf("ArticlesByExternalID: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v; want only a", got)
	}
	a := got["a"]
	if a.FeedID != feed.ID || a.Title != "A" || a.URL != "https://example.com/a" || a.Summary != "<p>sa</p>" || a.ID == "" {
		t.Errorf("got %+v; want the feed's article a with its columns", a)
	}

	t.Run("empty slice", func(t *testing.T) {
		got, err := repo.ArticlesByExternalID(t.Context(), feed.ID, nil)
		if err != nil {
			t.Fatalf("ArticlesByExternalID: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("got %v; want an empty map", got)
		}
	})
}

// insertDebugArticle inserts an article in feedID with canonicalURL, created
// at createdAt, and returns its ID. It's deleted along with the feed's site.
func insertDebugArticle(t *testing.T, db *sql.DB, feedID, externalID, canonicalURL string, createdAt time.Time) string {
	t.Helper()
	var id string
	err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, canonical_url, timeline_at, created_at)
		VALUES ($1, $2, $3, $4, $4) RETURNING id`, feedID, externalID, canonicalURL, createdAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert article: %v", err)
	}
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestDebugRepositoryArticleDebug(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDebugRepository(db)
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	t.Run("enrich job", func(t *testing.T) {
		feed := newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", "", at)
		if _, err := db.Exec(`INSERT INTO enrich_jobs (article_id, priority, attempts, next_attempt_at, last_error, created_at)
			VALUES ($1, 1, 2, $2, 'timeout', $3)`, id, at.Add(time.Hour), at); err != nil {
			t.Fatalf("insert enrich job: %v", err)
		}
		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		want := rss.EnrichJobState{Exists: true, Priority: 1, Attempts: 2, NextAttemptAt: at.Add(time.Hour), LastError: "timeout", CreatedAt: at}
		j := got.EnrichJob
		if j.Exists != want.Exists || j.Priority != want.Priority || j.Attempts != want.Attempts || !j.NextAttemptAt.Equal(want.NextAttemptAt) ||
			j.LastError != want.LastError || !j.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("got EnrichJob %+v; want %+v", j, want)
		}
	})

	t.Run("no enrich job, no reads or saves", func(t *testing.T) {
		feed := newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", "", at)
		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if got.EnrichJob != (rss.EnrichJobState{}) {
			t.Errorf("got EnrichJob %+v; want zero", got.EnrichJob)
		}
		if !got.ReadAt.IsZero() || !got.SavedAt.IsZero() {
			t.Errorf("got ReadAt %v, SavedAt %v; want zero", got.ReadAt, got.SavedAt)
		}
		if got.Copies != nil {
			t.Errorf("got copies %+v; want none", got.Copies)
		}
	})

	t.Run("copies in other feeds", func(t *testing.T) {
		canonical := fmt.Sprintf("https://example.com/copies-%d", time.Now().UnixNano())
		feed, second, third := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", canonical, at.Add(time.Minute))
		later := insertDebugArticle(t, db, third.ID, "a", canonical, at.Add(2*time.Minute))
		earlier := insertDebugArticle(t, db, second.ID, "a", canonical, at)
		insertDebugArticle(t, db, second.ID, "b", canonical+"/other", at)

		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if len(got.Copies) != 2 {
			t.Fatalf("got %d copies (%+v); want 2", len(got.Copies), got.Copies)
		}
		c := got.Copies[0]
		if c.ArticleID != earlier || c.FeedID != second.ID || c.FeedTitle != second.Title || !c.CreatedAt.Equal(at) || !c.TimelineAt.Equal(at) {
			t.Errorf("got first copy %+v; want the earlier one in the second feed", c)
		}
		if got.Copies[1].ArticleID != later {
			t.Errorf("got second copy %+v; want %s", got.Copies[1], later)
		}
	})

	t.Run("feed title falls back to its URL", func(t *testing.T) {
		canonical := fmt.Sprintf("https://example.com/untitled-%d", time.Now().UnixNano())
		feed, untitled := newTestFeed(t, db), newTestFeed(t, db)
		if _, err := db.Exec(`UPDATE feeds SET title = '' WHERE id = $1`, untitled.ID); err != nil {
			t.Fatalf("update feed: %v", err)
		}
		id := insertDebugArticle(t, db, feed.ID, "a", canonical, at)
		insertDebugArticle(t, db, untitled.ID, "a", canonical, at)
		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if len(got.Copies) != 1 || got.Copies[0].FeedTitle != untitled.Url {
			t.Errorf("got copies %+v; want one titled %q", got.Copies, untitled.Url)
		}
	})

	t.Run("empty canonical URL has no copies", func(t *testing.T) {
		feed, other := newTestFeed(t, db), newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", "", at)
		insertDebugArticle(t, db, other.ID, "a", "", at)
		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if len(got.Copies) != 0 {
			t.Errorf("got copies %+v; want none", got.Copies)
		}
	})

	t.Run("copies are capped", func(t *testing.T) {
		canonical := fmt.Sprintf("https://example.com/capped-%d", time.Now().UnixNano())
		feed, other := newTestFeed(t, db), newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", canonical, at)
		for i := range maxArticleCopies + 1 {
			insertDebugArticle(t, db, other.ID, fmt.Sprint(i), canonical, at.Add(time.Duration(i)*time.Second))
		}
		got, err := repo.ArticleDebug(t.Context(), newTestUser(t, db), id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if len(got.Copies) != maxArticleCopies {
			t.Errorf("got %d copies; want %d", len(got.Copies), maxArticleCopies)
		}
	})

	t.Run("this user's read and save", func(t *testing.T) {
		feed := newTestFeed(t, db)
		id := insertDebugArticle(t, db, feed.ID, "a", "", at)
		user, other := newTestUser(t, db), newTestUser(t, db)
		readTime, savedTime := at.Add(time.Hour), at.Add(2*time.Hour)
		if _, err := db.Exec(`INSERT INTO reads (user_id, article_id, read_at) VALUES ($1, $2, $3)`, user, id, readTime); err != nil {
			t.Fatalf("insert read: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO saves (user_id, article_id, saved_at) VALUES ($1, $2, $3)`, user, id, savedTime); err != nil {
			t.Fatalf("insert save: %v", err)
		}

		got, err := repo.ArticleDebug(t.Context(), user, id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if !got.ReadAt.Equal(readTime) || !got.SavedAt.Equal(savedTime) {
			t.Errorf("got ReadAt %v, SavedAt %v; want %v, %v", got.ReadAt, got.SavedAt, readTime, savedTime)
		}

		got, err = repo.ArticleDebug(t.Context(), other, id)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		if !got.ReadAt.IsZero() || !got.SavedAt.IsZero() {
			t.Errorf("another user got ReadAt %v, SavedAt %v; want zero", got.ReadAt, got.SavedAt)
		}
	})
}
