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
		{ExternalID: "a", URL: "https://example.com/a#x", CanonicalURL: "https://example.com/a", ImageURL: "https://example.com/a.jpg", Title: "A", Summary: "<p>a</p>", Content: "<p>A</p>", Excerpt: "a", PublishedAt: date},
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

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d articles, want 3", len(got))
		}
		a := articleByExternalID(t, got, "a")
		if a.FeedID != feed.ID || a.URL != "https://example.com/a#x" || a.CanonicalURL != "https://example.com/a" || a.ImageURL != "https://example.com/a.jpg" || a.Title != "A" || a.Summary != "<p>a</p>" || a.Content != "<p>A</p>" || a.Excerpt != "a" || !a.PublishedAt.Equal(date) {
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

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if b := articleByExternalID(t, got, "b"); b.Title != "B edited" {
			t.Errorf("article b: got title %q, want %q", b.Title, "B edited")
		}
	})

	t.Run("changed image", func(t *testing.T) {
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "d", Title: "D", ImageURL: "https://example.com/d.jpg"}})
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if want := (rss.FetchResult{Updated: 1}); res != want {
			t.Errorf("got %+v, want %+v", res, want)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if d := articleByExternalID(t, got, "d"); d.ImageURL != "https://example.com/d.jpg" {
			t.Errorf("article d: got ImageURL %q, want %q", d.ImageURL, "https://example.com/d.jpg")
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

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
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

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if a := articleByExternalID(t, got, "a"); !a.PublishedAt.Equal(date) {
			t.Errorf("article a: got PublishedAt %v, want %v", a.PublishedAt, date)
		}
	})

	t.Run("timeline position", func(t *testing.T) {
		tf := newTestFeed(t, db)
		old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "x", Title: "X", PublishedAt: old},
			{ExternalID: "y", Title: "Y", PublishedAt: old.Add(time.Hour)},
		}); err != nil {
			t.Fatalf("SaveFetch first: %v", err)
		}
		got, err := articles.ListByFeed(ctx, tf.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		for _, a := range got {
			if !a.TimelineAt.Equal(a.PublishedAt) {
				t.Errorf("first save, article %s: got TimelineAt %v, want published %v", a.ExternalID, a.TimelineAt, a.PublishedAt)
			}
		}

		before := dbNow(t, db)
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "recent", Title: "Recent", PublishedAt: before.Add(-time.Hour)},
			{ExternalID: "ancient", Title: "Ancient", PublishedAt: old.Add(-48 * time.Hour)},
		}); err != nil {
			t.Fatalf("SaveFetch second: %v", err)
		}
		got, err = articles.ListByFeed(ctx, tf.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if r := articleByExternalID(t, got, "recent"); r.TimelineAt.Before(before) {
			t.Errorf("recent: got TimelineAt %v, want at or after %v", r.TimelineAt, before)
		}
		if a := articleByExternalID(t, got, "ancient"); !a.TimelineAt.Equal(a.PublishedAt) {
			t.Errorf("ancient: got TimelineAt %v, want %v", a.TimelineAt, a.PublishedAt)
		}

		x := articleByExternalID(t, got, "x")
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "x", Title: "X edited", Content: "<p>new</p>", PublishedAt: old},
		}); err != nil {
			t.Fatalf("SaveFetch edit: %v", err)
		}
		got, err = articles.ListByFeed(ctx, tf.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if e := articleByExternalID(t, got, "x"); e.Title != "X edited" || !e.TimelineAt.Equal(x.TimelineAt) {
			t.Errorf("edited x: got title %q, TimelineAt %v; want edited title, TimelineAt %v", e.Title, e.TimelineAt, x.TimelineAt)
		}
	})

	t.Run("excerpt updated", func(t *testing.T) {
		ef := newTestFeed(t, db)
		save := func(excerpt string) rss.FetchResult {
			res, err := feeds.SaveFetch(ctx, ef, []rss.Article{{ExternalID: "e", Title: "E", Excerpt: excerpt}})
			if err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
			return res
		}
		save("one")
		if res := save("two"); res.Updated != 1 {
			t.Errorf("changed excerpt: got %+v, want Updated 1", res)
		}
		got, err := articles.ListByFeed(ctx, ef.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if len(got) != 1 || got[0].Excerpt != "two" {
			t.Errorf("got %+v, want excerpt %q", got, "two")
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

	t.Run("stores and clears validators", func(t *testing.T) {
		update := feed
		update.ETag = `W/"v1"`
		update.LastModified = "Mon, 02 Jan 2006 15:04:05 GMT"
		if _, err := feeds.SaveFetch(ctx, update, nil); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		f, err := feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if f.ETag != update.ETag || f.LastModified != update.LastModified {
			t.Errorf("got ETag %q, LastModified %q; want %q, %q", f.ETag, f.LastModified, update.ETag, update.LastModified)
		}

		if _, err := feeds.SaveFetch(ctx, feed, nil); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		f, err = feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if f.ETag != "" || f.LastModified != "" {
			t.Errorf("got ETag %q, LastModified %q; want both cleared", f.ETag, f.LastModified)
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

	// "undated" sorts by created_at (now), so it comes before the dated
	// ones, which are all in the past. "undated-old" gets a created_at
	// between "middle" and "new" below. "tie-1" and "tie-2" share
	// "middle"'s date, so the three are ordered by ID.
	date := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{
		{ExternalID: "old", Excerpt: "old excerpt", ImageURL: "https://example.com/old.jpg", PublishedAt: date},
		{ExternalID: "undated"},
		{ExternalID: "undated-old"},
		{ExternalID: "new", PublishedAt: date.Add(time.Hour)},
		{ExternalID: "middle", PublishedAt: date.Add(time.Minute)},
		{ExternalID: "tie-1", PublishedAt: date.Add(time.Minute)},
		{ExternalID: "tie-2", PublishedAt: date.Add(time.Minute)},
	}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if _, err := db.Exec(`UPDATE articles SET created_at = $1 WHERE feed_id = $2 AND external_id = 'undated-old'`, date.Add(30*time.Minute), feed.ID); err != nil {
		t.Fatalf("set created_at: %v", err)
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

	all, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 100)
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}

	t.Run("returns Excerpt", func(t *testing.T) {
		if a := articleByExternalID(t, all, "old"); a.Excerpt != "old excerpt" {
			t.Errorf("got Excerpt %q, want %q", a.Excerpt, "old excerpt")
		}
	})

	t.Run("returns ImageURL", func(t *testing.T) {
		if a := articleByExternalID(t, all, "old"); a.ImageURL != "https://example.com/old.jpg" {
			t.Errorf("got ImageURL %q, want %q", a.ImageURL, "https://example.com/old.jpg")
		}
	})

	t.Run("returns TimelineAt", func(t *testing.T) {
		got, err := articles.ListByFeed(ctx, other.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if len(got) != 1 || !got[0].TimelineAt.Equal(date.Add(2*time.Hour)) {
			t.Errorf("got %+v, want one article with TimelineAt %v", got, date.Add(2*time.Hour))
		}
	})

	t.Run("newest first, undated by created_at, ties by ID desc", func(t *testing.T) {
		got := externalIDs(all)
		if len(got) != 7 {
			t.Fatalf("got %v, want 7 articles", got)
		}
		// Positions 3-5 are middle, tie-1 and tie-2 in ID order, checked below.
		want := []string{"undated", "new", "undated-old", "", "", "", "old"}
		for i, w := range want {
			if w != "" && got[i] != w {
				t.Errorf("got %v, want %s at %d", got, w, i)
			}
		}
		for i := 1; i < len(all); i++ {
			p, c := all[i-1], all[i]
			if c.FeedSortAt().After(p.FeedSortAt()) || (c.FeedSortAt().Equal(p.FeedSortAt()) && c.ID > p.ID) {
				t.Errorf("order broken between %s and %s", p.ExternalID, c.ExternalID)
			}
		}
		ties := map[string]bool{}
		for _, id := range got[3:6] {
			ties[id] = true
		}
		if !ties["middle"] || !ties["tie-1"] || !ties["tie-2"] {
			t.Errorf("positions 3-5 = %v, want middle, tie-1, tie-2 in some order", got[3:6])
		}
	})

	t.Run("pages with a cursor have no overlap or gap", func(t *testing.T) {
		var got []string
		cursor := rss.ArticleCursor{}
		for range 10 {
			page, err := articles.ListByFeed(ctx, feed.ID, cursor, 2)
			if err != nil {
				t.Fatalf("ListByFeed: %v", err)
			}
			if len(page) > 2 {
				t.Fatalf("page has %d rows, want at most 2", len(page))
			}
			if len(page) == 0 {
				break
			}
			for _, a := range page {
				got = append(got, a.ID)
			}
			last := page[len(page)-1]
			cursor = rss.ArticleCursor{At: last.FeedSortAt(), ID: last.ID}
		}
		if len(got) != len(all) {
			t.Fatalf("paged %d articles, want %d", len(got), len(all))
		}
		for i := range got {
			if got[i] != all[i].ID {
				t.Errorf("paged[%d] = %s, want %s", i, got[i], all[i].ID)
			}
		}
	})

	tests := []struct {
		name   string
		feedID string
		limit  int
		want   []string
	}{
		{"limit", feed.ID, 2, []string{"undated", "new"}},
		{"other feed", other.ID, 10, []string{"other"}},
		{"unknown feed", "00000000-0000-4000-8000-000000000000", 10, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := articles.ListByFeed(ctx, tt.feedID, rss.ArticleCursor{}, tt.limit)
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

func TestTimelineAt(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	prev := now.Add(-6 * time.Hour)
	us := time.Microsecond
	art := func(published time.Time) rss.Article { return rss.Article{PublishedAt: published} }

	tests := []struct {
		name     string
		articles []rss.Article
		prev     time.Time
		want     []time.Time
	}{
		{
			name:     "first save: dated get their dates, future clamped, undated in document order",
			articles: []rss.Article{art(now.Add(-72 * time.Hour)), art(now.Add(time.Hour)), art(time.Time{}), art(time.Time{})},
			want:     []time.Time{now.Add(-72 * time.Hour), now, now, now.Add(-us)},
		},
		{
			name:     "later save: news, backlog and undated",
			articles: []rss.Article{art(now.Add(-time.Hour)), art(prev.Add(-backlogSlack - time.Second)), art(time.Time{})},
			prev:     prev,
			want:     []time.Time{now.Add(-us), prev.Add(-backlogSlack - time.Second), now},
		},
		{
			name: "later save: several news items, newest first, undated ahead",
			articles: []rss.Article{
				art(now.Add(-3 * time.Hour)), art(time.Time{}), art(now.Add(-time.Hour)), art(time.Time{}), art(now.Add(-2 * time.Hour)),
			},
			prev: prev,
			want: []time.Time{now.Add(-4 * us), now, now.Add(-2 * us), now.Add(-us), now.Add(-3 * us)},
		},
		{
			name:     "exactly at the boundary is news",
			articles: []rss.Article{art(prev.Add(-backlogSlack))},
			prev:     prev,
			want:     []time.Time{now},
		},
		{name: "no articles", prev: prev, want: []time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timelineAt(tt.articles, tt.prev, now)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d positions, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !got[i].Equal(tt.want[i]) {
					t.Errorf("article %d: got %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestArticleRepositoryGetByID(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	ctx := t.Context()

	feed := newTestFeed(t, db)
	date := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{
		{ExternalID: "a", URL: "https://example.com/a#x", CanonicalURL: "https://example.com/a", ImageURL: "https://example.com/a.jpg", Title: "A", Summary: "<p>a</p>", Content: "<p>A</p>", Excerpt: "a", PublishedAt: date},
		{ExternalID: "b", Title: "B"},
	}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	listed, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}

	t.Run("returns all fields", func(t *testing.T) {
		want := articleByExternalID(t, listed, "a")
		got, err := articles.GetByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
		if got.FeedID != feed.ID || got.URL != "https://example.com/a#x" || got.CanonicalURL != "https://example.com/a" || got.ImageURL != "https://example.com/a.jpg" || got.Title != "A" || got.Summary != "<p>a</p>" || got.Content != "<p>A</p>" || got.Excerpt != "a" || !got.PublishedAt.Equal(date) || got.TimelineAt.IsZero() {
			t.Errorf("article a: got %+v", got)
		}
	})

	t.Run("undated article has zero PublishedAt", func(t *testing.T) {
		got, err := articles.GetByID(ctx, articleByExternalID(t, listed, "b").ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !got.PublishedAt.IsZero() {
			t.Errorf("PublishedAt = %v, want zero", got.PublishedAt)
		}
	})

	t.Run("unknown ID", func(t *testing.T) {
		_, err := articles.GetByID(ctx, "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})
}

func TestListTimeline(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	feedA, feedB, feedC := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)
	user, other := newTestUser(t, db), newTestUser(t, db)
	for _, f := range []rss.Feed{feedA, feedB} {
		if err := subs.Subscribe(ctx, user, f.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	if err := subs.Subscribe(ctx, other, feedC.ID); err != nil {
		t.Fatalf("Subscribe other: %v", err)
	}

	for feed, ids := range map[*rss.Feed][]string{&feedA: {"a1", "a2", "a3"}, &feedB: {"b1", "b2"}, &feedC: {"c1"}} {
		var batch []rss.Article
		for _, id := range ids {
			batch = append(batch, rss.Article{ExternalID: id})
		}
		if _, err := feeds.SaveFetch(ctx, *feed, batch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
	}

	// Deterministic positions; a2, b1 and b2 tie on timeline_at.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for ext, at := range map[string]time.Time{
		"a1": base, "a2": base.Add(time.Hour), "a3": base.Add(2 * time.Hour),
		"b1": base.Add(time.Hour), "b2": base.Add(time.Hour), "c1": base.Add(3 * time.Hour),
	} {
		if _, err := db.Exec(`UPDATE articles SET timeline_at = $1 WHERE external_id = $2 AND feed_id IN ($3, $4, $5)`, at, ext, feedA.ID, feedB.ID, feedC.ID); err != nil {
			t.Fatalf("set timeline_at: %v", err)
		}
	}

	all, err := articles.ListTimeline(ctx, user, rss.ArticleCursor{}, 100)
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}

	t.Run("only subscribed feeds, ordered by timeline_at then ID desc", func(t *testing.T) {
		if len(all) != 5 {
			t.Fatalf("got %d articles, want 5", len(all))
		}
		if all[0].Article.ExternalID != "a3" || all[4].Article.ExternalID != "a1" {
			t.Errorf("first/last = %s/%s, want a3/a1", all[0].Article.ExternalID, all[4].Article.ExternalID)
		}
		for i, item := range all {
			if item.Feed.ID == feedC.ID {
				t.Errorf("article %s from an unsubscribed feed", item.Article.ExternalID)
			}
			if i > 0 {
				p, c := all[i-1].Article, item.Article
				if c.TimelineAt.After(p.TimelineAt) || (c.TimelineAt.Equal(p.TimelineAt) && c.ID > p.ID) {
					t.Errorf("order broken between %s and %s", p.ExternalID, c.ExternalID)
				}
			}
		}
	})

	t.Run("feed fields are populated", func(t *testing.T) {
		for _, item := range all {
			if item.Feed.ID != item.Article.FeedID || item.Feed.Url == "" || item.Feed.Title != "Old title" {
				t.Errorf("article %s feed = %+v", item.Article.ExternalID, item.Feed)
			}
		}
	})

	t.Run("pages with a cursor have no overlap or gap", func(t *testing.T) {
		var got []string
		cursor := rss.ArticleCursor{}
		for range 10 {
			page, err := articles.ListTimeline(ctx, user, cursor, 2)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			if len(page) > 2 {
				t.Fatalf("page has %d rows, want at most 2", len(page))
			}
			if len(page) == 0 {
				break
			}
			for _, item := range page {
				got = append(got, item.Article.ID)
			}
			last := page[len(page)-1].Article
			cursor = rss.ArticleCursor{At: last.TimelineAt, ID: last.ID}
		}
		if len(got) != len(all) {
			t.Fatalf("paged %d articles, want %d", len(got), len(all))
		}
		for i := range got {
			if got[i] != all[i].Article.ID {
				t.Errorf("paged[%d] = %s, want %s", i, got[i], all[i].Article.ID)
			}
		}
	})

	t.Run("no subscriptions gives an empty slice", func(t *testing.T) {
		got, err := articles.ListTimeline(ctx, newTestUser(t, db), rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("got %v, want empty non-nil slice", got)
		}
	})
}
