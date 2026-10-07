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

// newTestFeed inserts a site and a feed; deleting the site on cleanup
// deletes the feed and its articles.
func newTestFeed(t *testing.T, db *sql.DB) rss.Feed {
	t.Helper()
	siteID := newTestSite(t, db)
	f := rss.Feed{
		SiteID:      siteID,
		Url:         fmt.Sprintf("https://example.com/articles-%d.xml", time.Now().UnixNano()),
		SiteUrl:     "https://example.com/",
		Title:       "Old title",
		Description: "Old description",
		NextFetch:   time.Now().Add(time.Hour),
	}
	id, err := NewFeedRepository(db).Upsert(t.Context(), f)
	if err != nil {
		t.Fatalf("Upsert feed: %v", err)
	}
	f.ID = id
	return f
}

func articleUpdatedAt(t *testing.T, db *sql.DB, feedID, externalID string) time.Time {
	t.Helper()
	var updatedAt time.Time
	if err := db.QueryRow(`SELECT updated_at FROM articles WHERE feed_id = $1 AND external_id = $2`, feedID, externalID).Scan(&updatedAt); err != nil {
		t.Fatalf("select updated_at: %v", err)
	}
	return updatedAt
}

func articleByExternalID(t *testing.T, articles []rss.Article, externalID string) rss.Article {
	t.Helper()
	for _, a := range articles {
		if a.ExternalID == externalID {
			return a
		}
	}
	t.Fatalf("no article with external ID %q", externalID)
	return rss.Article{}
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetch(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	ctx := t.Context()

	feed := newTestFeed(t, db)
	date := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	first := []rss.Article{
		{ExternalID: "a", URL: "https://example.com/a", Title: "A", Summary: "<p>a</p>", Content: "<p>A</p>", PublishedAt: date},
		{ExternalID: "b", URL: "https://example.com/b", Title: "B", PublishedAt: date.Add(time.Hour)},
		{ExternalID: "c", URL: "https://example.com/c", Title: "C"},
	}

	t.Run("first save inserts", func(t *testing.T) {
		res, err := feeds.SaveFetch(ctx, feed, first)
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{New: 3}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d articles, want 3", len(got))
		}
		a := articleByExternalID(t, got, "a")
		if a.FeedID != feed.ID || a.URL != "https://example.com/a" || a.Title != "A" || a.Summary != "<p>a</p>" || a.Content != "<p>A</p>" || !a.PublishedAt.Equal(date) {
			t.Errorf("article a: got %+v", a)
		}
		if c := articleByExternalID(t, got, "c"); !c.PublishedAt.IsZero() {
			t.Errorf("article c: got PublishedAt %v, want zero", c.PublishedAt)
		}

		f, err := feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if f.LastFetched.IsZero() {
			t.Error("LastFetched is zero after SaveFetch")
		}
	})

	t.Run("unchanged save updates nothing", func(t *testing.T) {
		before := articleUpdatedAt(t, db, feed.ID, "a")

		res, err := feeds.SaveFetch(ctx, feed, first)
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}
		if after := articleUpdatedAt(t, db, feed.ID, "a"); !after.Equal(before) {
			t.Errorf("updated_at changed from %v to %v", before, after)
		}
	})

	t.Run("changed and new", func(t *testing.T) {
		changed := first[1]
		changed.Title = "B edited"
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{first[0], changed, {ExternalID: "d", Title: "D"}})
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{New: 1, Updated: 1}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if b := articleByExternalID(t, got, "b"); b.Title != "B edited" {
			t.Errorf("article b: got title %q, want %q", b.Title, "B edited")
		}
	})

	t.Run("missing date is filled in", func(t *testing.T) {
		dated := first[2]
		dated.PublishedAt = date
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{dated})
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{Updated: 1}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if c := articleByExternalID(t, got, "c"); !c.PublishedAt.Equal(date) {
			t.Errorf("article c: got PublishedAt %v, want %v", c.PublishedAt, date)
		}
	})

	t.Run("existing date is kept", func(t *testing.T) {
		redated := first[0]
		redated.PublishedAt = date.Add(24 * time.Hour)
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{redated})
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if a := articleByExternalID(t, got, "a"); !a.PublishedAt.Equal(date) {
			t.Errorf("article a: got PublishedAt %v, want %v", a.PublishedAt, date)
		}
	})

	t.Run("feed metadata", func(t *testing.T) {
		update := feed
		update.Title = "New title"
		update.Description = ""
		update.SiteUrl = ""
		if _, err := feeds.SaveFetch(ctx, update, nil); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		f, err := feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if f.Title != "New title" || f.Description != "Old description" || f.SiteUrl != "https://example.com/" {
			t.Errorf("got title %q, description %q, site URL %q; want %q, %q, %q",
				f.Title, f.Description, f.SiteUrl, "New title", "Old description", "https://example.com/")
		}
		if f.Url != feed.Url {
			t.Errorf("got URL %q, want %q", f.Url, feed.Url)
		}
	})

	t.Run("schedules and records a successful attempt", func(t *testing.T) {
		if err := feeds.RecordFailure(ctx, feed.ID, "boom", time.Now()); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
		update := feed
		update.NextFetch = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		before := dbNow(t, db)
		if _, err := feeds.SaveFetch(ctx, update, nil); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		f, err := feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !f.NextFetch.Equal(update.NextFetch) {
			t.Errorf("got NextFetch %v, want %v", f.NextFetch, update.NextFetch)
		}
		if f.LastAttempt.Before(before) || !f.LastAttempt.Equal(f.LastFetched) {
			t.Errorf("got LastAttempt %v, LastFetched %v; want both the same, not before %v", f.LastAttempt, f.LastFetched, before)
		}
		if f.LastError != "" || f.ConsecutiveFailures != 0 {
			t.Errorf("got LastError %q, ConsecutiveFailures %d; want cleared", f.LastError, f.ConsecutiveFailures)
		}
	})

	t.Run("zero NextFetch is an error", func(t *testing.T) {
		update := feed
		update.NextFetch = time.Time{}
		if _, err := feeds.SaveFetch(ctx, update, nil); err == nil {
			t.Fatal("SaveFetch with zero NextFetch succeeded; want an error")
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		unknown := rss.Feed{ID: "00000000-0000-4000-8000-000000000000", Title: "T", NextFetch: time.Now()}
		_, err := feeds.SaveFetch(ctx, unknown, []rss.Article{{ExternalID: "x", Title: "X"}})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Fatalf("got %v, want rss.ErrNoRecord", err)
		}

		var n int
		if err := db.QueryRow(`SELECT count(*) FROM articles WHERE feed_id = $1`, unknown.ID).Scan(&n); err != nil {
			t.Fatalf("count articles: %v", err)
		}
		if n != 0 {
			t.Errorf("got %d articles for unknown feed, want 0", n)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListByFeed(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	ctx := t.Context()

	feed := newTestFeed(t, db)
	other := newTestFeed(t, db)

	// The undated article sorts by created_at (now), so it comes before
	// the dated ones, which are all in the past.
	date := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{
		{ExternalID: "old", PublishedAt: date},
		{ExternalID: "undated"},
		{ExternalID: "new", PublishedAt: date.Add(time.Hour)},
		{ExternalID: "middle", PublishedAt: date.Add(time.Minute)},
	}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if _, err := feeds.SaveFetch(ctx, other, []rss.Article{{ExternalID: "other", PublishedAt: date.Add(2 * time.Hour)}}); err != nil {
		t.Fatalf("SaveFetch other: %v", err)
	}

	externalIDs := func(as []rss.Article) []string {
		ids := []string{}
		for _, a := range as {
			ids = append(ids, a.ExternalID)
		}
		return ids
	}

	tests := []struct {
		name   string
		feedID string
		limit  int
		want   []string
	}{
		{"all, newest first", feed.ID, 10, []string{"undated", "new", "middle", "old"}},
		{"limit", feed.ID, 2, []string{"undated", "new"}},
		{"other feed", other.ID, 10, []string{"other"}},
		{"unknown feed", "00000000-0000-4000-8000-000000000000", 10, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := articles.ListByFeed(ctx, tt.feedID, tt.limit)
			if err != nil {
				t.Fatalf("ListByFeed: %v", err)
			}
			if got == nil {
				t.Fatal("got nil slice, want non-nil")
			}
			if g := fmt.Sprint(externalIDs(got)); g != fmt.Sprint(tt.want) {
				t.Errorf("got %s, want %s", g, fmt.Sprint(tt.want))
			}
		})
	}
}
