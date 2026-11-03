package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
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
		{ExternalID: "a", URL: "https://example.com/a#x", CanonicalURL: "https://example.com/a", ImageURL: "https://example.com/a.jpg", MediaType: "audio/mpeg", Authors: []string{"Ada", "Grace"}, Title: "A", Summary: "<p>a</p>", Content: "<p>A</p>", Excerpt: "a", PublishedAt: date},
		{ExternalID: "b", URL: "https://example.com/b", Title: "B", PublishedAt: date.Add(time.Hour)},
		{ExternalID: "c", URL: "https://example.com/c", Title: "C"},
	}

	t.Run("first save inserts", func(t *testing.T) {
		res, err := feeds.SaveFetch(ctx, feed, first, feed.NextFetch)
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
		if a.FeedID != feed.ID || a.URL != "https://example.com/a#x" || a.CanonicalURL != "https://example.com/a" || a.ImageURL != "https://example.com/a.jpg" || a.MediaType != "audio/mpeg" || a.Title != "A" || a.Summary != "" || a.Content != "" || a.Excerpt != "a" || !a.PublishedAt.Equal(date) {
			t.Errorf("article a: got %+v", a)
		}
		byID, err := articles.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if byID.Summary != "<p>a</p>" || byID.Content != "<p>A</p>" || byID.MediaType != "audio/mpeg" {
			t.Errorf("article a by ID: got Summary %q, Content %q, MediaType %q, want %q, %q, %q", byID.Summary, byID.Content, byID.MediaType, "<p>a</p>", "<p>A</p>", "audio/mpeg")
		}
		if !slices.Equal(a.Authors, []string{"Ada", "Grace"}) {
			t.Errorf("article a: got Authors %q, want %q", a.Authors, []string{"Ada", "Grace"})
		}
		if b := articleByExternalID(t, got, "b"); len(b.Authors) != 0 {
			t.Errorf("article b: got Authors %q, want none", b.Authors)
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

		res, err := feeds.SaveFetch(ctx, feed, first, feed.NextFetch)
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
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{first[0], changed, {ExternalID: "d", Title: "D"}}, feed.NextFetch)
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
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "d", Title: "D", ImageURL: "https://example.com/d.jpg"}}, feed.NextFetch)
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

	t.Run("changed media type", func(t *testing.T) {
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "d", Title: "D", ImageURL: "https://example.com/d.jpg", MediaType: "video/mp4"}}, feed.NextFetch)
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
		if d := articleByExternalID(t, got, "d"); d.MediaType != "video/mp4" {
			t.Errorf("article d: got MediaType %q, want %q", d.MediaType, "video/mp4")
		}
	})

	t.Run("changed authors", func(t *testing.T) {
		d := rss.Article{ExternalID: "d", Title: "D", ImageURL: "https://example.com/d.jpg", MediaType: "video/mp4"}
		for _, authors := range [][]string{{"Ada"}, nil} {
			d.Authors = authors
			res, err := feeds.SaveFetch(ctx, feed, []rss.Article{d}, feed.NextFetch)
			if err != nil {
				t.Fatalf("SaveFetch: %v", err)
			}
			if want := (rss.FetchResult{Updated: 1}); res != want {
				t.Errorf("authors %q: got %+v, want %+v", authors, res, want)
			}

			got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
			if err != nil {
				t.Fatalf("ListByFeed: %v", err)
			}
			if d := articleByExternalID(t, got, "d"); !slices.Equal(d.Authors, authors) {
				t.Errorf("article d: got Authors %q, want %q", d.Authors, authors)
			}
		}
	})

	t.Run("missing date is filled in", func(t *testing.T) {
		dated := first[2]
		dated.PublishedAt = date
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{dated}, feed.NextFetch)
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
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{redated}, feed.NextFetch)
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

	t.Run("date-only flag follows the kept date", func(t *testing.T) {
		feed := newTestFeed(t, db)
		day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		dateOnly := rss.Article{ExternalID: "date-only", Title: "Date only", PublishedAt: day, PublishedDateOnly: true}
		undated := rss.Article{ExternalID: "undated", Title: "Undated"}
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{dateOnly, undated}, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		// The feed now gives a time: the kept date is still date-only.
		timed := dateOnly
		timed.PublishedAt, timed.PublishedDateOnly = day.Add(14*time.Hour), false
		timed.Title = "Date only edited"
		// The undated article gets a date-only date, which is filled in.
		dated := undated
		dated.PublishedAt, dated.PublishedDateOnly = day, true
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{timed, dated}, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if a := articleByExternalID(t, got, "date-only"); a.Title != "Date only edited" || !a.PublishedAt.Equal(day) || !a.PublishedDateOnly {
			t.Errorf("article date-only: got title %q, PublishedAt %v, PublishedDateOnly %t; want edited, %v, true", a.Title, a.PublishedAt, a.PublishedDateOnly, day)
		}
		if a := articleByExternalID(t, got, "undated"); !a.PublishedAt.Equal(day) || !a.PublishedDateOnly {
			t.Errorf("article undated: got PublishedAt %v, PublishedDateOnly %t; want %v, true", a.PublishedAt, a.PublishedDateOnly, day)
		}

		byID, err := articles.GetByID(ctx, articleByExternalID(t, got, "date-only").ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !byID.PublishedDateOnly {
			t.Error("GetByID: PublishedDateOnly = false, want true")
		}
	})

	t.Run("timeline position", func(t *testing.T) {
		tf := newTestFeed(t, db)
		old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "x", Title: "X", PublishedAt: old},
			{ExternalID: "y", Title: "Y", PublishedAt: old.Add(time.Hour)},
		}, tf.NextFetch); err != nil {
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

		// The second save's news, dated before the first save, is spread
		// between that save and this one.
		var prevFetched time.Time
		if err := db.QueryRow(`SELECT last_fetched_at FROM feeds WHERE id = $1`, tf.ID).Scan(&prevFetched); err != nil {
			t.Fatalf("select last_fetched_at: %v", err)
		}
		before := dbNow(t, db)
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "recent", Title: "Recent", PublishedAt: before.Add(-time.Hour)},
			{ExternalID: "ancient", Title: "Ancient", PublishedAt: old.Add(-48 * time.Hour)},
		}, tf.NextFetch); err != nil {
			t.Fatalf("SaveFetch second: %v", err)
		}
		got, err = articles.ListByFeed(ctx, tf.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		if r := articleByExternalID(t, got, "recent"); !r.TimelineAt.After(prevFetched) || r.TimelineAt.After(dbNow(t, db)) {
			t.Errorf("recent: got TimelineAt %v, want after the previous fetch %v and not after now", r.TimelineAt, prevFetched)
		}
		if a := articleByExternalID(t, got, "ancient"); !a.TimelineAt.Equal(a.PublishedAt) {
			t.Errorf("ancient: got TimelineAt %v, want %v", a.TimelineAt, a.PublishedAt)
		}

		x := articleByExternalID(t, got, "x")
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "x", Title: "X edited", Content: "<p>new</p>", PublishedAt: old},
		}, tf.NextFetch); err != nil {
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

	t.Run("undated articles on a first save aren't on top", func(t *testing.T) {
		tf := newTestFeed(t, db)
		user := newTestUser(t, db)
		if err := NewSubscriptionRepository(db).Subscribe(ctx, user, tf.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		recent := dbNow(t, db).Add(-time.Hour).Truncate(time.Second)
		if _, err := feeds.SaveFetch(ctx, tf, []rss.Article{
			{ExternalID: "recent", Title: "Recent", PublishedAt: recent},
			{ExternalID: "u1", Title: "U1"},
			{ExternalID: "u2", Title: "U2"},
			{ExternalID: "week", Title: "Week", PublishedAt: recent.Add(-7 * 24 * time.Hour)},
		}, tf.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		items, err := articles.ListTimeline(ctx, user, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}

		var order []string
		for _, item := range items {
			order = append(order, item.Article.ExternalID)
		}
		if want := []string{"recent", "u1", "u2", "week"}; !slices.Equal(order, want) {
			t.Fatalf("timeline order %v, want %v", order, want)
		}
		// A day apart below the dated article before them.
		for i, want := range []time.Time{recent.Add(-24 * time.Hour), recent.Add(-48 * time.Hour)} {
			if got := items[i+1].Article.TimelineAt; !got.Equal(want) {
				t.Errorf("%s: got TimelineAt %v, want %v", order[i+1], got, want)
			}
		}
	})

	t.Run("excerpt updated", func(t *testing.T) {
		ef := newTestFeed(t, db)
		save := func(excerpt string) rss.FetchResult {
			res, err := feeds.SaveFetch(ctx, ef, []rss.Article{{ExternalID: "e", Title: "E", Excerpt: excerpt}}, ef.NextFetch)
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
		if _, err := feeds.SaveFetch(ctx, update, nil, update.NextFetch); err != nil {
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
		if _, err := feeds.SaveFetch(ctx, update, nil, update.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		f, err := feeds.GetByID(ctx, feed.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if f.ETag != update.ETag || f.LastModified != update.LastModified {
			t.Errorf("got ETag %q, LastModified %q; want %q, %q", f.ETag, f.LastModified, update.ETag, update.LastModified)
		}

		if _, err := feeds.SaveFetch(ctx, feed, nil, feed.NextFetch); err != nil {
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
		if _, err := feeds.SaveFetch(ctx, update, nil, update.NextFetch); err != nil {
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
		if _, err := feeds.SaveFetch(ctx, update, nil, feed.NextFetch); err == nil {
			t.Fatal("SaveFetch with zero NextFetch succeeded; want an error")
		}
	})

	t.Run("zero catchUp is an error", func(t *testing.T) {
		if _, err := feeds.SaveFetch(ctx, feed, nil, time.Time{}); err == nil {
			t.Fatal("SaveFetch with zero catchUp succeeded; want an error")
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		unknown := rss.Feed{ID: "00000000-0000-4000-8000-000000000000", Title: "T", NextFetch: time.Now()}
		_, err := feeds.SaveFetch(ctx, unknown, []rss.Article{{ExternalID: "x", Title: "X"}}, unknown.NextFetch)
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
		{ExternalID: "old", Title: "Old", Summary: "<p>old summary</p>", Content: "<p>old content</p>", Excerpt: "old excerpt", ImageURL: "https://example.com/old.jpg", PublishedAt: date},
		{ExternalID: "undated"},
		{ExternalID: "undated-old"},
		{ExternalID: "new", PublishedAt: date.Add(time.Hour)},
		{ExternalID: "middle", PublishedAt: date.Add(time.Minute)},
		{ExternalID: "tie-1", PublishedAt: date.Add(time.Minute)},
		{ExternalID: "tie-2", PublishedAt: date.Add(time.Minute)},
	}, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if _, err := db.Exec(`UPDATE articles SET created_at = $1 WHERE feed_id = $2 AND external_id = 'undated-old'`, date.Add(30*time.Minute), feed.ID); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	if _, err := feeds.SaveFetch(ctx, other, []rss.Article{{ExternalID: "other", PublishedAt: date.Add(2 * time.Hour)}}, other.NextFetch); err != nil {
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

	t.Run("leaves Summary and Content empty", func(t *testing.T) {
		a := articleByExternalID(t, all, "old")
		if a.Summary != "" || a.Content != "" {
			t.Errorf("got Summary %q, Content %q, want both empty", a.Summary, a.Content)
		}
		if a.Title != "Old" || a.Excerpt != "old excerpt" || a.ImageURL != "https://example.com/old.jpg" || !a.PublishedAt.Equal(date) || a.TimelineAt.IsZero() || a.CreatedAt.IsZero() {
			t.Errorf("got %+v, want the other fields filled", a)
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
	h := time.Hour
	m := time.Minute
	art := func(published time.Time) rss.Article { return rss.Article{PublishedAt: published} }
	undated := rss.Article{}

	tests := []struct {
		name     string
		articles []rss.Article
		prev     time.Time
		want     []time.Time
	}{
		{
			name:     "first save: dated get their dates, future clamped, undated a day apart from a day back",
			articles: []rss.Article{art(now.Add(-72 * h)), art(now.Add(h)), undated, undated},
			want:     []time.Time{now.Add(-72 * h), now, now.Add(-24 * h), now.Add(-48 * h)},
		},
		{
			name:     "first save, nothing dated",
			articles: []rss.Article{undated, undated, undated},
			want:     []time.Time{now.Add(-24 * h), now.Add(-48 * h), now.Add(-72 * h)},
		},
		{
			name:     "first save, future-dated only",
			articles: []rss.Article{art(now.Add(h)), undated},
			want:     []time.Time{now, now.Add(-24 * h)},
		},
		{
			// The dated items are 6 h apart, so that's the step.
			name:     "first save: undated follow the feed's pace below the item before them",
			articles: []rss.Article{art(now.Add(-30 * h)), undated, art(now.Add(-36 * h)), art(now.Add(-42 * h)), undated},
			want:     []time.Time{now.Add(-30 * h), now.Add(-33 * h), now.Add(-36 * h), now.Add(-42 * h), now.Add(-48 * h)},
		},
		{
			name:     "first save: a run that would reach the next dated item is spread evenly down to it",
			articles: []rss.Article{art(now.Add(-48 * h)), undated, undated, undated, art(now.Add(-50 * h))},
			want: []time.Time{
				now.Add(-48 * h), now.Add(-48*h - 30*time.Minute), now.Add(-49 * h), now.Add(-49*h - 30*time.Minute), now.Add(-50 * h),
			},
		},
		{
			name:     "first save: a spread run still starts no later than a day back",
			articles: []rss.Article{undated, undated, undated, art(now.Add(-25 * h))},
			want:     []time.Time{now.Add(-24 * h), now.Add(-24*h - 20*time.Minute), now.Add(-24*h - 40*time.Minute), now.Add(-25 * h)},
		},
		{
			name:     "first save: undated never later than a day back, and below earlier undated",
			articles: []rss.Article{undated, art(now.Add(-h)), undated, art(now.Add(-2 * h))},
			want:     []time.Time{now.Add(-24 * h), now.Add(-h), now.Add(-25 * h), now.Add(-2 * h)},
		},
		{
			name:     "later save: news, backlog and undated",
			articles: []rss.Article{art(now.Add(-time.Hour)), art(prev.Add(-backlogSlack - time.Second)), art(time.Time{})},
			prev:     prev,
			want:     []time.Time{now.Add(-time.Hour), prev.Add(-backlogSlack - time.Second), now},
		},
		{
			name: "later save: several news items at their dates, undated on top in document order",
			articles: []rss.Article{
				art(now.Add(-3 * time.Hour)), art(time.Time{}), art(now.Add(-time.Hour)), art(time.Time{}), art(now.Add(-2 * time.Hour)),
			},
			prev: prev,
			want: []time.Time{now.Add(-3 * time.Hour), now, now.Add(-time.Hour), now.Add(-us), now.Add(-2 * time.Hour)},
		},
		{
			// At prev - backlogSlack it's news, but below the floor (prev), so
			// it's spread between the floor and now.
			name:     "exactly at the boundary is news",
			articles: []rss.Article{art(prev.Add(-backlogSlack))},
			prev:     prev,
			want:     []time.Time{now.Add(-3 * h)},
		},
		{
			name:     "later save: news at its published date",
			articles: []rss.Article{art(now.Add(-30 * m))},
			prev:     now.Add(-h),
			want:     []time.Time{now.Add(-30 * m)},
		},
		{
			name:     "later save: news dated before the previous fetch is spread above it",
			articles: []rss.Article{art(now.Add(-2 * h)), art(now.Add(-3 * h))},
			prev:     now.Add(-h),
			want:     []time.Time{now.Add(-20 * m), now.Add(-40 * m)},
		},
		{
			name:     "later save: spread newest-published first",
			articles: []rss.Article{art(now.Add(-3 * h)), art(now.Add(-2 * h))},
			prev:     now.Add(-h),
			want:     []time.Time{now.Add(-40 * m), now.Add(-20 * m)},
		},
		{
			name:     "later save: spread below the lowest news placed by date",
			articles: []rss.Article{art(now.Add(-10 * m)), art(now.Add(-2 * h))},
			prev:     now.Add(-h),
			want:     []time.Time{now.Add(-10 * m), now.Add(-35 * m)},
		},
		{
			name:     "later save: the floor is at most newsWindow back",
			articles: []rss.Article{art(now.Add(-20 * h))},
			prev:     now.Add(-24 * h),
			want:     []time.Time{now.Add(-3 * h)},
		},
		{
			name:     "later save: undated news on top in document order, dated below it",
			articles: []rss.Article{undated, undated, art(now.Add(-5 * m))},
			prev:     now.Add(-h),
			want:     []time.Time{now, now.Add(-us), now.Add(-5 * m)},
		},
		{
			name:     "later save: future-dated news just below undated news",
			articles: []rss.Article{undated, art(now.Add(h))},
			prev:     now.Add(-h),
			want:     []time.Time{now, now.Add(-us)},
		},
		{
			name:     "later save: future-dated news without undated news at now",
			articles: []rss.Article{art(now.Add(h))},
			prev:     now.Add(-h),
			want:     []time.Time{now},
		},
		{
			name:     "later save: spread below the undated news",
			articles: []rss.Article{undated, art(now.Add(-2 * h))},
			prev:     now.Add(-h),
			want:     []time.Time{now, now.Add(-us - (time.Hour-us)/2)},
		},
		{
			name:     "later save: backlog at its date",
			articles: []rss.Article{art(now.Add(-h - backlogSlack - time.Second))},
			prev:     now.Add(-h),
			want:     []time.Time{now.Add(-h - backlogSlack - time.Second)},
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

func TestIsBacklog(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	prev := now.Add(-time.Hour)
	art := func(published time.Time) rss.Article { return rss.Article{PublishedAt: published} }
	tests := []struct {
		name    string
		article rss.Article
		prev    time.Time
		want    bool
	}{
		{"first save, published an hour ago", art(now.Add(-time.Hour)), time.Time{}, true},
		{"first save, future-dated", art(now.Add(time.Hour)), time.Time{}, true},
		{"first save, undated", art(time.Time{}), time.Time{}, true},
		{"later save, recent", art(now.Add(-2 * time.Hour)), prev, false},
		{"later save, undated", art(time.Time{}), prev, false},
		{"later save, exactly at the boundary", art(prev.Add(-backlogSlack)), prev, false},
		{"later save, before the boundary", art(prev.Add(-backlogSlack - time.Second)), prev, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBacklog(tt.article, tt.prev); got != tt.want {
				t.Errorf("isBacklog = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRaisePositions(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	m := time.Minute
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	tests := []struct {
		name      string
		positions []time.Time
		seenUpTo  time.Time
		want      []time.Time
	}{
		{
			name:      "no floor: unchanged",
			positions: []time.Time{ago(30 * m), ago(3 * time.Hour)},
			want:      []time.Time{ago(30 * m), ago(3 * time.Hour)},
		},
		{
			name:      "all after seenUpTo: unchanged",
			positions: []time.Time{ago(time.Hour), ago(10 * m)},
			seenUpTo:  ago(3 * time.Hour),
			want:      []time.Time{ago(time.Hour), ago(10 * m)},
		},
		{
			name:      "one at or before seenUpTo goes halfway to now",
			positions: []time.Time{ago(30 * m)},
			seenUpTo:  ago(10 * m),
			want:      []time.Time{ago(5 * m)},
		},
		{
			name:      "several are spread, newest first, keeping their order",
			positions: []time.Time{ago(40 * m), ago(20 * m)},
			seenUpTo:  ago(10 * m),
			want:      []time.Time{ago(6*m + 40*time.Second), ago(3*m + 20*time.Second)},
		},
		{
			name:      "spread below the lowest kept one",
			positions: []time.Time{ago(5 * m), ago(30 * m)},
			seenUpTo:  ago(10 * m),
			want:      []time.Time{ago(5 * m), ago(7*m + 30*time.Second)},
		},
		{
			name:      "exactly at seenUpTo is raised",
			positions: []time.Time{ago(10 * m)},
			seenUpTo:  ago(10 * m),
			want:      []time.Time{ago(5 * m)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := raisePositions(tt.positions, tt.seenUpTo, now)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d positions, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !got[i].Equal(tt.want[i]) {
					t.Errorf("position %d: got %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}

	t.Run("never at or before seenUpTo", func(t *testing.T) {
		seenUpTo := ago(2 * time.Microsecond)
		positions := []time.Time{ago(time.Hour), ago(time.Hour), ago(time.Hour)}
		for i, p := range raisePositions(positions, seenUpTo, now) {
			if !p.After(seenUpTo) || p.After(now) {
				t.Errorf("position %d: got %v, want after %v and not after %v", i, p, seenUpTo, now)
			}
		}
	})

	t.Run("doesn't change its argument", func(t *testing.T) {
		positions := []time.Time{ago(30 * m)}
		raisePositions(positions, ago(10*m), now)
		if !positions[0].Equal(ago(30 * m)) {
			t.Errorf("positions[0] = %v, want %v", positions[0], ago(30*m))
		}
	})
}

// setTimelineMark sets userID's timeline mark: what they've seen up to, and
// when they reported it, each that long before the database's now().
func setTimelineMark(t *testing.T, db *sql.DB, userID string, seenUpToAgo, seenAgo time.Duration) time.Time {
	t.Helper()
	var seenUpTo time.Time
	err := db.QueryRow(`INSERT INTO timeline_marks (user_id, seen_up_to, seen_at)
		VALUES ($1, now() - $2 * interval '1 microsecond', now() - $3 * interval '1 microsecond')
		ON CONFLICT (user_id) DO UPDATE SET seen_up_to = EXCLUDED.seen_up_to, seen_at = EXCLUDED.seen_at
		RETURNING seen_up_to`,
		userID, seenUpToAgo.Microseconds(), seenAgo.Microseconds()).Scan(&seenUpTo)
	if err != nil {
		t.Fatalf("set timeline mark: %v", err)
	}
	return seenUpTo
}

// readerPositions returns userID's own positions (timeline_entries rows), by
// article ID.
func readerPositions(t *testing.T, db *sql.DB, userID string) map[string]time.Time {
	t.Helper()
	rows, err := db.Query(`SELECT article_id, timeline_at FROM timeline_entries WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatalf("select timeline_entries: %v", err)
	}
	defer rows.Close()
	got := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatalf("scan timeline_entries: %v", err)
		}
		got[id] = at
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("select timeline_entries: %v", err)
	}
	return got
}

// insertPositionedArticle inserts an article of feedID positioned that long
// before the database's now(), and returns its ID and position.
func insertPositionedArticle(t *testing.T, db *sql.DB, feedID, externalID string, ago time.Duration) (string, time.Time) {
	t.Helper()
	var id string
	var at time.Time
	err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, title, timeline_at, created_at)
		VALUES ($1, $2, $2, now() - $3 * interval '1 microsecond', now() - $3 * interval '1 microsecond')
		RETURNING id, timeline_at`, feedID, externalID, ago.Microseconds()).Scan(&id, &at)
	if err != nil {
		t.Fatalf("insert %s: %v", externalID, err)
	}
	return id, at
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchWritesReaderPositions(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	subscribe := func(t *testing.T, feedID string, users ...string) {
		t.Helper()
		for _, u := range users {
			if err := subs.Subscribe(ctx, u, feedID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}
	// byExternalID returns feedID's articles by external ID.
	byExternalID := func(t *testing.T, feedID string) map[string]rss.Article {
		t.Helper()
		got, err := articles.ListByFeed(ctx, feedID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		m := map[string]rss.Article{}
		for _, a := range got {
			m[a.ExternalID] = a
		}
		return m
	}

	active, below, away, noMark, other := newTestUser(t, db), newTestUser(t, db), newTestUser(t, db), newTestUser(t, db), newTestUser(t, db)
	activeSeen := setTimelineMark(t, db, active, 10*time.Minute, 10*time.Minute)
	setTimelineMark(t, db, below, 2*time.Hour, 2*time.Hour)
	setTimelineMark(t, db, away, 7*time.Hour, 7*time.Hour)
	setTimelineMark(t, db, other, 10*time.Minute, 10*time.Minute)

	// A feed with a previous fetch an hour ago, then a later save.
	feed := newTestFeed(t, db)
	if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "first", Title: "First", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}, feed.NextFetch); err != nil {
		t.Fatalf("first SaveFetch: %v", err)
	}
	if _, err := db.Exec(`UPDATE feeds SET last_fetched_at = now() - interval '1 hour', last_attempt_at = now() - interval '1 hour' WHERE id = $1`, feed.ID); err != nil {
		t.Fatalf("set last_fetched_at: %v", err)
	}
	subscribe(t, feed.ID, active, below, away, noMark)

	now := dbNow(t, db).Truncate(time.Second)
	later := []rss.Article{
		{ExternalID: "recent", Title: "Recent", PublishedAt: now.Add(-30 * time.Minute)},
		{ExternalID: "backlog", Title: "Backlog", PublishedAt: now.Add(-72 * time.Hour)},
		{ExternalID: "undated", Title: "Undated"},
	}
	if _, err := feeds.SaveFetch(ctx, feed, later, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	saved := byExternalID(t, feed.ID)

	t.Run("a row per inserted news article for each follower who looked in the last NewsWindow", func(t *testing.T) {
		for _, u := range []string{active, below} {
			got := readerPositions(t, db, u)
			if len(got) != 2 {
				t.Errorf("got %d rows, want 2: %v", len(got), got)
			}
			for _, id := range []string{"recent", "undated"} {
				if _, ok := got[saved[id].ID]; !ok {
					t.Errorf("no row for %s", id)
				}
			}
			if _, ok := got[saved["backlog"].ID]; ok {
				t.Error("a row for the backlog item")
			}
		}
	})

	t.Run("news dated below what the reader saw goes above it", func(t *testing.T) {
		a := saved["recent"]
		got := readerPositions(t, db, active)[a.ID]
		if !got.After(activeSeen) || !got.After(a.TimelineAt) {
			t.Errorf("got %v, want after seen_up_to %v and timeline_at %v", got, activeSeen, a.TimelineAt)
		}
	})

	t.Run("a floor below the shared floor gets the shared positions", func(t *testing.T) {
		got := readerPositions(t, db, below)
		for _, id := range []string{"recent", "undated"} {
			if a := saved[id]; !got[a.ID].Equal(a.TimelineAt) {
				t.Errorf("%s: got %v, want timeline_at %v", id, got[a.ID], a.TimelineAt)
			}
		}
	})

	t.Run("no rows for a follower away longer than NewsWindow, without a mark, or not following", func(t *testing.T) {
		for name, u := range map[string]string{"away": away, "no mark": noMark, "not following": other} {
			if got := readerPositions(t, db, u); len(got) != 0 {
				t.Errorf("%s: got %d rows, want none", name, len(got))
			}
		}
	})

	t.Run("a first save's posts from the last NewsWindow get rows, raised", func(t *testing.T) {
		added := newTestFeed(t, db)
		subscribe(t, added.ID, active, away)
		before, beforeAway := readerPositions(t, db, active), readerPositions(t, db, away)
		if _, err := feeds.SaveFetch(ctx, added, []rss.Article{
			{ExternalID: "hour", Title: "Hour", PublishedAt: now.Add(-time.Hour)},
			{ExternalID: "days", Title: "Days", PublishedAt: now.Add(-48 * time.Hour)},
			{ExternalID: "undated", Title: "Undated"},
		}, added.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		first := byExternalID(t, added.ID)

		got := readerPositions(t, db, active)
		if len(got) != len(before)+1 {
			t.Errorf("got %d new rows, want 1", len(got)-len(before))
		}
		hour, ok := got[first["hour"].ID]
		if !ok {
			t.Fatal("no row for the hour-ago item")
		}
		if !hour.After(activeSeen) {
			t.Errorf("hour-ago item: got %v, want after seen_up_to %v", hour, activeSeen)
		}
		if got := readerPositions(t, db, away); len(got) != len(beforeAway) {
			t.Errorf("away: got %d new rows, want none", len(got)-len(beforeAway))
		}
	})

	t.Run("an update writes no rows and changes none", func(t *testing.T) {
		before := map[string]map[string]time.Time{active: readerPositions(t, db, active), below: readerPositions(t, db, below)}
		changed := slices.Clone(later)
		changed[0].Title = "Recent, edited"
		changed[2].Title = "Undated, edited"
		res, err := feeds.SaveFetch(ctx, feed, changed, feed.NextFetch)
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if res.Updated != 2 {
			t.Fatalf("got %+v, want 2 updated", res)
		}
		for u, want := range before {
			if got := readerPositions(t, db, u); !maps.EqualFunc(got, want, time.Time.Equal) {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestReaderPositionsDeletedWith(t *testing.T) {
	db := psqltest.NewDB(t)
	feed := newTestFeed(t, db)

	// insertRow gives userID a row for articleID.
	insertRow := func(t *testing.T, userID, articleID string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at) VALUES ($1, $2, $3, now())`, userID, articleID, feed.ID); err != nil {
			t.Fatalf("insert timeline_entries: %v", err)
		}
	}
	count := func(t *testing.T, column, id string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM timeline_entries WHERE `+column+` = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count timeline_entries: %v", err)
		}
		return n
	}

	t.Run("the user", func(t *testing.T) {
		user := newTestUser(t, db)
		article, _ := insertPositionedArticle(t, db, feed.ID, "user-deleted", time.Hour)
		insertRow(t, user, article)
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, user); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		if n := count(t, "user_id", user); n != 0 {
			t.Errorf("got %d rows, want none", n)
		}
	})

	t.Run("the article", func(t *testing.T) {
		user := newTestUser(t, db)
		article, _ := insertPositionedArticle(t, db, feed.ID, "article-deleted", time.Hour)
		insertRow(t, user, article)
		if _, err := db.Exec(`DELETE FROM articles WHERE id = $1`, article); err != nil {
			t.Fatalf("delete article: %v", err)
		}
		if n := count(t, "article_id", article); n != 0 {
			t.Errorf("got %d rows, want none", n)
		}
	})
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
	}, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	listed, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}

	t.Run("returns all fields", func(t *testing.T) {
		// Lists leave out the article HTML; GetByID returns it.
		want := articleByExternalID(t, listed, "a")
		want.Summary, want.Content = "<p>a</p>", "<p>A</p>"
		got, err := articles.GetByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
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
		if _, err := feeds.SaveFetch(ctx, *feed, batch, (*feed).NextFetch); err != nil {
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

	all, err := articles.ListTimeline(ctx, user, rss.TimelineFilter{}, rss.ArticleCursor{}, 100)
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
			page, err := articles.ListTimeline(ctx, user, rss.TimelineFilter{}, cursor, 2)
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
		got, err := articles.ListTimeline(ctx, newTestUser(t, db), rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("got %v, want empty non-nil slice", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineReaderPositions(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	reads := NewReadRepository(db)
	ctx := t.Context()

	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	at := func(hour, min int) time.Time { return time.Date(2026, 1, 10, hour, min, 0, 0, time.UTC) }

	follow := func(t *testing.T, userID string, feeds ...rss.Feed) {
		t.Helper()
		for _, f := range feeds {
			if err := subs.Subscribe(ctx, userID, f.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}
	// insert inserts an article of feedID titled title, positioned (and
	// created) at position, and returns its ID. A canonical URL flags it as
	// having copies.
	insert := func(t *testing.T, feedID, title string, position time.Time, canonical string) string {
		t.Helper()
		var id string
		err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, title, url, canonical_url, has_copies, timeline_at, created_at)
			VALUES ($1, $2::text, $2::text, 'https://example.com/' || $2::text, $3::text, $3::text <> '', $4, $4)
			RETURNING id`, feedID, title, canonical, position).Scan(&id)
		if err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}
	// row gives userID their own position for articleID.
	row := func(t *testing.T, userID, articleID string, position time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at)
			SELECT $1, id, feed_id, $3 FROM articles WHERE id = $2`, userID, articleID, position); err != nil {
			t.Fatalf("insert timeline_entries: %v", err)
		}
	}
	list := func(t *testing.T, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) []rss.ArticleWithFeed {
		t.Helper()
		items, err := articles.ListTimeline(ctx, userID, filter, before, limit)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		return items
	}
	titles := func(items []rss.ArticleWithFeed) []string {
		var got []string
		for _, item := range items {
			got = append(got, item.Article.Title)
		}
		return got
	}
	check := func(t *testing.T, items []rss.ArticleWithFeed, want ...string) {
		t.Helper()
		if got := titles(items); !slices.Equal(got, want) {
			t.Errorf("got  %v\nwant %v", got, want)
		}
	}
	// lastOf is the cursor of a page ending with items' last entry.
	lastOf := func(items []rss.ArticleWithFeed) rss.ArticleCursor {
		last := items[len(items)-1]
		return rss.ArticleCursor{At: last.Position, ID: last.Article.ID}
	}

	t.Run("a row puts the article at the reader's position", func(t *testing.T) {
		feed := newTestFeed(t, db)
		reader, other := newTestUser(t, db), newTestUser(t, db)
		follow(t, reader, feed)
		follow(t, other, feed)
		moved := insert(t, feed.ID, "moved", base.Add(-4*time.Hour), "")
		insert(t, feed.ID, "mid", base.Add(-time.Hour), "")
		row(t, reader, moved, base.Add(-5*time.Minute))

		items := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		check(t, items, "moved", "mid")
		if len(items) == 2 && (!items[0].Position.Equal(base.Add(-5*time.Minute)) || !items[0].Article.TimelineAt.Equal(base.Add(-4*time.Hour))) {
			t.Errorf("moved: Position %v, TimelineAt %v; want the row's %v and the shared %v", items[0].Position, items[0].Article.TimelineAt, base.Add(-5*time.Minute), base.Add(-4*time.Hour))
		}

		t.Run("another reader sees the shared position", func(t *testing.T) {
			items := list(t, other, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
			check(t, items, "mid", "moved")
			for _, item := range items {
				if !item.Position.Equal(item.Article.TimelineAt) {
					t.Errorf("%s: Position %v, want its timeline_at %v", item.Article.Title, item.Position, item.Article.TimelineAt)
				}
			}
		})
	})

	t.Run("an arrival keeps its place between two looks", func(t *testing.T) {
		reader := newTestUser(t, db)
		followed := newTestFeed(t, db)
		_, top := insertPositionedArticle(t, db, followed.ID, "top", 30*time.Minute)
		insertPositionedArticle(t, db, followed.ID, "below", 2*time.Hour)

		// Feeds fetched an hour ago, so their next saves are news.
		fetched := func(t *testing.T) rss.Feed {
			t.Helper()
			f := newTestFeed(t, db)
			if _, err := feeds.SaveFetch(ctx, f, []rss.Article{{ExternalID: "old", Title: "old", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}, f.NextFetch); err != nil {
				t.Fatalf("first SaveFetch: %v", err)
			}
			if _, err := db.Exec(`UPDATE feeds SET last_fetched_at = now() - interval '1 hour', last_attempt_at = now() - interval '1 hour' WHERE id = $1`, f.ID); err != nil {
				t.Fatalf("set last_fetched_at: %v", err)
			}
			return f
		}
		busy, another := fetched(t), fetched(t)
		follow(t, reader, followed, busy, another)
		// The reader had the top of their timeline on screen a minute ago.
		if _, err := db.Exec(`INSERT INTO timeline_marks (user_id, seen_up_to, seen_at) VALUES ($1, $2, now() - interval '1 minute')`, reader, top); err != nil {
			t.Fatalf("set timeline mark: %v", err)
		}

		now := dbNow(t, db)
		if _, err := feeds.SaveFetch(ctx, busy, []rss.Article{{ExternalID: "arrival", Title: "arrival", PublishedAt: now.Add(-4 * time.Hour)}}, busy.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		position := func(t *testing.T, items []rss.ArticleWithFeed) (int, time.Time) {
			t.Helper()
			for i, item := range items {
				if item.Article.Title == "arrival" {
					return i, item.Position
				}
			}
			t.Fatalf("no arrival in %v", titles(items))
			return 0, time.Time{}
		}

		look1 := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		i, p1 := position(t, look1)
		if i != 0 || !p1.After(top) {
			t.Errorf("look 1: arrival at %d (position %v), want first, above top %v: %v", i, p1, top, titles(look1))
		}
		if err := reads.MarkSeen(ctx, reader, "", look1[0].Position); err != nil {
			t.Fatalf("MarkSeen: %v", err)
		}

		look2 := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		if i, p2 := position(t, look2); i != 0 || !p2.Equal(p1) {
			t.Errorf("look 2: arrival at %d, position %v; want first at %v: %v", i, p2, p1, titles(look2))
		}

		if _, err := feeds.SaveFetch(ctx, another, []rss.Article{{ExternalID: "newer", Title: "newer"}}, another.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		look3 := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		if i, p3 := position(t, look3); i != 1 || look3[0].Article.Title != "newer" || !p3.Equal(p1) {
			t.Errorf("look 3: arrival at %d, position %v; want second, below newer, at %v: %v", i, p3, p1, titles(look3))
		}
	})

	t.Run("articles without a row merge in at their shared positions", func(t *testing.T) {
		feed := newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, feed)
		r1 := insert(t, feed.ID, "r1", base.Add(-5*time.Hour), "")
		r2 := insert(t, feed.ID, "r2", base.Add(-6*time.Hour), "")
		insert(t, feed.ID, "b", base.Add(-2*time.Hour), "")
		row(t, reader, r1, base.Add(-time.Hour))
		row(t, reader, r2, base.Add(-3*time.Hour))
		check(t, list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10), "r1", "b", "r2")
	})

	t.Run("a full page of rows bounds the rest, and paging has no gap or overlap", func(t *testing.T) {
		feed := newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, feed)
		// The rows' articles are positioned lower for everyone else.
		for _, r := range []struct {
			title          string
			shared, theirs time.Time
		}{{"r10", at(3, 0), at(10, 0)}, {"r9", at(2, 0), at(9, 0)}, {"r7", at(1, 0), at(7, 0)}} {
			row(t, reader, insert(t, feed.ID, r.title, r.shared, ""), r.theirs)
		}
		insert(t, feed.ID, "n8", at(8, 0), "")
		insert(t, feed.ID, "h6", at(6, 0), "")
		insert(t, feed.ID, "h5", at(5, 0), "")

		var pages [][]string
		cursor := rss.ArticleCursor{}
		for range 10 {
			page := list(t, reader, rss.TimelineFilter{}, cursor, 2)
			if len(page) == 0 {
				break
			}
			pages = append(pages, titles(page))
			cursor = lastOf(page)
		}
		want := [][]string{{"r10", "r9"}, {"n8", "r7"}, {"h6", "h5"}}
		if !slices.EqualFunc(pages, want, slices.Equal) {
			t.Errorf("pages %v, want %v", pages, want)
		}

		t.Run("past the rows, articles without a row continue in shared order", func(t *testing.T) {
			check(t, list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{At: at(6, 30), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}, 10), "h6", "h5")
		})
	})

	t.Run("an article without a row is at its shared position", func(t *testing.T) {
		feed := newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, feed)
		a := insert(t, feed.ID, "a", base.Add(-4*time.Hour), "")
		insert(t, feed.ID, "b", base.Add(-time.Hour), "")
		row(t, reader, a, base.Add(-5*time.Minute))
		check(t, list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10), "a", "b")

		if _, err := db.Exec(`DELETE FROM timeline_entries WHERE user_id = $1 AND article_id = $2`, reader, a); err != nil {
			t.Fatalf("delete row: %v", err)
		}
		items := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		check(t, items, "b", "a")
		if len(items) == 2 && !items[1].Position.Equal(base.Add(-4*time.Hour)) {
			t.Errorf("a: Position %v, want its timeline_at %v", items[1].Position, base.Add(-4*time.Hour))
		}
	})

	t.Run("rows of feeds the reader no longer follows aren't shown", func(t *testing.T) {
		kept, left := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, kept, left)
		insert(t, kept.ID, "kept", base.Add(-time.Hour), "")
		row(t, reader, insert(t, left.ID, "left", base.Add(-2*time.Hour), ""), base.Add(-time.Minute))
		if _, err := db.Exec(`DELETE FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, reader, left.ID); err != nil {
			t.Fatalf("delete subscription: %v", err)
		}
		check(t, list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10), "kept")
	})

	t.Run("filters apply to rows", func(t *testing.T) {
		f1, f2 := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, f1, f2)
		row(t, reader, insert(t, f1.ID, "Quasar news", base.Add(-3*time.Hour), ""), base.Add(-time.Minute))
		row(t, reader, insert(t, f1.ID, "Other news", base.Add(-4*time.Hour), ""), base.Add(-2*time.Minute))
		row(t, reader, insert(t, f2.ID, "Left out", base.Add(-5*time.Hour), ""), base.Add(-3*time.Minute))

		check(t, list(t, reader, rss.TimelineFilter{ExcludeFeedIDs: []string{f2.ID}}, rss.ArticleCursor{}, 10), "Quasar news", "Other news")
		check(t, list(t, reader, rss.TimelineFilter{IncludeWords: []string{"quasar"}}, rss.ArticleCursor{}, 10), "Quasar news")
	})

	t.Run("copies collapse by the reader's positions", func(t *testing.T) {
		f1, f2 := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		follow(t, reader, f1, f2)
		canonical := fmt.Sprintf("https://example.com/copy-%d", time.Now().UnixNano())
		// By shared positions A is the earlier copy; by the reader's, B.
		a := insert(t, f1.ID, "A", at(8, 0), canonical)
		b := insert(t, f2.ID, "B", at(9, 0), canonical)
		row(t, reader, a, at(10, 0))

		items := list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		check(t, items, "B")
		if len(items) == 1 && (!items[0].Position.Equal(at(9, 0)) || len(items[0].AlsoIn) != 1 || items[0].AlsoIn[0].ID != f1.ID) {
			t.Errorf("B: Position %v, AlsoIn %v; want %v, also in feed 1", items[0].Position, items[0].AlsoIn, at(9, 0))
		}

		row(t, reader, b, at(10, 5))
		items = list(t, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		check(t, items, "A")
		if len(items) == 1 && !items[0].Position.Equal(at(10, 0)) {
			t.Errorf("A: Position %v, want %v", items[0].Position, at(10, 0))
		}
	})

	t.Run("ListShared ignores rows", func(t *testing.T) {
		feed := newTestFeed(t, db)
		owner := newTestUser(t, db)
		follow(t, owner, feed)
		insert(t, feed.ID, "a", base.Add(-time.Hour), "")
		row(t, owner, insert(t, feed.ID, "b", base.Add(-2*time.Hour), ""), base.Add(-time.Minute))

		got, err := articles.ListShared(ctx, owner, rss.TimelineFilter{}, 10)
		if err != nil {
			t.Fatalf("ListShared: %v", err)
		}
		var order []string
		for _, item := range got {
			order = append(order, item.Article.Title)
		}
		if want := []string{"a", "b"}; !slices.Equal(order, want) {
			t.Errorf("got %v, want %v", order, want)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
//
// A newly followed feed's posts from the last rss.NewsWindow go above what
// the reader saw, and its older posts stay at their dates: both keep their
// places on the next look.
func TestListTimelineNewlyFollowedFeed(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	reads := NewReadRepository(db)
	ctx := t.Context()

	followed, added := newTestFeed(t, db), newTestFeed(t, db)
	reader := newTestUser(t, db)
	if err := subs.Subscribe(ctx, reader, followed.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_, newer := insertPositionedArticle(t, db, followed.ID, "newer", 30*time.Minute)
	insertPositionedArticle(t, db, followed.ID, "older", 3*time.Hour)
	// An active reader: the top of their timeline on screen a minute ago.
	if _, err := db.Exec(`INSERT INTO timeline_marks (user_id, seen_up_to, seen_at) VALUES ($1, $2, now() - interval '1 minute')`, reader, newer); err != nil {
		t.Fatalf("set timeline mark: %v", err)
	}

	now := dbNow(t, db).Truncate(time.Second)
	if _, err := feeds.SaveFetch(ctx, added, []rss.Article{
		{ExternalID: "hour", Title: "hour", PublishedAt: now.Add(-time.Hour)},
		{ExternalID: "days", Title: "days", PublishedAt: now.Add(-48 * time.Hour)},
	}, added.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if err := subs.Subscribe(ctx, reader, added.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	look := func(t *testing.T) ([]string, map[string]rss.ArticleWithFeed) {
		t.Helper()
		items, err := articles.ListTimeline(ctx, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		var order []string
		byTitle := map[string]rss.ArticleWithFeed{}
		for _, item := range items {
			order = append(order, item.Article.Title)
			byTitle[item.Article.Title] = item
		}
		return order, byTitle
	}

	order, first := look(t)
	if want := []string{"hour", "newer", "older", "days"}; !slices.Equal(order, want) {
		t.Errorf("got  %v\nwant %v", order, want)
	}
	if p := first["hour"].Position; !p.After(newer) {
		t.Errorf("hour: Position %v, want above what the reader saw, %v", p, newer)
	}
	if d := first["days"]; !d.Position.Equal(d.Article.TimelineAt) {
		t.Errorf("days: Position %v, want its timeline_at %v", d.Position, d.Article.TimelineAt)
	}

	if err := reads.MarkSeen(ctx, reader, "", first["hour"].Position); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	order, second := look(t)
	if want := []string{"hour", "newer", "older", "days"}; !slices.Equal(order, want) {
		t.Errorf("second look: got %v, want %v", order, want)
	}
	for _, title := range []string{"hour", "days"} {
		if !second[title].Position.Equal(first[title].Position) {
			t.Errorf("second look: %s at %v, want unchanged %v", title, second[title].Position, first[title].Position)
		}
	}
}

func TestListTimelineCollapsesCopies(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	// The reader subscribes to A, B and C; D is subscribed only by someone else.
	feedA, feedB, feedC, feedD := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)
	reader, bOnly, other := newTestUser(t, db), newTestUser(t, db), newTestUser(t, db)
	for _, s := range []struct {
		user string
		feed rss.Feed
	}{{reader, feedA}, {reader, feedB}, {reader, feedC}, {bOnly, feedB}, {other, feedD}} {
		if err := subs.Subscribe(ctx, s.user, s.feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	nano := time.Now().UnixNano()
	postX := fmt.Sprintf("https://example.com/post-x-%d", nano)
	postZ := fmt.Sprintf("https://example.com/post-z-%d", nano)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// x is in A, B and D: D's copy is earliest but unsubscribed, B's arrives
	// after A's. z is in A and C, C's first. e1 and e2 share a title but have
	// no canonical URL.
	type copyOf struct {
		feed     rss.Feed
		ext, url string
		title    string
		offset   time.Duration
	}
	for _, c := range []copyOf{
		{feedA, "x", postX, "Post X", time.Hour},
		{feedB, "x", postX, "Post X", 3 * time.Hour},
		{feedD, "x", postX, "Post X", 0},
		{feedA, "z", postZ, "Post Z", 6 * time.Hour},
		{feedC, "z", postZ, "Post Z", 30 * time.Minute},
		{feedC, "e1", "", "Same", 2 * time.Hour},
		{feedC, "e2", "", "Same", 4 * time.Hour},
	} {
		a := rss.Article{ExternalID: c.ext, CanonicalURL: c.url, Title: c.title, Summary: "<p>summary</p>", Content: "<p>content</p>", Excerpt: "excerpt"}
		if _, err := feeds.SaveFetch(ctx, c.feed, []rss.Article{a}, c.feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if _, err := db.Exec(`UPDATE articles SET timeline_at = $1 WHERE feed_id = $2 AND external_id = $3`, base.Add(c.offset), c.feed.ID, c.ext); err != nil {
			t.Fatalf("set timeline_at: %v", err)
		}
	}

	all, err := articles.ListTimeline(ctx, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 100)
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}

	type entry struct {
		ext, feedID string
		at          string // timeline_at, RFC 3339, so failures print readably
		alsoIn      []string
	}
	entries := func(items []rss.ArticleWithFeed) []entry {
		var got []entry
		for _, item := range items {
			e := entry{ext: item.Article.ExternalID, feedID: item.Feed.ID, at: item.Article.TimelineAt.UTC().Format(time.RFC3339)}
			for _, f := range item.AlsoIn {
				e.alsoIn = append(e.alsoIn, f.ID)
			}
			got = append(got, e)
		}
		return got
	}
	at := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339) }
	want := []entry{
		{"e2", feedC.ID, at(4 * time.Hour), nil},
		{"e1", feedC.ID, at(2 * time.Hour), nil},
		{"x", feedA.ID, at(time.Hour), []string{feedB.ID}},
		{"z", feedC.ID, at(30 * time.Minute), []string{feedA.ID}},
	}
	assertEntries := func(t *testing.T, got, want []entry) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %d entries %+v, want %d %+v", len(got), got, len(want), want)
		}
		for i := range want {
			g, w := got[i], want[i]
			if g.ext != w.ext || g.feedID != w.feedID || g.at != w.at || !slices.Equal(g.alsoIn, w.alsoIn) {
				t.Errorf("entry %d = %+v, want %+v", i, g, w)
			}
		}
	}

	// One check covers: x appears once, as A's copy (the earliest subscribed)
	// at A's position, with AlsoIn [B] (not D, which the reader doesn't
	// subscribe to); B's later copy doesn't move it; z is C's earlier copy;
	// e1 and e2 aren't collapsed.
	t.Run("copies collapse to the earliest subscribed copy", func(t *testing.T) {
		assertEntries(t, entries(all), want)
	})

	// x has copies in other feeds, so it goes through the earlier-copy check.
	t.Run("leaves Summary and Content empty", func(t *testing.T) {
		for _, item := range all {
			if item.Article.ExternalID != "x" {
				continue
			}
			a := item.Article
			if a.Summary != "" || a.Content != "" {
				t.Errorf("got Summary %q, Content %q, want both empty", a.Summary, a.Content)
			}
			if a.Title != "Post X" || a.Excerpt != "excerpt" || a.CanonicalURL != postX || a.TimelineAt.IsZero() {
				t.Errorf("got %+v, want the other fields filled", a)
			}
			return
		}
		t.Fatal("no entry x")
	})

	t.Run("AlsoIn feeds are populated", func(t *testing.T) {
		for _, item := range all {
			for _, f := range item.AlsoIn {
				if f.Url == "" || f.Title != "Old title" {
					t.Errorf("article %s AlsoIn feed = %+v", item.Article.ExternalID, f)
				}
			}
		}
	})

	t.Run("pages over collapsed entries have no overlap or gap", func(t *testing.T) {
		var got []rss.ArticleWithFeed
		cursor := rss.ArticleCursor{}
		for range 10 {
			page, err := articles.ListTimeline(ctx, reader, rss.TimelineFilter{}, cursor, 1)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			if len(page) > 1 {
				t.Fatalf("page has %d rows, want at most 1", len(page))
			}
			if len(page) == 0 {
				break
			}
			got = append(got, page...)
			last := page[0].Article
			cursor = rss.ArticleCursor{At: last.TimelineAt, ID: last.ID}
		}
		assertEntries(t, entries(got), want)
	})

	t.Run("a user subscribed only to B sees B's copy without AlsoIn", func(t *testing.T) {
		got, err := articles.ListTimeline(ctx, bOnly, rss.TimelineFilter{}, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		assertEntries(t, entries(got), []entry{{"x", feedB.ID, at(3 * time.Hour), nil}})
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineFillsPagePastCopies(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	feedA, feedB := newTestFeed(t, db), newTestFeed(t, db)
	reader := newTestUser(t, db)
	for _, f := range []rss.Feed{feedA, feedB} {
		if err := subs.Subscribe(ctx, reader, f.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	// B has b1…b8 at 1…8 h. A has later copies of them, a11…a18 at 11…18 h,
	// so A's newest entries all collapse, and a9 (no copy) at 0.5 h.
	nano := time.Now().UnixNano()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	type item struct {
		feed   rss.Feed
		ext    string
		url    string
		offset time.Duration
	}
	items := []item{{feedA, "a9", "", 30 * time.Minute}}
	for i := 1; i <= 8; i++ {
		url := fmt.Sprintf("https://example.com/fills-%d-%d", nano, i)
		items = append(items,
			item{feedB, fmt.Sprintf("b%d", i), url, time.Duration(i) * time.Hour},
			item{feedA, fmt.Sprintf("a%d", 10+i), url, time.Duration(10+i) * time.Hour})
	}
	for _, it := range items {
		a := rss.Article{ExternalID: it.ext, URL: it.url, CanonicalURL: it.url, Title: it.ext}
		if _, err := feeds.SaveFetch(ctx, it.feed, []rss.Article{a}, it.feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if _, err := db.Exec(`UPDATE articles SET timeline_at = $1 WHERE feed_id = $2 AND external_id = $3`, base.Add(it.offset), it.feed.ID, it.ext); err != nil {
			t.Fatalf("set timeline_at: %v", err)
		}
	}

	pages := func(t *testing.T, filter rss.TimelineFilter) [][]string {
		t.Helper()
		var got [][]string
		cursor := rss.ArticleCursor{}
		for range 10 {
			page, err := articles.ListTimeline(ctx, reader, filter, cursor, 5)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			if len(page) == 0 {
				break
			}
			var exts []string
			for _, it := range page {
				exts = append(exts, it.Article.ExternalID)
			}
			got = append(got, exts)
			last := page[len(page)-1].Article
			cursor = rss.ArticleCursor{At: last.TimelineAt, ID: last.ID}
		}
		return got
	}
	assertPages := func(t *testing.T, got, want [][]string) {
		t.Helper()
		if !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("got pages %v, want %v", got, want)
		}
	}

	t.Run("Home skips A's collapsed copies and reaches a9", func(t *testing.T) {
		assertPages(t, pages(t, rss.TimelineFilter{}), [][]string{
			{"b8", "b7", "b6", "b5", "b4"},
			{"b3", "b2", "b1", "a9"},
		})
	})

	t.Run("leaving out B shows A's copies at their own positions", func(t *testing.T) {
		assertPages(t, pages(t, rss.TimelineFilter{ExcludeFeedIDs: []string{feedB.ID}}), [][]string{
			{"a18", "a17", "a16", "a15", "a14"},
			{"a13", "a12", "a11", "a9"},
		})
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineSubscriptionNames(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	// Both users subscribe to A and B; only the renamer names them. A and B
	// carry copies of one post, so each entry has the other in AlsoIn.
	feedA, feedB := newTestFeed(t, db), newTestFeed(t, db)
	renamer, other := newTestUser(t, db), newTestUser(t, db)
	for _, user := range []string{renamer, other} {
		for _, f := range []rss.Feed{feedA, feedB} {
			if err := subs.Subscribe(ctx, user, f.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}
	for feedID, name := range map[string]string{feedA.ID: "Name for A", feedB.ID: "Name for B"} {
		if err := subs.Rename(ctx, renamer, feedID, name); err != nil {
			t.Fatalf("Rename: %v", err)
		}
	}

	post := fmt.Sprintf("https://example.com/post-%d", time.Now().UnixNano())
	for _, f := range []rss.Feed{feedA, feedB} {
		batch := []rss.Article{{ExternalID: "shared", CanonicalURL: post}, {ExternalID: "own"}}
		if _, err := feeds.SaveFetch(ctx, f, batch, f.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
	}

	names := map[string]map[string]string{
		renamer: {feedA.ID: "Name for A", feedB.ID: "Name for B"},
		other:   {feedA.ID: "Old title", feedB.ID: "Old title"},
	}
	for user, want := range names {
		items, err := articles.ListTimeline(ctx, user, rss.TimelineFilter{}, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		// Two "own" entries and one collapsed "shared" entry.
		if len(items) != 3 {
			t.Fatalf("got %d entries, want 3", len(items))
		}
		var alsoIn int
		for _, item := range items {
			if got := item.Feed.Title; got != want[item.Feed.ID] {
				t.Errorf("article %s: Feed.Title = %q, want %q", item.Article.ExternalID, got, want[item.Feed.ID])
			}
			for _, f := range item.AlsoIn {
				alsoIn++
				if f.Title != want[f.ID] {
					t.Errorf("AlsoIn feed %s: Title = %q, want %q", f.ID, f.Title, want[f.ID])
				}
			}
		}
		if alsoIn != 1 {
			t.Errorf("got %d AlsoIn feeds, want 1", alsoIn)
		}
	}
}

// enrichJobs returns the priority of each of feedID's articles' enrichment
// jobs, by external ID.
func enrichJobs(t *testing.T, db *sql.DB, feedID string) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT a.external_id, j.priority FROM enrich_jobs j
		JOIN articles a ON a.id = j.article_id WHERE a.feed_id = $1`, feedID)
	if err != nil {
		t.Fatalf("select enrich_jobs: %v", err)
	}
	defer rows.Close()
	jobs := map[string]int{}
	for rows.Next() {
		var id string
		var priority int
		if err := rows.Scan(&id, &priority); err != nil {
			t.Fatalf("scan enrich_jobs: %v", err)
		}
		jobs[id] = priority
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("enrich_jobs rows: %v", err)
	}
	return jobs
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchQueuesEnrichment(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	ctx := t.Context()
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("first save queues the 20 latest at priority 1", func(t *testing.T) {
		feed := newTestFeed(t, db)
		// Backlog, so each is placed at its published date: n29 is latest.
		items := make([]rss.Article, 30)
		for i := range items {
			items[i] = rss.Article{
				ExternalID:  fmt.Sprintf("n%02d", i),
				URL:         fmt.Sprintf("https://example.com/%d", i),
				Title:       "Title",
				Excerpt:     "Excerpt",
				PublishedAt: date.Add(time.Duration(i) * time.Hour),
			}
		}
		if _, err := feeds.SaveFetch(ctx, feed, items, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		jobs := enrichJobs(t, db, feed.ID)
		if len(jobs) != enrichFirstSave {
			t.Errorf("got %d jobs, want %d: %v", len(jobs), enrichFirstSave, jobs)
		}
		for i := range items {
			id := items[i].ExternalID
			priority, ok := jobs[id]
			if want := i >= 30-enrichFirstSave; ok != want {
				t.Errorf("article %s queued = %v, want %v", id, ok, want)
			}
			if ok && priority != 1 {
				t.Errorf("article %s priority = %d, want 1", id, priority)
			}
		}
	})

	t.Run("later saves", func(t *testing.T) {
		feed := newTestFeed(t, db)
		first := rss.Article{ExternalID: "old", URL: "https://example.com/old", Title: "Old"}
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{first}, feed.NextFetch); err != nil {
			t.Fatalf("first SaveFetch: %v", err)
		}
		if jobs := enrichJobs(t, db, feed.ID); len(jobs) != 1 {
			t.Fatalf("after first save: jobs = %v, want old only", jobs)
		}
		// The job of "old" is done.
		if _, err := db.Exec(`DELETE FROM enrich_jobs j USING articles a WHERE a.id = j.article_id AND a.feed_id = $1`, feed.ID); err != nil {
			t.Fatalf("delete jobs: %v", err)
		}

		updated := first
		updated.Title = "Old, edited"
		items := []rss.Article{
			updated,
			{ExternalID: "no-image", URL: "https://example.com/1", Title: "T", Excerpt: "E"},
			{ExternalID: "no-title", URL: "https://example.com/2", ImageURL: "https://example.com/i.jpg", Excerpt: "E"},
			{ExternalID: "no-excerpt", URL: "https://example.com/3", ImageURL: "https://example.com/i.jpg", Title: "T"},
			{ExternalID: "complete", URL: "https://example.com/4", ImageURL: "https://example.com/i.jpg", Title: "T", Excerpt: "E"},
			{ExternalID: "no-url", Title: "T"},
		}
		if _, err := feeds.SaveFetch(ctx, feed, items, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		want := map[string]int{"no-image": 0, "no-title": 0, "no-excerpt": 0}
		if jobs := enrichJobs(t, db, feed.ID); !maps.Equal(jobs, want) {
			t.Errorf("jobs = %v, want %v", jobs, want)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveFetchAfterNoAttempts(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	ctx := t.Context()

	// setFetched saves feed once, then sets when it was last fetched and
	// attempted, each that long before now.
	setFetched := func(t *testing.T, feed rss.Feed, fetchedAgo, attemptedAgo time.Duration) {
		t.Helper()
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: "first", Title: "First", PublishedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}, feed.NextFetch); err != nil {
			t.Fatalf("first SaveFetch: %v", err)
		}
		if _, err := db.Exec(`UPDATE feeds SET last_fetched_at = now() - $2 * interval '1 microsecond', last_attempt_at = now() - $3 * interval '1 microsecond' WHERE id = $1`,
			feed.ID, fetchedAgo.Microseconds(), attemptedAgo.Microseconds()); err != nil {
			t.Fatalf("set last_fetched_at, last_attempt_at: %v", err)
		}
	}

	// save saves the later items and returns the result and the saved
	// articles by external ID.
	save := func(t *testing.T, feed rss.Feed) (rss.FetchResult, time.Time, map[string]rss.Article) {
		t.Helper()
		now := dbNow(t, db).Truncate(time.Second)
		res, err := feeds.SaveFetch(ctx, feed, []rss.Article{
			{ExternalID: "days", Title: "Days", PublishedAt: now.Add(-48 * time.Hour)},
			{ExternalID: "hour", Title: "Hour", PublishedAt: now.Add(-time.Hour)},
			{ExternalID: "undated", Title: "Undated"},
		}, feed.NextFetch)
		if err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		got, err := articles.ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListByFeed: %v", err)
		}
		byID := make(map[string]rss.Article)
		for _, a := range got {
			byID[a.ExternalID] = a
		}
		return res, now, byID
	}

	assertFirstSave := func(t *testing.T, res rss.FetchResult, now time.Time, got map[string]rss.Article) {
		t.Helper()
		if res.Behind {
			t.Errorf("got %+v, want not Behind", res)
		}
		for _, id := range []string{"days", "hour", "undated"} {
			if !got[id].Backlog {
				t.Errorf("%s: Backlog = false, want true", id)
			}
		}
		for _, id := range []string{"days", "hour"} {
			if a := got[id]; !a.TimelineAt.Equal(a.PublishedAt) {
				t.Errorf("%s: got TimelineAt %v, want published %v", id, a.TimelineAt, a.PublishedAt)
			}
		}
		if u := got["undated"]; u.TimelineAt.After(now.Add(-24 * time.Hour)) {
			t.Errorf("undated: got TimelineAt %v, want at least a day before %v", u.TimelineAt, now)
		}
	}

	t.Run("no attempt for three days: placed as a first save", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setFetched(t, feed, 72*time.Hour, 72*time.Hour)
		res, now, got := save(t, feed)
		assertFirstSave(t, res, now, got)
	})

	t.Run("attempted exactly staleFeedAfter ago: placed as a first save", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setFetched(t, feed, staleFeedAfter, staleFeedAfter)
		res, now, got := save(t, feed)
		assertFirstSave(t, res, now, got)
	})

	t.Run("failing but attempted: news", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setFetched(t, feed, 72*time.Hour, time.Hour)
		_, now, got := save(t, feed)
		after := dbNow(t, db)
		for _, id := range []string{"days", "hour"} {
			a := got[id]
			if a.Backlog {
				t.Errorf("%s: Backlog = true, want false", id)
			}
			if a.TimelineAt.Before(now.Add(-rss.NewsWindow)) || a.TimelineAt.After(after) {
				t.Errorf("%s: got TimelineAt %v, want within the %v before %v", id, a.TimelineAt, rss.NewsWindow, after)
			}
		}
		if u := got["undated"]; u.Backlog || !u.TimelineAt.Equal(u.CreatedAt) {
			t.Errorf("undated: got Backlog %t, TimelineAt %v; want news at the save time %v", u.Backlog, u.TimelineAt, u.CreatedAt)
		}
	})

	t.Run("enrichment follows the first save's allowance", func(t *testing.T) {
		feed := newTestFeed(t, db)
		setFetched(t, feed, 72*time.Hour, 72*time.Hour)
		if _, err := db.Exec(`DELETE FROM enrich_jobs j USING articles a WHERE a.id = j.article_id AND a.feed_id = $1`, feed.ID); err != nil {
			t.Fatalf("delete jobs: %v", err)
		}
		date := dbNow(t, db).Add(-30 * 24 * time.Hour)
		items := make([]rss.Article, 30)
		for i := range items {
			items[i] = rss.Article{
				ExternalID:  fmt.Sprintf("n%02d", i),
				URL:         fmt.Sprintf("https://example.com/%d", i),
				Title:       "Title",
				Excerpt:     "Excerpt",
				PublishedAt: date.Add(time.Duration(i) * time.Hour),
			}
		}
		if _, err := feeds.SaveFetch(ctx, feed, items, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}

		jobs := enrichJobs(t, db, feed.ID)
		if len(jobs) > enrichFirstSave {
			t.Errorf("got %d jobs, want at most %d: %v", len(jobs), enrichFirstSave, jobs)
		}
		for id, priority := range jobs {
			if priority != enrichPriorityBacklog {
				t.Errorf("article %s priority = %d, want %d", id, priority, enrichPriorityBacklog)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestGetByIDReadsPageColumns(t *testing.T) {
	db := psqltest.NewDB(t)
	id := newTestArticles(t, db, 1)[0]
	if _, err := db.Exec(`UPDATE articles SET page_image_url = 'https://example.com/p.jpg', page_title = 'Page title', page_description = 'Page description' WHERE id = $1`, id); err != nil {
		t.Fatalf("update page columns: %v", err)
	}

	a, err := NewArticleRepository(db).GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if a.PageImageURL != "https://example.com/p.jpg" || a.PageTitle != "Page title" || a.PageDescription != "Page description" {
		t.Errorf("page columns = %q, %q, %q", a.PageImageURL, a.PageTitle, a.PageDescription)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineFilter(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	// The user subscribes to A, B, C and D. A is tagged T1; B and C are
	// tagged T2. x has copies in C (first to arrive) and A.
	user := newTestUser(t, db)
	ids := newTestSubscriptions(t, db, user, 4)
	feedOf := map[string]string{"A": ids[0], "B": ids[1], "C": ids[2], "D": ids[3]}
	nameOf := map[string]string{}
	for name, id := range feedOf {
		nameOf[id] = name
	}
	t1, t2 := mustCreateTag(t, tags, user, "T1"), mustCreateTag(t, tags, user, "T2")
	mustSetFeeds(t, tags, user, t1.ID, feedOf["A"])
	mustSetFeeds(t, tags, user, t2.ID, feedOf["B"], feedOf["C"])

	postX := fmt.Sprintf("https://example.com/filter-x-%d", time.Now().UnixNano())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		feed, ext, url string
		offset         time.Duration
	}{
		{"A", "a1", "", time.Hour},
		{"A", "a2", "", 2 * time.Hour},
		{"B", "b1", "", 3 * time.Hour},
		{"C", "c1", "", 4 * time.Hour},
		{"D", "d1", "", 5 * time.Hour},
		{"C", "x", postX, 6 * time.Hour},
		{"A", "x", postX, 7 * time.Hour},
	} {
		feed := rss.Feed{ID: feedOf[c.feed], NextFetch: time.Now().Add(time.Hour)}
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{{ExternalID: c.ext, CanonicalURL: c.url}}, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		if _, err := db.Exec(`UPDATE articles SET timeline_at = $1 WHERE feed_id = $2 AND external_id = $3`, base.Add(c.offset), feed.ID, c.ext); err != nil {
			t.Fatalf("set timeline_at: %v", err)
		}
	}

	// entries lists filter's timeline as "ext@feed", newest first.
	entries := func(t *testing.T, filter rss.TimelineFilter) []string {
		t.Helper()
		items, err := articles.ListTimeline(ctx, user, filter, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		got := []string{}
		for _, item := range items {
			got = append(got, item.Article.ExternalID+"@"+nameOf[item.Feed.ID])
		}
		return got
	}

	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   []string
	}{
		{"zero filter shows every subscription", rss.TimelineFilter{},
			[]string{"x@C", "d1@D", "c1@C", "b1@B", "a2@A", "a1@A"}},
		{"only one tag", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{t1.ID}},
			[]string{"x@A", "a2@A", "a1@A"}},
		{"one tag plus one feed", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{t1.ID}, IncludeFeedIDs: []string{feedOf["D"]}},
			[]string{"x@A", "d1@D", "a2@A", "a1@A"}},
		{"leave out a tag and a feed", rss.TimelineFilter{ExcludeTagIDs: []string{t2.ID}, ExcludeFeedIDs: []string{feedOf["D"]}},
			[]string{"x@A", "a2@A", "a1@A"}},
		{"leave out wins over include", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{t2.ID}, IncludeFeedIDs: []string{feedOf["A"]}, ExcludeFeedIDs: []string{feedOf["C"]}},
			[]string{"x@A", "b1@B", "a2@A", "a1@A"}},
		{"leave out a tag that's included", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{t1.ID, t2.ID}, ExcludeTagIDs: []string{t2.ID}},
			[]string{"x@A", "a2@A", "a1@A"}},
		{"only included with nothing included shows nothing", rss.TimelineFilter{OnlyIncluded: true},
			[]string{}},
		// x's copy in C arrived first, but C is left out, so A's copy shows.
		{"copies collapse among the timeline's feeds only", rss.TimelineFilter{ExcludeFeedIDs: []string{feedOf["C"]}},
			[]string{"x@A", "d1@D", "b1@B", "a2@A", "a1@A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := entries(t, tt.filter); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("also in still lists every other subscribed copy", func(t *testing.T) {
		items, err := articles.ListTimeline(ctx, user, rss.TimelineFilter{ExcludeFeedIDs: []string{feedOf["C"]}}, rss.ArticleCursor{}, 1)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		if len(items) != 1 || len(items[0].AlsoIn) != 1 || items[0].AlsoIn[0].ID != feedOf["C"] {
			t.Errorf("got %+v, want x from A, also in C", items)
		}
	})

	t.Run("paging with a filter", func(t *testing.T) {
		filter := rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{t1.ID}, IncludeFeedIDs: []string{feedOf["D"]}}
		var got []string
		cursor := rss.ArticleCursor{}
		for range 10 {
			page, err := articles.ListTimeline(ctx, user, filter, cursor, 1)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			if len(page) == 0 {
				break
			}
			got = append(got, page[0].Article.ExternalID+"@"+nameOf[page[0].Feed.ID])
			cursor = rss.ArticleCursor{At: page[0].Article.TimelineAt, ID: page[0].Article.ID}
		}
		if want := []string{"x@A", "d1@D", "a2@A", "a1@A"}; !slices.Equal(got, want) {
			t.Errorf("paged %v, want %v", got, want)
		}
	})
}

// newConditionArticles subscribes userID to A and C, which post 1.4 times a
// week, and B, which posts 5 times; A and B are tagged T. Each feed has an
// audio article ("<feed>-audio", the newest) and a text-only one
// ("<feed>-text"); B's are newest, then A's, then C's. It returns the
// articles' feeds by article ID, the article IDs by external ID, and T.
func newConditionArticles(t *testing.T, db *sql.DB, userID string) (feedName, articleID map[string]string, tag rss.Tag) {
	t.Helper()
	ctx := t.Context()
	feeds := NewFeedRepository(db)
	tags := NewTagRepository(db)

	ids := newTestSubscriptions(t, db, userID, 3)
	feedOf := map[string]string{"A": ids[0], "B": ids[1], "C": ids[2]}
	tag = mustCreateTag(t, tags, userID, "T")
	mustSetFeeds(t, tags, userID, tag.ID, feedOf["A"], feedOf["B"])

	feedName = map[string]string{}
	articleID = map[string]string{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range []struct {
		feed         string
		postsPerWeek float64
	}{{"C", 1.4}, {"A", 1.4}, {"B", 5}} {
		feed := rss.Feed{ID: feedOf[c.feed], NextFetch: time.Now().Add(time.Hour)}
		items := []rss.Article{
			{ExternalID: c.feed + "-text"},
			{ExternalID: c.feed + "-audio", MediaType: "audio/mpeg"},
		}
		if _, err := feeds.SaveFetch(ctx, feed, items, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		// SaveFetch computes posts_per_week from the articles' dates; set it
		// after.
		if _, err := db.Exec(`UPDATE feeds SET posts_per_week = $1 WHERE id = $2`, c.postsPerWeek, feed.ID); err != nil {
			t.Fatalf("set posts_per_week: %v", err)
		}
		for j, item := range items {
			var id string
			err := db.QueryRow(`UPDATE articles SET timeline_at = $1 WHERE feed_id = $2 AND external_id = $3 RETURNING id`,
				base.Add(time.Duration(2*i+j)*time.Hour), feed.ID, item.ExternalID).Scan(&id)
			if err != nil {
				t.Fatalf("set timeline_at: %v", err)
			}
			feedName[id] = c.feed
			articleID[item.ExternalID] = id
		}
	}
	return feedName, articleID, tag
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineConditions(t *testing.T) {
	db := psqltest.NewDB(t)
	articles := NewArticleRepository(db)
	user := newTestUser(t, db)
	_, _, tag := newConditionArticles(t, db, user)

	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   []string
	}{
		{"any posting rate", rss.TimelineFilter{},
			[]string{"B-audio", "B-text", "A-audio", "A-text", "C-audio", "C-text"}},
		{"at most 2 posts a week leaves out the busy feed", rss.TimelineFilter{MaxPostsPerWeek: 2},
			[]string{"A-audio", "A-text", "C-audio", "C-text"}},
		{"at most 5 posts a week includes a feed at exactly 5", rss.TimelineFilter{MaxPostsPerWeek: 5},
			[]string{"B-audio", "B-text", "A-audio", "A-text", "C-audio", "C-text"}},
		{"at most 2 posts a week with a tag", rss.TimelineFilter{MaxPostsPerWeek: 2, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"A-audio", "A-text"}},
		{"audio or video", rss.TimelineFilter{MediaOnly: true},
			[]string{"B-audio", "A-audio", "C-audio"}},
		{"audio or video with a tag", rss.TimelineFilter{MediaOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"B-audio", "A-audio"}},
		{"both", rss.TimelineFilter{MediaOnly: true, MaxPostsPerWeek: 2, ExcludeTagIDs: []string{tag.ID}},
			[]string{"C-audio"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := articles.ListTimeline(t.Context(), user, tt.filter, rss.ArticleCursor{}, 100)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			got := []string{}
			for _, item := range items {
				got = append(got, item.Article.ExternalID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// setConditionTexts gives newConditionArticles' articles titles, page titles
// and excerpts for keyword conditions: A-text, B-text (in its page title)
// and C-audio (in its excerpt) mention AI or LLM; B-audio (in its title) and
// C-audio (in its excerpt) mention "sponsored"; A-audio and C-text mention
// neither, only words containing them.
func setConditionTexts(t *testing.T, db *sql.DB, articleID map[string]string) {
	t.Helper()
	for ext, text := range map[string][3]string{
		"A-text":  {"New AI model", "", ""},
		"A-audio": {"She said nothing", "", "AIDS research"},
		"B-text":  {"Weekly news", "LLM roundup", ""},
		"B-audio": {"Sponsored: AI deals", "", ""},
		"C-audio": {"Episode 12", "", "All about AI. Sponsored by Example."},
		"C-text":  {"OpenAI raises", "", "unsponsored"},
	} {
		if _, err := db.Exec(`UPDATE articles SET title = $1, page_title = $2, excerpt = $3 WHERE id = $4`,
			text[0], text[1], text[2], articleID[ext]); err != nil {
			t.Fatalf("set texts: %v", err)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestListTimelineWords(t *testing.T) {
	db := psqltest.NewDB(t)
	articles := NewArticleRepository(db)
	user := newTestUser(t, db)
	_, articleID, tag := newConditionArticles(t, db, user)
	setConditionTexts(t, db, articleID)

	ai := []string{"AI", "LLM"}
	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   []string
	}{
		{"must mention keeps only matches", rss.TimelineFilter{IncludeWords: ai},
			[]string{"B-audio", "B-text", "A-text", "C-audio"}},
		{"a page title match counts", rss.TimelineFilter{IncludeWords: []string{"llm"}},
			[]string{"B-text"}},
		{"an excerpt match counts", rss.TimelineFilter{IncludeWords: []string{"example"}},
			[]string{"C-audio"}},
		{"leave out removes matches", rss.TimelineFilter{ExcludeWords: []string{"sponsored"}},
			[]string{"B-text", "A-audio", "A-text", "C-text"}},
		{"leave out wins over must mention", rss.TimelineFilter{IncludeWords: ai, ExcludeWords: []string{"sponsored"}},
			[]string{"B-text", "A-text"}},
		{"must mention with a tag", rss.TimelineFilter{IncludeWords: ai, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"B-audio", "B-text", "A-text"}},
		{"leave out with a tag", rss.TimelineFilter{ExcludeWords: []string{"sponsored"}, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"B-text", "A-audio", "A-text"}},
		{"no matches", rss.TimelineFilter{IncludeWords: []string{"C++"}},
			[]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := articles.ListTimeline(t.Context(), user, tt.filter, rss.ArticleCursor{}, 100)
			if err != nil {
				t.Fatalf("ListTimeline: %v", err)
			}
			got := []string{}
			for _, item := range items {
				got = append(got, item.Article.ExternalID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestListShared(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	articles := NewArticleRepository(db)
	subs := NewSubscriptionRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	// The owner subscribes to A (renamed, tagged T) and B. A has a linked
	// article, one mentioning AI, and one without a link; B one linked one.
	owner := newTestUser(t, db)
	ids := newTestSubscriptions(t, db, owner, 2)
	feedA, feedB := ids[0], ids[1]
	if err := subs.Rename(ctx, owner, feedA, "My name for A"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	tag := mustCreateTag(t, tags, owner, "T")
	mustSetFeeds(t, tags, owner, tag.ID, feedA)

	saves := map[string][]rss.Article{
		feedA: {
			{ExternalID: "a-link", URL: "https://example.com/a-link", Title: "A link"},
			{ExternalID: "a-ai", URL: "https://example.com/a-ai", Title: "All about AI"},
			{ExternalID: "a-nolink", Title: "No link"},
		},
		feedB: {{ExternalID: "b-link", URL: "https://example.com/b-link", Title: "B link"}},
	}
	articleOf := map[string]string{}
	for feedID, batch := range saves {
		next := time.Now().Add(time.Hour)
		if _, err := feeds.SaveFetch(ctx, rss.Feed{ID: feedID, NextFetch: next}, batch, next); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		for _, a := range batch {
			var id string
			if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feedID, a.ExternalID).Scan(&id); err != nil {
				t.Fatalf("select article: %v", err)
			}
			articleOf[a.ExternalID] = id
		}
	}
	externalID := map[string]string{}
	for ext, id := range articleOf {
		externalID[id] = ext
	}

	list := func(t *testing.T, filter rss.TimelineFilter, limit int) []rss.SharedArticle {
		t.Helper()
		items, err := articles.ListShared(ctx, owner, filter, limit)
		if err != nil {
			t.Fatalf("ListShared: %v", err)
		}
		return items
	}
	names := func(items []rss.SharedArticle) []string {
		got := []string{}
		for _, item := range items {
			got = append(got, externalID[item.Article.ID])
		}
		return got
	}

	t.Run("linked articles with each feed's own title", func(t *testing.T) {
		items := list(t, rss.TimelineFilter{}, 50)
		if got, want := slices.Sorted(slices.Values(names(items))), []string{"a-ai", "a-link", "b-link"}; !slices.Equal(got, want) {
			t.Errorf("articles = %v, want %v (none without a link)", got, want)
		}
		for _, item := range items {
			if item.Feed.Title != "Old title" {
				t.Errorf("%s: Feed.Title = %q, want the feed's own title", externalID[item.Article.ID], item.Feed.Title)
			}
			if !item.StarredAt.IsZero() {
				t.Errorf("%s: StarredAt = %v, want zero", externalID[item.Article.ID], item.StarredAt)
			}
		}
	})

	t.Run("the filter applies", func(t *testing.T) {
		got := slices.Sorted(slices.Values(names(list(t, rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}}, 50))))
		if want := []string{"a-ai", "a-link"}; !slices.Equal(got, want) {
			t.Errorf("tag T = %v, want %v", got, want)
		}
		if got, want := names(list(t, rss.TimelineFilter{IncludeWords: []string{"AI"}}, 50)), []string{"a-ai"}; !slices.Equal(got, want) {
			t.Errorf("mentions AI = %v, want %v", got, want)
		}
	})

	t.Run("at most limit", func(t *testing.T) {
		if got := list(t, rss.TimelineFilter{}, 2); len(got) != 2 {
			t.Errorf("got %d articles, want 2", len(got))
		}
	})

	t.Run("starred only, newest starred first", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		for i, ext := range []string{"b-link", "a-link", "a-nolink"} {
			if _, err := db.Exec(`INSERT INTO stars (user_id, article_id, starred_at) VALUES ($1, $2, $3)`,
				owner, articleOf[ext], base.Add(time.Duration(i)*time.Hour)); err != nil {
				t.Fatalf("insert star: %v", err)
			}
		}
		items := list(t, rss.TimelineFilter{StarredOnly: true}, 50)
		if got, want := names(items), []string{"a-link", "b-link"}; !slices.Equal(got, want) {
			t.Fatalf("starred = %v, want %v", got, want)
		}
		if !items[0].StarredAt.Equal(base.Add(time.Hour)) {
			t.Errorf("StarredAt = %v, want %v", items[0].StarredAt, base.Add(time.Hour))
		}
		if items[0].Feed.Title != "Old title" {
			t.Errorf("Feed.Title = %q, want the feed's own title", items[0].Feed.Title)
		}
	})
}
