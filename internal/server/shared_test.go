package server

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/rss"
)

const (
	sharedArticleID  = "99999999-9999-4999-8999-999999999991"
	sharedArticle2ID = "99999999-9999-4999-8999-999999999992"
)

// sharedStores returns stores where testShareToken is user-1's shared
// timeline tl (its ShareToken set), and ListShared returns items for it,
// counting its calls.
func sharedStores(tl rss.Timeline, items []rss.SharedArticle) (*fakeTimelineStore, *fakeArticleStore, *int) {
	tl.ID = testTimelineID
	tl.ShareToken = testShareToken
	var listCalls int
	timelines := &fakeTimelineStore{getSharedFn: func(ctx context.Context, token string) (rss.Timeline, string, error) {
		if token != testShareToken {
			return rss.Timeline{}, "", rss.ErrNoRecord
		}
		return tl, "user-1", nil
	}}
	articles := &fakeArticleStore{listSharedFn: func(ctx context.Context, ownerID string, filter rss.TimelineFilter, limit int) ([]rss.SharedArticle, error) {
		listCalls++
		if ownerID != "user-1" || filter.StarredOnly != tl.Filter.StarredOnly || limit != sharedFeedLimit {
			return nil, errors.New("wrong arguments")
		}
		return items, nil
	}}
	return timelines, articles, &listCalls
}

func newSharedTestServer(t *testing.T, timelines *fakeTimelineStore, articles *fakeArticleStore) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example"},
		Services{TimelineService: timelines, ArticleService: articles})
}

// getShared requests target through the router, logged out, with header.
func getShared(t *testing.T, s *Server, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, req)
	if c := rr.Header().Get("Set-Cookie"); c != "" {
		t.Errorf("GET %s sets a cookie: %s", target, c)
	}
	return rr
}

// sharedItems are a timeline's articles: one with a canonical URL, a
// published date and an excerpt from a renamed feed (its own title is
// still Feed One), one with none of those.
func sharedItems() []rss.SharedArticle {
	published := time.Date(2026, 3, 1, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	return []rss.SharedArticle{
		{
			Article: rss.Article{
				ID: sharedArticleID, URL: "https://one.example/posts/1", CanonicalURL: "https://one.example/posts/1",
				Title: "First <post>", Excerpt: "An excerpt & more", PublishedAt: published,
				TimelineAt: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
			},
			Feed:      rss.Feed{Url: "https://one.example/feed.xml", SiteUrl: "https://one.example/", Title: "Feed One"},
			StarredAt: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			Article: rss.Article{
				ID: sharedArticle2ID, URL: "https://two.example/p", Title: "Second",
				TimelineAt: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
			},
			Feed:      rss.Feed{Url: "https://two.example/feed.xml"},
			StarredAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		},
	}
}

func TestSharedTimelinePage(t *testing.T) {
	timelines, articles, listCalls := sharedStores(rss.Timeline{Name: "Ada's picks", Description: "Good reads"}, sharedItems())
	s := newSharedTestServer(t, timelines, articles)

	rr := getShared(t, s, "/shared/"+testShareToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	assertBodyContains(t, rr.Body.String(),
		`<meta name="robots" content="noindex" />`,
		`<title>Ada&#39;s picks - rss</title>`,
		`<link rel="alternate" type="application/atom+xml" title="Ada&#39;s picks" href="/shared/`+testShareToken+`/feed.atom" />`,
		"<h1>Ada&#39;s picks</h1>",
		`<p class="shared-timeline-description">Good reads</p>`,
		"A timeline shared from rss.",
		"paste this page's address, or the feed link, into your reader",
		`<input type="text" id="feed-url" value="https://rss.example/shared/`+testShareToken+`/feed.atom" readonly />`,
		`href="/shared/`+testShareToken+`/feed.atom">`,
		"Open the feed</a>",
		`<a href="/">rss</a>`,
	)
	if got := rr.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}
	if *listCalls != 0 {
		t.Errorf("the page read the timeline's articles %d times; only the feed does", *listCalls)
	}

	t.Run("no description", func(t *testing.T) {
		timelines, articles, _ := sharedStores(rss.Timeline{Name: "Picks"}, nil)
		s := newSharedTestServer(t, timelines, articles)
		if body := getShared(t, s, "/shared/"+testShareToken, nil).Body.String(); strings.Contains(body, "shared-timeline-description") {
			t.Errorf("shows an empty description: %s", body)
		}
	})
}

func TestSharedFeed(t *testing.T) {
	feedPath := "/shared/" + testShareToken + "/feed.atom"

	// get returns the feed of tl with items, checking its headers.
	get := func(t *testing.T, tl rss.Timeline, items []rss.SharedArticle) (*httptest.ResponseRecorder, atomFeed) {
		t.Helper()
		timelines, articles, _ := sharedStores(tl, items)
		rr := getShared(t, newSharedTestServer(t, timelines, articles), feedPath, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		for name, want := range map[string]string{
			"Content-Type":  "application/atom+xml; charset=utf-8",
			"Cache-Control": "public, max-age=900",
			"X-Robots-Tag":  "noindex",
		} {
			if got := rr.Header().Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		var feed atomFeed
		if err := xml.Unmarshal(rr.Body.Bytes(), &feed); err != nil {
			t.Fatalf("xml.Unmarshal: %v", err)
		}
		return rr, feed
	}

	updated := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
	tl := rss.Timeline{Name: "Ada's picks", Description: "Good reads", UpdatedAt: updated}

	t.Run("our parser reads it", func(t *testing.T) {
		rr, _ := get(t, tl, sharedItems())
		u, _ := url.Parse("https://rss.example" + feedPath)
		parsed, err := feedparse.Parse(u, rr.Body.Bytes())
		if err != nil {
			t.Fatalf("feedparse.Parse: %v\n%s", err, rr.Body.String())
		}
		if parsed.Format != "atom" || parsed.Title != "Ada's picks" || parsed.Description != "Good reads" ||
			parsed.SiteURL != "https://rss.example/shared/"+testShareToken {
			t.Errorf("meta = %+v", parsed.Meta)
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("got %d items, want 2", len(parsed.Items))
		}
		first, second := parsed.Items[0], parsed.Items[1]
		if first.ID != "https://one.example/posts/1" || first.URL != "https://one.example/posts/1" || first.Title != "First <post>" {
			t.Errorf("first item = %+v", first)
		}
		if !first.Published.Equal(time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)) {
			t.Errorf("first Published = %v", first.Published)
		}
		if second.ID != "urn:uuid:"+sharedArticle2ID || second.URL != "https://two.example/p" || second.Title != "Second" {
			t.Errorf("second item = %+v", second)
		}
	})

	t.Run("document", func(t *testing.T) {
		rr, feed := get(t, tl, sharedItems())
		if !strings.HasPrefix(rr.Body.String(), `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<feed xmlns="http://www.w3.org/2005/Atom">`) {
			t.Errorf("document starts %.120q", rr.Body.String())
		}
		page := "https://rss.example/shared/" + testShareToken
		if feed.ID != page || feed.Title != plainText("Ada's picks") || feed.Subtitle == nil || *feed.Subtitle != plainText("Good reads") ||
			feed.Author.Name != "Ada's picks" || feed.Generator != "rss" {
			t.Errorf("feed = %+v", feed)
		}
		wantLinks := []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: page + "/feed.atom"},
			{Rel: "alternate", Type: "text/html", Href: page},
		}
		if len(feed.Links) != 2 || feed.Links[0] != wantLinks[0] || feed.Links[1] != wantLinks[1] {
			t.Errorf("links = %+v, want %+v", feed.Links, wantLinks)
		}
		// The newest entry's <updated>: the second's position, after the
		// first's published date.
		if feed.Updated != "2026-03-05T00:00:00Z" {
			t.Errorf("updated = %q", feed.Updated)
		}
		if got := rr.Header().Get("Last-Modified"); got != "Thu, 05 Mar 2026 00:00:00 GMT" {
			t.Errorf("Last-Modified = %q", got)
		}

		if len(feed.Entries) != 2 {
			t.Fatalf("got %d entries", len(feed.Entries))
		}
		first, second := feed.Entries[0], feed.Entries[1]
		if first.Published != "2026-03-01T08:00:00Z" || first.Updated != "2026-03-01T08:00:00Z" {
			t.Errorf("first published %q, updated %q", first.Published, first.Updated)
		}
		if first.Summary == nil || *first.Summary != plainText("An excerpt & more") {
			t.Errorf("first summary = %+v", first.Summary)
		}
		if first.Title != plainText("First <post>") || len(first.Links) != 1 || first.Links[0] != (atomLink{Rel: "alternate", Href: "https://one.example/posts/1"}) {
			t.Errorf("first = %+v", first)
		}
		wantSource := atomSource{ID: "https://one.example/feed.xml", Title: plainText("Feed One"),
			Links: []atomLink{{Rel: "alternate", Href: "https://one.example/"}}}
		if first.Source.ID != wantSource.ID || first.Source.Title != wantSource.Title || len(first.Source.Links) != 1 || first.Source.Links[0] != wantSource.Links[0] {
			t.Errorf("first source = %+v, want %+v", first.Source, wantSource)
		}

		// No published date, excerpt, feed title or site URL.
		if second.Published != "" || second.Summary != nil || second.Updated != "2026-03-05T00:00:00Z" {
			t.Errorf("second = %+v", second)
		}
		if second.Source.Title != plainText("https://two.example/feed.xml") || second.Source.Links[0].Href != "https://two.example/feed.xml" {
			t.Errorf("second source = %+v, want the feed URL for its title and link", second.Source)
		}
		if body := rr.Body.String(); strings.Contains(body, "<published></published>") || strings.Count(body, "<summary") != 1 {
			t.Errorf("document has an empty <published> or <summary>: %s", body)
		}
	})

	t.Run("starred-only entries are updated when starred", func(t *testing.T) {
		starred := tl
		starred.Filter.StarredOnly = true
		_, feed := get(t, starred, sharedItems())
		if feed.Entries[0].Updated != "2026-04-01T12:00:00Z" || feed.Entries[1].Updated != "2026-03-20T12:00:00Z" {
			t.Errorf("updated = %q, %q; want the star times", feed.Entries[0].Updated, feed.Entries[1].Updated)
		}
		if feed.Updated != "2026-04-01T12:00:00Z" {
			t.Errorf("feed updated = %q", feed.Updated)
		}
	})

	t.Run("no description or entries", func(t *testing.T) {
		rr, feed := get(t, rss.Timeline{Name: "Empty", UpdatedAt: updated}, nil)
		if feed.Subtitle != nil || strings.Contains(rr.Body.String(), "<subtitle") {
			t.Errorf("has a subtitle: %s", rr.Body.String())
		}
		if feed.Updated != "2026-01-10T08:00:00Z" || len(feed.Entries) != 0 {
			t.Errorf("updated = %q, entries %d; want the timeline's UpdatedAt and none", feed.Updated, len(feed.Entries))
		}
	})

	t.Run("ETag", func(t *testing.T) {
		timelines, articles, _ := sharedStores(tl, sharedItems())
		s := newSharedTestServer(t, timelines, articles)
		etag := getShared(t, s, feedPath, nil).Header().Get("ETag")
		if len(etag) != 34 || etag[0] != '"' || etag[33] != '"' {
			t.Fatalf("ETag = %q, want a quoted 32-character hash", etag)
		}

		for _, inm := range []string{etag, `"other", ` + etag, "W/" + etag} {
			rr := getShared(t, s, feedPath, http.Header{"If-None-Match": {inm}})
			if rr.Code != http.StatusNotModified || rr.Body.Len() != 0 {
				t.Errorf("If-None-Match %s: status %d, %d bytes; want 304 and none", inm, rr.Code, rr.Body.Len())
			}
		}
		if rr := getShared(t, s, feedPath, http.Header{"If-None-Match": {`"other"`}}); rr.Code != http.StatusOK {
			t.Errorf("another ETag: status %d, want 200", rr.Code)
		}

		renamed := tl
		renamed.Name = "Renamed"
		timelines, articles, _ = sharedStores(renamed, sharedItems())
		s = newSharedTestServer(t, timelines, articles)
		rr := getShared(t, s, feedPath, http.Header{"If-None-Match": {etag}})
		if rr.Code != http.StatusOK || rr.Header().Get("ETag") == etag {
			t.Errorf("changed timeline: status %d, ETag %q; want 200 and a new ETag", rr.Code, rr.Header().Get("ETag"))
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		timelines, _, _ := sharedStores(tl, nil)
		articles := &fakeArticleStore{listSharedFn: func(context.Context, string, rss.TimelineFilter, int) ([]rss.SharedArticle, error) {
			return nil, errors.New("boom")
		}}
		if rr := getShared(t, newSharedTestServer(t, timelines, articles), feedPath, nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func TestSharedNotFound(t *testing.T) {
	timelines, articles, listCalls := sharedStores(rss.Timeline{Name: "Picks"}, nil)
	s := newSharedTestServer(t, timelines, articles)

	unknown := strings.Repeat("x", 43)
	for _, target := range []string{
		"/shared/" + unknown,
		"/shared/" + unknown + "/feed.atom",
		"/shared/short",
		"/shared/short/feed.atom",
		"/shared/" + testShareToken + "!",
	} {
		t.Run(target, func(t *testing.T) {
			rr := getShared(t, s, target, nil)
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want the HTML 404", ct)
			}
		})
	}
	if *listCalls != 0 {
		t.Errorf("ListShared called %d times for unknown tokens", *listCalls)
	}
}
