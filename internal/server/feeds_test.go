package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

func assertNotFoundHTML(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), "Page not found") {
		t.Errorf("body does not contain %q: %s", "Page not found", rr.Body.String())
	}
}

func TestFeedHandlerMalformedID(t *testing.T) {
	s := newTestServer(t)

	for _, id := range []string{"abc", " "} {
		req := httptest.NewRequest(http.MethodGet, "/feeds/x", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rr := httptest.NewRecorder()

		s.feedHandler(rr, req)

		t.Run(id, func(t *testing.T) { assertNotFoundHTML(t, rr) })
	}
}

const testFeedID = "11111111-1111-4111-8111-111111111111"

func serveFeed(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	return serveFeedQuery(t, s, id, "")
}

// serveFeedQuery serves the feed page for id with query appended to its URL.
func serveFeedQuery(t *testing.T, s *Server, id, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+id+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.feedHandler)).ServeHTTP(rr, req)
	return rr
}

func feedServer(t *testing.T, siteErr error) *Server {
	t.Helper()
	return feedServerWithArticles(t, siteErr, &fakeArticleStore{})
}

func feedServerWithArticles(t *testing.T, siteErr error, articles ArticleStore) *Server {
	t.Helper()
	return newTestServerWith(t, Services{
		SubscriptionService: &fakeSubscriptionStore{},
		ReadService:         &fakeReadStore{},
		SaveService:         &fakeSaveStore{},
		ArticleService:      articles,
		FeedService: &fakeFeedStore{
			getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
				return rss.Feed{
					ID:          id,
					Title:       "Example Feed Title",
					Description: "A feed about examples",
					Url:         "https://example.com/feed.xml",
					SiteID:      testSiteID,
				}, nil
			},
		},
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			if siteErr != nil {
				return rss.Site{}, siteErr
			}
			return rss.Site{ID: id, Host: "example.com", Title: "Example Site"}, nil
		}},
	})
}

func TestFeedHandlerSuccess(t *testing.T) {
	rr := serveFeed(t, feedServer(t, nil), testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Example Feed Title",
		"A feed about examples",
		"https://example.com/feed.xml",
		`href="/sites/` + testSiteID + `"`,
		"Example Site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestFeedHandlerListsArticles(t *testing.T) {
	var gotFeedID string
	var gotLimit int
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			gotFeedID, gotLimit = feedID, limit
			return []rss.Article{
				{
					ID:          "art-1",
					Title:       "A <b>bold</b> title",
					URL:         "https://example.com/a",
					PublishedAt: time.Date(2024, time.March, 2, 10, 0, 0, 0, time.UTC),
					Excerpt:     "First excerpt",
					Authors:     []string{"Ada", "Grace"},
					Summary:     "<script>SUMMARY-MARKER</script>",
					Content:     "<script>SUMMARY-MARKER</script>",
				},
				{ID: "art-2", URL: "https://example.com/b"},
				{ID: "art-3", Excerpt: "Microblog post start"},
				{ID: "art-4", Title: "No link title"},
			}, nil
		},
	})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotFeedID != testFeedID || gotLimit != 31 {
		t.Errorf("ListByFeed(%q, %d), want (%q, 31)", gotFeedID, gotLimit, testFeedID)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<article class="article-row" id="article-art-1">`,
		`<article class="article-row" id="article-art-2">`,
		`<a href="/articles/art-1">A &lt;b&gt;bold&lt;/b&gt; title</a>`,
		`<a href="/articles/art-2">(untitled)</a>`,
		`<a href="/articles/art-3">Microblog post start</a>`,
		`<a href="/articles/art-4">No link title</a>`,
		`<p class="article-excerpt">First excerpt</p>`,
		`<p class="article-source-line"><span class="article-author">Ada and Grace</span><span aria-hidden="true">·</span><a class="article-source" href="https://example.com/a"`,
		`<time data-local datetime="2024-03-02T10:00:00Z">2 Mar 2024 10:00 UTC</time>`,
		`<script src="/static/js/app.js?v=` + jsVersion + `" defer></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	if n := strings.Count(body, `class="article-row"`); n != 4 {
		t.Errorf("article rows = %d, want 4", n)
	}
	if strings.Contains(body, "<b>bold</b>") {
		t.Errorf("title was not escaped: %s", body)
	}
	if strings.Contains(body, "SUMMARY-MARKER") {
		t.Errorf("summary or content was rendered: %s", body)
	}
	if n := strings.Count(body, "<time"); n != 1 {
		t.Errorf("<time elements = %d, want 1", n)
	}
	if strings.Contains(body, `<a href="https://example.com/`) {
		t.Errorf("titles link to the original instead of the article page: %s", body)
	}
	// On the feed's own page the rows don't link back to the feed.
	if strings.Count(body, `href="/feeds/`) != 0 {
		t.Errorf("rows link to the feed on the feed's own page: %s", body)
	}
}

func TestFeedHandlerDateOnlyRows(t *testing.T) {
	// Midnight UTC in a zone behind UTC, as a scan can return it: the row
	// still shows the UTC date.
	day := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC).In(time.FixedZone("EDT", -4*60*60))
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			return []rss.Article{
				{ID: "art-1", Title: "Date only", PublishedAt: day, PublishedDateOnly: true},
				{ID: "art-2", Title: "Timed", PublishedAt: time.Date(2024, time.March, 2, 10, 0, 0, 0, time.UTC)},
			}, nil
		},
	})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		`<time datetime="2026-10-09">9 Oct 2026</time>`,
		`<time data-local datetime="2024-03-02T10:00:00Z">2 Mar 2024 10:00 UTC</time>`,
	)
	if n := strings.Count(body, "data-local"); n != 1 {
		t.Errorf("data-local attributes = %d, want 1 (the timed row only)", n)
	}
	if strings.Contains(body, "00:00 UTC") {
		t.Errorf("date-only row shows a time: %s", body)
	}
}

func TestFeedHandlerRowTimeInOtherZone(t *testing.T) {
	at := time.Date(2024, time.March, 2, 6, 0, 0, 0, time.FixedZone("EST", -5*60*60)) // 11:00 UTC
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return []rss.Article{{ID: "art-1", Title: "Timed", PublishedAt: at}}, nil
		},
	})

	body := serveFeed(t, s, testFeedID).Body.String()

	assertBodyContains(t, body, `<time data-local datetime="2024-03-02T11:00:00Z">2 Mar 2024 11:00 UTC</time>`)
}

func TestFeedHandlerSourceLinks(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return []rss.Article{
				{ID: "art-1", Title: "Linked", URL: "https://www.example.com/a"},
				{ID: "art-2", Title: "Unlinked"},
				{ID: "art-3", Title: "Not http", URL: "mailto:someone@example.com"},
			}, nil
		},
	})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	want := `<a class="article-source" href="https://www.example.com/a" ping="/articles/art-1/read" target="_blank" rel="noopener noreferrer">example.com ↗</a>`
	if !strings.Contains(body, want) {
		t.Errorf("body does not contain %q: %s", want, body)
	}
	if n := strings.Count(body, `class="article-source"`); n != 1 {
		t.Errorf("source links = %d, want 1 (only the article with an http(s) URL): %s", n, body)
	}
}

func TestFeedHandlerReadRows(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return feedArticles(2), nil
		},
	})
	ids := []string{feedArticles(2)[0].ID, feedArticles(2)[1].ID}
	var gotUser string
	var gotIDs []string
	s.services.ReadService = readStoreReporting(&gotUser, &gotIDs, ids[0])

	rr := serveFeedAs(t, s, "user-1", testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser != "user-1" || !slices.Equal(gotIDs, ids) {
		t.Errorf("ReadArticleIDs(%q, %v), want (user-1, %v)", gotUser, gotIDs, ids)
	}
	assertReadMarkers(t, rr.Body.String(), ids[0], ids[1])
}

func TestFeedHandlerSavedRows(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return feedArticles(2), nil
		},
	})
	ids := []string{feedArticles(2)[0].ID, feedArticles(2)[1].ID}
	var gotUser string
	var gotIDs []string
	s.services.SaveService = saveStoreReporting(&gotUser, &gotIDs, ids[1])

	rr := serveFeedAs(t, s, "user-1", testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser != "user-1" || !slices.Equal(gotIDs, ids) {
		t.Errorf("SavedArticleIDs(%q, %v), want (user-1, %v)", gotUser, gotIDs, ids)
	}
	assertSaveButtons(t, rr.Body.String(), "/feeds/"+testFeedID, ids[1], ids[0])
}

func TestFeedHandlerStarredRows(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return feedArticles(2), nil
		},
	})
	ids := []string{feedArticles(2)[0].ID, feedArticles(2)[1].ID}
	var gotUser string
	var gotIDs []string
	s.services.StarService = starStoreReporting(&gotUser, &gotIDs, ids[0])

	rr := serveFeedAs(t, s, "user-1", testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser != "user-1" || !slices.Equal(gotIDs, ids) {
		t.Errorf("StarredArticleIDs(%q, %v), want (user-1, %v)", gotUser, gotIDs, ids)
	}
	assertStarButtons(t, rr.Body.String(), "/feeds/"+testFeedID, ids[0], ids[1])
}

// feedArticles returns n articles, newest first, the odd ones undated.
func feedArticles(n int) []rss.Article {
	as := make([]rss.Article, n)
	for i := range as {
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute)
		as[i] = rss.Article{ID: fmt.Sprintf(timelineUUID, i), Title: fmt.Sprintf("Article %d", i), CreatedAt: at}
		if i%2 == 0 {
			as[i].PublishedAt, as[i].CreatedAt = at, at.Add(time.Hour)
		}
	}
	return as
}

func TestFeedHandlerPaging(t *testing.T) {
	var gotBefore rss.ArticleCursor
	calls := 0
	var result []rss.Article
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			calls++
			gotBefore = before
			return result, nil
		},
	})
	feedPath := "/feeds/" + testFeedID

	olderHref := func(t *testing.T, body string) string {
		t.Helper()
		start := strings.Index(body, `href="`+feedPath+`?before=`)
		if start < 0 {
			return ""
		}
		href := body[start+len(`href="`):]
		return href[:strings.Index(href, `"`)]
	}

	t.Run("31 results gives 30 rows and an older link", func(t *testing.T) {
		result = feedArticles(31)
		body := serveFeed(t, s, testFeedID).Body.String()
		if got := strings.Count(body, `class="article-row"`); got != 30 {
			t.Errorf("rows = %d, want 30", got)
		}
		if !strings.Contains(body, `rel="next"`) || !strings.Contains(body, "Older articles") {
			t.Fatalf("no older link: %s", body)
		}
		href := olderHref(t, body)
		if href == "" {
			t.Fatalf("no before link: %s", body)
		}
		// The 30th article is undated, so the cursor uses its CreatedAt.
		last := result[29]
		want := encodeCursor(rss.ArticleCursor{At: last.CreatedAt, ID: last.ID})
		// html/template's URL escaping may differ from QueryEscape; compare decoded.
		u, err := url.ParseRequestURI(href)
		if err != nil || u.Path != feedPath || u.Query().Get("before") != want {
			t.Errorf("href = %q, want %s?before=%s", href, feedPath, want)
		}
		if strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected Back to newest on the first page")
		}
	})

	t.Run("30 results gives no link", func(t *testing.T) {
		result = feedArticles(30)
		body := serveFeed(t, s, testFeedID).Body.String()
		if strings.Contains(body, "Older articles") {
			t.Errorf("unexpected older link")
		}
	})

	t.Run("before is decoded and shows back to newest", func(t *testing.T) {
		result = feedArticles(2)
		want := rss.ArticleCursor{At: time.Date(2026, 1, 1, 0, 0, 0, 5, time.UTC), ID: "123e4567-e89b-12d3-a456-426614174000"}
		rr := serveFeedQuery(t, s, testFeedID, "?before="+url.QueryEscape(encodeCursor(want)))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if !gotBefore.At.Equal(want.At) || gotBefore.ID != want.ID {
			t.Errorf("store before = %+v, want %+v", gotBefore, want)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `<a href="`+feedPath+`">Back to newest</a>`) {
			t.Errorf("missing Back to newest link to %s: %s", feedPath, body)
		}
		// Paged views keep the feed's header.
		if !strings.Contains(body, "Example Feed Title") || !strings.Contains(body, "Subscribe") {
			t.Errorf("paged view lost the feed header: %s", body)
		}
	})

	t.Run("malformed before is 400", func(t *testing.T) {
		before := calls
		rr := serveFeedQuery(t, s, testFeedID, "?before=junk")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
		if calls != before {
			t.Errorf("store was called")
		}
	})

	t.Run("empty with a cursor", func(t *testing.T) {
		result = nil
		cur := encodeCursor(rss.ArticleCursor{At: time.Now(), ID: "123e4567-e89b-12d3-a456-426614174000"})
		body := serveFeedQuery(t, s, testFeedID, "?before="+url.QueryEscape(cur)).Body.String()
		if !strings.Contains(body, "No older articles.") || strings.Contains(body, "No articles yet.") {
			t.Errorf("wrong empty message: %s", body)
		}
	})

	t.Run("empty without a cursor", func(t *testing.T) {
		result = nil
		body := serveFeed(t, s, testFeedID).Body.String()
		if !strings.Contains(body, "No articles yet.") || strings.Contains(body, "No older articles.") {
			t.Errorf("wrong empty message: %s", body)
		}
	})
}

func TestFeedHandlerArticleImages(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			return []rss.Article{
				{ID: "art-1", Title: "With image", ImageURL: "https://example.com/pic.jpg"},
				{ID: "art-2", Title: "Without image"},
			}, nil
		},
	})

	body := serveFeed(t, s, testFeedID).Body.String()

	if n := strings.Count(body, `<img class="article-image"`); n != 1 {
		t.Fatalf("article images = %d, want 1: %s", n, body)
	}
	for _, want := range []string{
		`src="https://example.com/pic.jpg"`,
		`referrerpolicy="no-referrer"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	// The image belongs to the first row only.
	second := body[strings.Index(body, `id="article-art-2"`):]
	if strings.Contains(second, "<img") {
		t.Errorf("article without an image rendered an <img>: %s", second)
	}
}

func TestFeedHandlerPageMetadata(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			return []rss.Article{
				{ID: "art-1", PageImageURL: "https://example.com/page.jpg", PageTitle: "Page title", PageDescription: "Page description"},
				{ID: "art-2", Title: "Feed title", ImageURL: "https://example.com/feed.jpg", Excerpt: "Feed excerpt",
					PageImageURL: "https://example.com/other.jpg", PageTitle: "Other title", PageDescription: "Other description"},
			}, nil
		},
	})

	body := serveFeed(t, s, testFeedID).Body.String()

	first := body[strings.Index(body, `id="article-art-1"`):strings.Index(body, `id="article-art-2"`)]
	for _, want := range []string{
		`src="https://example.com/page.jpg"`,
		`<a href="/articles/art-1">Page title</a>`,
		`<p class="article-excerpt">Page description</p>`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("row with only page values does not contain %q: %s", want, first)
		}
	}
	second := body[strings.Index(body, `id="article-art-2"`):]
	for _, want := range []string{
		`src="https://example.com/feed.jpg"`,
		`<a href="/articles/art-2">Feed title</a>`,
		`<p class="article-excerpt">Feed excerpt</p>`,
	} {
		if !strings.Contains(second, want) {
			t.Errorf("row with feed values does not contain %q: %s", want, second)
		}
	}
	for _, bad := range []string{"other.jpg", "Other title", "Other description"} {
		if strings.Contains(body, bad) {
			t.Errorf("page value %q shown over the feed's: %s", bad, body)
		}
	}
}

func TestFeedHandlerNoArticles(t *testing.T) {
	rr := serveFeed(t, feedServer(t, nil), testFeedID)

	if !strings.Contains(rr.Body.String(), "No articles yet.") {
		t.Errorf("body does not contain %q: %s", "No articles yet.", rr.Body.String())
	}
}

func TestFeedHandlerStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name  string
		serve func(t *testing.T) *Server
	}{
		{"site", func(t *testing.T) *Server { return feedServer(t, boom) }},
		{"articles", func(t *testing.T) *Server {
			return feedServerWithArticles(t, nil, &fakeArticleStore{
				listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) { return nil, boom },
			})
		}},
		{"read article IDs", func(t *testing.T) *Server {
			s := feedServer(t, nil)
			s.services.ReadService = &fakeReadStore{readArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) { return nil, boom }}
			return s
		}},
		{"saved article IDs", func(t *testing.T) *Server {
			s := feedServer(t, nil)
			s.services.SaveService = &fakeSaveStore{savedArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) { return nil, boom }}
			return s
		}},
		{"starred article IDs", func(t *testing.T) *Server {
			s := feedServer(t, nil)
			s.services.StarService = &fakeStarStore{starredArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) { return nil, boom }}
			return s
		}},
		{"subscription", func(t *testing.T) *Server {
			s := feedServer(t, nil)
			s.services.SubscriptionService = &fakeSubscriptionStore{getFn: func(context.Context, string, string) (rss.Subscription, error) { return rss.Subscription{}, boom }}
			return s
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rr := serveFeed(t, tt.serve(t), testFeedID); rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
			}
		})
	}
}

func TestFeedHandlerNotFound(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			return rss.Feed{}, rss.ErrNoRecord
		},
	}})

	rr := serveFeed(t, s, testFeedID)

	assertNotFoundHTML(t, rr)
}

func getFeeds(t *testing.T, subs *fakeSubscriptionStore) (*httptest.ResponseRecorder, string) {
	t.Helper()
	s := newTestServerWith(t, Services{SubscriptionService: subs})
	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.feedsHandler)).ServeHTTP(rr, req)
	return rr, rr.Body.String()
}

func listing(subs ...rss.Subscription) *fakeSubscriptionStore {
	return &fakeSubscriptionStore{listByUserFn: func(ctx context.Context, userID string) ([]rss.Subscription, error) {
		if userID != "user-1" {
			return nil, fmt.Errorf("listed for user %q", userID)
		}
		return subs, nil
	}}
}

func TestFeedsHandler(t *testing.T) {
	const (
		otherID = "22222222-2222-4222-8222-222222222222"
		siteA   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		siteB   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	a := rss.Site{ID: siteA, Host: "a.example.com", Title: "Site A"}
	b := rss.Site{ID: siteB, Host: "b.example.com"}
	latest := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

	assertContains := func(t *testing.T, body string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
	}
	assertNotContains := func(t *testing.T, body string, nots ...string) {
		t.Helper()
		for _, not := range nots {
			if strings.Contains(body, not) {
				t.Errorf("body contains %q: %s", not, body)
			}
		}
	}

	t.Run("a site with two subscriptions gets a header above both", func(t *testing.T) {
		rr, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}, LatestArticleAt: latest},
			rss.Subscription{Site: a, Feed: rss.Feed{ID: otherID, Url: "https://a.example.com/two.xml"}},
			rss.Subscription{Site: b, Feed: rss.Feed{ID: "33333333-3333-4333-8333-333333333333", Title: "Three"}},
		))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		header := `<h2><a href="/sites/` + siteA + `">Site A</a></h2>`
		one := `<a class="feed-row-title" href="/feeds/` + testFeedID + `">One</a>`
		two := `<a class="feed-row-title" href="/feeds/` + otherID + `">https://a.example.com/two.xml</a>`
		assertContains(t, body, "<h1>My feeds</h1>", header, one, two)
		if n := strings.Count(body, "<h2>"); n != 1 {
			t.Errorf("got %d site headers, want 1 (site B has one subscription): %s", n, body)
		}
		if h, o, w := strings.Index(body, header), strings.Index(body, one), strings.Index(body, two); h > o || h > w {
			t.Errorf("the site header should come before both its feeds: %s", body)
		}
		// Grouped rows don't repeat their site.
		assertNotContains(t, body, `From <a href="/sites/`+siteA+`">`)
		if strings.Index(body, "One") > strings.Index(body, "Three") {
			t.Errorf("site A's feeds should come before site B's: %s", body)
		}
	})

	t.Run("a site with one subscription gets no header", func(t *testing.T) {
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertNotContains(t, body, "<h2>", `class="list-head"`)
	})

	t.Run("a single subscription names its site when the names differ", func(t *testing.T) {
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}},
			rss.Subscription{Site: b, Title: "My B", Feed: rss.Feed{ID: otherID, Title: "b.example.com"}},
		))
		assertContains(t, body,
			`<p class="feed-site">From <a href="/sites/`+siteA+`">Site A</a></p>`,
			// A site without a title goes by its host.
			`<p class="feed-site">From <a href="/sites/`+siteB+`">b.example.com</a></p>`,
		)
	})

	t.Run("a single subscription named like its site doesn't repeat it", func(t *testing.T) {
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "Site A"}},
			rss.Subscription{Site: b, Feed: rss.Feed{ID: otherID, Title: "b.example.com"}},
		))
		assertNotContains(t, body, `class="feed-site"`)
	})

	t.Run("latest article", func(t *testing.T) {
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One", PostsPerWeek: 3.4}, LatestArticleAt: latest},
			rss.Subscription{Site: a, Feed: rss.Feed{ID: otherID, Title: "Two"}},
		))
		assertContains(t, body, `Latest article <time data-local="date" datetime="2026-03-04T10:00:00Z">4 Mar 2026</time> · About 3 posts a week</p>`, "<p>No articles yet.</p>")
		// The feed without articles shows no rate.
		assertNotContains(t, body, "No posts in the last 30 days")
	})

	t.Run("latest article in another zone", func(t *testing.T) {
		// 23:30 EST on 4 Mar is 5 Mar in UTC.
		late := time.Date(2026, 3, 4, 23, 30, 0, 0, time.FixedZone("EST", -5*60*60))
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}, LatestArticleAt: late},
		))
		assertContains(t, body, `Latest article <time data-local="date" datetime="2026-03-05T04:30:00Z">5 Mar 2026</time>`)
	})

	health := []struct {
		name    string
		feed    rss.Feed
		want    []string
		wantNot []string
	}{
		{"gone", rss.Feed{GoneAt: latest}, []string{"This feed no longer exists."}, []string{"failing to update"}},
		{"3 failures", rss.Feed{ConsecutiveFailures: 3}, []string{"This feed is failing to update."}, []string{"no longer exists"}},
		{"2 failures", rss.Feed{ConsecutiveFailures: 2}, nil, []string{"failing to update", "no longer exists"}},
		{"gone wins over failing", rss.Feed{GoneAt: latest, ConsecutiveFailures: 5}, []string{"This feed no longer exists."}, []string{"failing to update"}},
	}
	for _, tt := range health {
		t.Run(tt.name, func(t *testing.T) {
			tt.feed.ID = testFeedID
			_, body := getFeeds(t, listing(rss.Subscription{Site: a, Feed: tt.feed}))
			assertContains(t, body, tt.want...)
			assertNotContains(t, body, tt.wantNot...)
		})
	}

	t.Run("each row links to its feed and edit page, and has no Unsubscribe", func(t *testing.T) {
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}},
			rss.Subscription{Site: b, Feed: rss.Feed{ID: otherID, Title: "Two"}},
		))
		for _, id := range []string{testFeedID, otherID} {
			assertContains(t, body,
				`<a class="feed-row-title" href="/feeds/`+id+`">`,
				`href="/feeds/`+id+`/edit?return_to=/feeds"`,
			)
		}
		// Unsubscribe is on Edit subscription and the feed page.
		assertNotContains(t, body, "/unsubscribe", "Unsubscribe")
	})

	t.Run("renamed subscription shows its name and an Edit link", func(t *testing.T) {
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Title: "My Name", Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertContains(t, body,
			`<a class="feed-row-title" href="/feeds/`+testFeedID+`">My Name</a>`,
			`<a class="row-icon-btn" href="/feeds/`+testFeedID+`/edit?return_to=/feeds" aria-label="Edit My Name" title="Edit">`,
		)
		assertNotContains(t, body, ">One<")
	})

	t.Run("unrenamed subscription shows the feed's title", func(t *testing.T) {
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertContains(t, body,
			`<a class="feed-row-title" href="/feeds/`+testFeedID+`">One</a>`,
			`aria-label="Edit One"`,
		)
	})

	t.Run("a feed's tags link to their pages", func(t *testing.T) {
		tags := []rss.Tag{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "AI"}, {ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Name: "News"}}
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Tags: tags, Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertContains(t, body,
			`<p class="feed-tags"><a href="/tags/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa">AI</a><a href="/tags/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb">News</a></p>`,
		)
	})

	t.Run("a feed without tags has no tag line", func(t *testing.T) {
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertNotContains(t, body, `class="feed-tags"`)
	})

	t.Run("empty state", func(t *testing.T) {
		rr, body := getFeeds(t, listing())
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertContains(t, body, `<a href="/search">Search</a> for a site to find some.`)
	})

	t.Run("flash", func(t *testing.T) {
		s := newTestServerWith(t, Services{SubscriptionService: listing()})
		req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
		req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "flash", "Unsubscribed from One.")
			s.feedsHandler(w, r)
		})).ServeHTTP(rr, req)
		assertContains(t, rr.Body.String(), "Unsubscribed from One.")
	})

	t.Run("store error", func(t *testing.T) {
		rr, _ := getFeeds(t, &fakeSubscriptionStore{listByUserFn: func(ctx context.Context, userID string) ([]rss.Subscription, error) {
			return nil, errors.New("boom")
		}})
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestFeedHandlerRefreshControls(t *testing.T) {
	tests := []struct {
		name        string
		lastFetched time.Time
		want        []string
		wantNot     []string
	}{
		{"never fetched", time.Time{}, []string{"Never fetched."}, []string{"posts a week"}},
		{"fetched", time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), []string{`Last fetched <time data-local datetime="2026-01-02T15:04:00Z">2 Jan 2026 15:04 UTC</time>.`, "About 3 posts a week."}, nil},
		{"fetched in another zone", time.Date(2026, 1, 2, 11, 4, 0, 0, time.FixedZone("EDT", -4*60*60)), []string{`Last fetched <time data-local datetime="2026-01-02T15:04:00Z">2 Jan 2026 15:04 UTC</time>.`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
				ReadService:         &fakeReadStore{},
				SaveService:         &fakeSaveStore{},
				ArticleService:      &fakeArticleStore{},
				FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
					return rss.Feed{ID: id, Title: "T", Url: "https://example.com/feed.xml", SiteID: testSiteID, LastFetched: tt.lastFetched, PostsPerWeek: 3.4}, nil
				}},
				SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
					return rss.Site{ID: id, Host: "example.com"}, nil
				}},
			})
			rr := serveFeed(t, s, testFeedID)
			body := rr.Body.String()
			want := append(tt.want, `action="/feeds/`+testFeedID+`/refresh"`, `method="post"`)
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("body does not contain %q: %s", w, body)
				}
			}
			for _, not := range tt.wantNot {
				if strings.Contains(body, not) {
					t.Errorf("body contains %q: %s", not, body)
				}
			}
		})
	}
}

func TestFeedHandlerFetchStatus(t *testing.T) {
	attempt := time.Date(2026, 1, 2, 11, 4, 0, 0, time.FixedZone("EDT", -4*60*60)) // 15:04 UTC
	next := time.Date(2099, 1, 3, 9, 30, 0, 0, time.UTC)                           // future, so no refresh is requested
	tests := []struct {
		name     string
		failures int
		want     []string
		notWant  []string
	}{
		{"no failures", 0, []string{`Next check around <time data-local datetime="2099-01-03T09:30:00Z">3 Jan 2099 09:30 UTC</time>.`}, []string{"fetch-error"}},
		{"one failure", 1, []string{
			`The last attempt to fetch this feed failed (<time data-local datetime="2026-01-02T15:04:00Z">2 Jan 2026 15:04 UTC</time>). We'll keep trying.`,
			`Next check around <time data-local datetime="2099-01-03T09:30:00Z">3 Jan 2099 09:30 UTC</time>.`,
		}, []string{"The last 1 attempts"}},
		{"three failures", 3, []string{
			`The last 3 attempts to fetch this feed failed, most recently <time data-local datetime="2026-01-02T15:04:00Z">2 Jan 2026 15:04 UTC</time>.`,
			`Next check around <time data-local datetime="2099-01-03T09:30:00Z">3 Jan 2099 09:30 UTC</time>.`,
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
				ReadService:         &fakeReadStore{},
				SaveService:         &fakeSaveStore{},
				ArticleService:      &fakeArticleStore{},
				FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
					return rss.Feed{
						ID: id, Title: "T", Url: "https://example.com/feed.xml", SiteID: testSiteID,
						LastAttempt: attempt, NextFetch: next, ConsecutiveFailures: tt.failures,
						LastError: "dial tcp 10.0.0.1: secret detail",
					}, nil
				}},
				SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
					return rss.Site{ID: id, Host: "example.com"}, nil
				}},
			})
			body := serveFeed(t, s, testFeedID).Body.String()
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body does not contain %q: %s", w, body)
				}
			}
			for _, w := range append(tt.notWant, "secret detail") {
				if strings.Contains(body, w) {
					t.Errorf("body contains %q: %s", w, body)
				}
			}
		})
	}
}

func TestFeedHandlerGoneFeed(t *testing.T) {
	s := newTestServerWith(t, Services{
		SubscriptionService: &fakeSubscriptionStore{},
		ReadService:         &fakeReadStore{},
		SaveService:         &fakeSaveStore{},
		ArticleService:      &fakeArticleStore{},
		FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			return rss.Feed{
				ID: id, Title: "T", Url: "https://example.com/feed.xml", SiteID: testSiteID,
				LastAttempt: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), NextFetch: time.Date(2026, 1, 3, 9, 30, 0, 0, time.UTC),
				ConsecutiveFailures: 2, LastError: "status 410", GoneAt: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC),
			}, nil
		}},
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			return rss.Site{ID: id, Host: "example.com"}, nil
		}},
	})
	body := serveFeed(t, s, testFeedID).Body.String()
	want := `<p class="fetch-error">This feed no longer exists: its publisher removed it. We've stopped checking it.</p>`
	if !strings.Contains(body, want) {
		t.Errorf("body does not contain %q: %s", want, body)
	}
	for _, w := range []string{"attempts to fetch this feed failed", "Next check", "/refresh", "Refresh</button>"} {
		if strings.Contains(body, w) {
			t.Errorf("body contains %q: %s", w, body)
		}
	}
}

func TestFeedHandlerEmptyTitleShowsURL(t *testing.T) {
	tests := []struct {
		name, title, want string
	}{
		{"titled", "Example Feed", "Example Feed"},
		{"untitled", "", "https://example.com/feed.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
				ReadService:         &fakeReadStore{},
				SaveService:         &fakeSaveStore{},
				ArticleService:      &fakeArticleStore{},
				FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
					return rss.Feed{ID: id, Title: tt.title, Url: "https://example.com/feed.xml", SiteID: testSiteID}, nil
				}},
				SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
					return rss.Site{ID: id, Host: "example.com"}, nil
				}},
			})
			body := serveFeed(t, s, testFeedID).Body.String()
			for _, want := range []string{"<h1>" + tt.want + "</h1>", "<title>Paper Round - " + tt.want + "</title>"} {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q: %s", want, body)
				}
			}
		})
	}
}

func TestFeedHandlerRefreshRequest(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	const checking = "Checking for new articles…"

	tests := []struct {
		name        string
		feed        rss.Feed
		requestErr  error
		wantRequest bool
		wantMessage bool
	}{
		{"due feed", rss.Feed{NextFetch: past}, nil, true, true},
		{"feed not due", rss.Feed{NextFetch: future}, nil, false, false},
		{"gone feed", rss.Feed{NextFetch: past, GoneAt: past}, nil, false, false},
		{"pending request", rss.Feed{NextFetch: past, RefreshRequestedAt: past}, nil, false, true},
		{"pending request, not due", rss.Feed{NextFetch: future, RefreshRequestedAt: past}, nil, false, true},
		{"request error", rss.Feed{NextFetch: past}, errors.New("db down"), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requested []string
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
				ReadService:         &fakeReadStore{},
				SaveService:         &fakeSaveStore{},
				ArticleService:      &fakeArticleStore{},
				FeedService: &fakeFeedStore{
					getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
						f := tt.feed
						f.ID, f.Title, f.Url, f.SiteID = id, "T", "https://example.com/feed.xml", testSiteID
						return f, nil
					},
					requestRefreshFn: func(ctx context.Context, id string) error {
						requested = append(requested, id)
						return tt.requestErr
					},
				},
				SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
					return rss.Site{ID: id, Host: "example.com"}, nil
				}},
			})

			rr := serveFeed(t, s, testFeedID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if tt.wantRequest {
				if len(requested) != 1 || requested[0] != testFeedID {
					t.Errorf("RequestRefresh calls = %v, want [%s]", requested, testFeedID)
				}
			} else if len(requested) != 0 {
				t.Errorf("RequestRefresh calls = %v, want none", requested)
			}
			body := rr.Body.String()
			if got := strings.Contains(body, checking); got != tt.wantMessage {
				t.Errorf("body contains %q = %v, want %v: %s", checking, got, tt.wantMessage, body)
			}
			if tt.wantMessage && strings.Contains(body, "Next check around") {
				t.Errorf("body contains %q alongside %q: %s", "Next check around", checking, body)
			}
		})
	}
}

func TestRefreshFlash(t *testing.T) {
	tests := []struct {
		res  rss.FetchResult
		want string
	}{
		{rss.FetchResult{}, "No new articles."},
		{rss.FetchResult{New: 1}, "1 new article."},
		{rss.FetchResult{New: 3}, "3 new articles."},
		{rss.FetchResult{Updated: 2}, "No new articles, 2 updated."},
		{rss.FetchResult{New: 3, Updated: 1}, "3 new articles, 1 updated."},
	}
	for _, tt := range tests {
		if got := refreshFlash(tt.res); got != tt.want {
			t.Errorf("refreshFlash(%+v) = %q, want %q", tt.res, got, tt.want)
		}
	}
}

func TestPostingRate(t *testing.T) {
	tests := []struct {
		perWeek float64
		want    string
	}{
		{0, "No posts in the last 30 days"},
		{0.5, "Less than one post a week"},
		{1, "About 1 post a week"},
		{1.4, "About 1 post a week"},
		{3.4, "About 3 posts a week"},
		{13.9, "About 14 posts a week"},
		{14, "About 2 posts a day"},
		{70, "About 10 posts a day"},
	}
	for _, tt := range tests {
		if got := postingRate(tt.perWeek); got != tt.want {
			t.Errorf("postingRate(%v) = %q, want %q", tt.perWeek, got, tt.want)
		}
	}
}

// postRefresh posts to the refresh handler as userID and returns the response
// and the flash message left in the session.
func postRefresh(t *testing.T, s *Server, userID, feedID string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/feeds/x/refresh", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", feedID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.feedRefreshHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func refreshServer(t *testing.T, feed rss.Feed, getErr error, ref *fakeRefresher) *Server {
	t.Helper()
	feed.ID = testFeedID
	return newTestServerWith(t, Services{
		FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			if getErr != nil {
				return rss.Feed{}, getErr
			}
			return feed, nil
		}},
		Refresher: ref,
	})
}

func refreshReturns(res rss.FetchResult, err error) *fakeRefresher {
	return &fakeRefresher{refreshFn: func(context.Context, rss.Feed) (rss.FetchResult, error) { return res, err }}
}

func TestFeedRefreshHandler(t *testing.T) {
	tests := []struct {
		name      string
		res       rss.FetchResult
		err       error
		wantCode  int
		wantFlash string
	}{
		{"success", rss.FetchResult{New: 3, Updated: 1}, nil, http.StatusSeeOther, "3 new articles, 1 updated."},
		{"unreachable", rss.FetchResult{}, fmt.Errorf("%w: status 500", ingest.ErrUnreachable), http.StatusSeeOther, "Couldn't reach this feed. Try again later."},
		{"not a feed", rss.FetchResult{}, fmt.Errorf("%w: bad xml", ingest.ErrNotFeed), http.StatusSeeOther, "This feed's address didn't return a feed. Try again later."},
		{"gone", rss.FetchResult{}, fmt.Errorf("%w: status 410", ingest.ErrGone), http.StatusSeeOther, "This feed no longer exists. We've stopped checking it."},
		{"deleted", rss.FetchResult{}, rss.ErrNoRecord, http.StatusNotFound, ""},
		{"other error", rss.FetchResult{}, errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := refreshReturns(tt.res, tt.err)
			s := refreshServer(t, rss.Feed{Url: "https://example.com/feed.xml"}, nil, ref)

			rr, flash := postRefresh(t, s, "user-1", testFeedID)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if tt.wantCode == http.StatusSeeOther {
				if loc := rr.Header().Get("Location"); loc != "/feeds/"+testFeedID {
					t.Errorf("Location = %q, want /feeds/%s", loc, testFeedID)
				}
			}
			if flash != tt.wantFlash {
				t.Errorf("flash = %q, want %q", flash, tt.wantFlash)
			}
			if len(ref.calls) != 1 || ref.calls[0].ID != testFeedID || ref.calls[0].Url != "https://example.com/feed.xml" {
				t.Errorf("Refresh calls = %+v, want one with the feed from GetByID", ref.calls)
			}
		})
	}
}

func TestFeedRefreshHandlerNotFound(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		getCalled := false
		ref := refreshReturns(rss.FetchResult{}, nil)
		s := newTestServerWith(t, Services{
			FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
				getCalled = true
				return rss.Feed{}, nil
			}},
			Refresher: ref,
		})
		rr, _ := postRefresh(t, s, "user-1", "abc")
		assertNotFoundHTML(t, rr)
		if getCalled {
			t.Error("GetByID called for invalid id")
		}
	})
	t.Run("no record", func(t *testing.T) {
		ref := refreshReturns(rss.FetchResult{}, nil)
		s := refreshServer(t, rss.Feed{}, rss.ErrNoRecord, ref)
		rr, _ := postRefresh(t, s, "user-1", testFeedID)
		assertNotFoundHTML(t, rr)
		if len(ref.calls) != 0 {
			t.Errorf("Refresh called %d times, want 0", len(ref.calls))
		}
	})
}

func TestFeedRefreshHandlerCooldown(t *testing.T) {
	t.Run("recent", func(t *testing.T) {
		ref := refreshReturns(rss.FetchResult{}, nil)
		s := refreshServer(t, rss.Feed{LastFetched: time.Now().Add(-time.Minute)}, nil, ref)
		rr, flash := postRefresh(t, s, "user-1", testFeedID)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rr.Code)
		}
		if want := "This feed was refreshed in the last few minutes. Try again later."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if len(ref.calls) != 0 {
			t.Errorf("Refresh called %d times, want 0", len(ref.calls))
		}
	})
	t.Run("old", func(t *testing.T) {
		ref := refreshReturns(rss.FetchResult{}, nil)
		s := refreshServer(t, rss.Feed{LastFetched: time.Now().Add(-10 * time.Minute)}, nil, ref)
		postRefresh(t, s, "user-1", testFeedID)
		if len(ref.calls) != 1 {
			t.Errorf("Refresh called %d times, want 1", len(ref.calls))
		}
	})
	t.Run("cooldown does not use rate limit", func(t *testing.T) {
		ref := refreshReturns(rss.FetchResult{}, nil)
		s := refreshServer(t, rss.Feed{LastFetched: time.Now().Add(-time.Minute)}, nil, ref)
		for range 11 {
			postRefresh(t, s, "user-1", testFeedID)
		}
		if !s.refreshLimiter.Allow("user-1") {
			t.Error("cooldown clicks used up the rate limit")
		}
	})
}

func TestFeedRefreshHandlerGoneFeed(t *testing.T) {
	ref := refreshReturns(rss.FetchResult{}, nil)
	s := refreshServer(t, rss.Feed{GoneAt: time.Now().Add(-time.Hour), LastFetched: time.Now().Add(-time.Minute)}, nil, ref)
	for range 11 {
		rr, flash := postRefresh(t, s, "user-1", testFeedID)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rr.Code)
		}
		if want := "This feed no longer exists, so it can't be refreshed."; flash != want {
			t.Fatalf("flash = %q, want %q", flash, want)
		}
	}
	if len(ref.calls) != 0 {
		t.Errorf("Refresh called %d times, want 0", len(ref.calls))
	}
	if !s.refreshLimiter.Allow("user-1") {
		t.Error("clicks on a gone feed used up the rate limit")
	}
}

func TestFeedRefreshHandlerRateLimit(t *testing.T) {
	ref := refreshReturns(rss.FetchResult{}, nil)
	s := refreshServer(t, rss.Feed{}, nil, ref)

	for i := 1; i <= 10; i++ {
		if _, flash := postRefresh(t, s, "user-1", testFeedID); flash != "No new articles." {
			t.Fatalf("refresh %d: flash = %q", i, flash)
		}
	}
	_, flash := postRefresh(t, s, "user-1", testFeedID)
	if want := "You've refreshed a lot of feeds recently. Please wait a few minutes and try again."; flash != want {
		t.Errorf("flash = %q, want %q", flash, want)
	}
	if len(ref.calls) != 10 {
		t.Errorf("Refresh called %d times, want 10", len(ref.calls))
	}

	if _, flash := postRefresh(t, s, "user-2", testFeedID); flash != "No new articles." {
		t.Errorf("other user: flash = %q", flash)
	}
}

func TestFeedRefreshHandlerDeadline(t *testing.T) {
	var checked bool
	ref := &fakeRefresher{refreshFn: func(ctx context.Context, _ rss.Feed) (rss.FetchResult, error) {
		checked = true
		d, ok := ctx.Deadline()
		if !ok {
			t.Error("context has no deadline")
		} else if time.Until(d) > refreshTimeout {
			t.Errorf("deadline in %v, want at most %v", time.Until(d), refreshTimeout)
		}
		return rss.FetchResult{}, nil
	}}
	s := refreshServer(t, rss.Feed{}, nil, ref)
	postRefresh(t, s, "user-1", testFeedID)
	if !checked {
		t.Error("Refresh not called")
	}
}

// postSubscription posts to handler as userID, with returnTo as the form's
// return_to if it is not empty. It returns the response and the flash message
// left in the session.
func postSubscription(t *testing.T, s *Server, handler http.HandlerFunc, userID, feedID, returnTo string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	form := url.Values{}
	if returnTo != "" {
		form.Set("return_to", returnTo)
	}
	req := httptest.NewRequest(http.MethodPost, "/feeds/x/subscribe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", feedID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

type subscriptionCall struct{ userID, feedID string }

func subscriptionServer(t *testing.T, feed rss.Feed, getErr error, subs *fakeSubscriptionStore) *Server {
	t.Helper()
	feed.ID = testFeedID
	return newTestServerWith(t, Services{
		FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			if getErr != nil {
				return rss.Feed{}, getErr
			}
			return feed, nil
		}},
		SubscriptionService: subs,
	})
}

func TestFeedSubscribeHandler(t *testing.T) {
	feed := rss.Feed{Title: "Example Feed", Url: "https://example.com/feed.xml"}

	t.Run("success", func(t *testing.T) {
		var calls []subscriptionCall
		s := subscriptionServer(t, feed, nil, &fakeSubscriptionStore{subscribeFn: func(ctx context.Context, userID, feedID string) error {
			calls = append(calls, subscriptionCall{userID, feedID})
			return nil
		}})

		rr, flash := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "/sites/"+testSiteID+"?x=1")

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if loc := rr.Header().Get("Location"); loc != "/sites/"+testSiteID+"?x=1" {
			t.Errorf("Location = %q, want the return_to path", loc)
		}
		if want := "Subscribed to Example Feed."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if len(calls) != 1 || calls[0] != (subscriptionCall{"user-1", testFeedID}) {
			t.Errorf("Subscribe calls = %+v, want one for user-1 and %s", calls, testFeedID)
		}
	})

	t.Run("return_to keeps a fragment", func(t *testing.T) {
		s := subscriptionServer(t, feed, nil, &fakeSubscriptionStore{})
		returnTo := "/sites/" + testSiteID + "#feed-" + testFeedID

		rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, returnTo)

		if loc := rr.Header().Get("Location"); loc != returnTo {
			t.Errorf("Location = %q, want %q", loc, returnTo)
		}
	})

	t.Run("invalid return_to goes to the feed page", func(t *testing.T) {
		for _, returnTo := range []string{"", "//example.com", "https://example.com/"} {
			s := subscriptionServer(t, feed, nil, &fakeSubscriptionStore{})

			rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, returnTo)

			if loc := rr.Header().Get("Location"); loc != "/feeds/"+testFeedID {
				t.Errorf("return_to %q: Location = %q, want /feeds/%s", returnTo, loc, testFeedID)
			}
		}
	})

	t.Run("empty title uses the URL", func(t *testing.T) {
		s := subscriptionServer(t, rss.Feed{Url: "https://example.com/feed.xml"}, nil, &fakeSubscriptionStore{})

		_, flash := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "")

		if want := "Subscribed to https://example.com/feed.xml."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		s := newTestServer(t) // nil stores: any store call would panic
		rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", "abc", "")
		assertNotFoundHTML(t, rr)
	})

	t.Run("unknown feed", func(t *testing.T) {
		s := subscriptionServer(t, rss.Feed{}, rss.ErrNoRecord, &fakeSubscriptionStore{})
		rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "")
		assertNotFoundHTML(t, rr)
	})

	t.Run("feed deleted before subscribing", func(t *testing.T) {
		s := subscriptionServer(t, feed, nil, &fakeSubscriptionStore{subscribeFn: func(context.Context, string, string) error {
			return rss.ErrNoRecord
		}})
		rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "")
		assertNotFoundHTML(t, rr)
	})

	t.Run("gone feed", func(t *testing.T) {
		called := false
		gone := rss.Feed{Title: "Old Feed", GoneAt: time.Now()}
		s := subscriptionServer(t, gone, nil, &fakeSubscriptionStore{subscribeFn: func(context.Context, string, string) error {
			called = true
			return nil
		}})

		rr, flash := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "")

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if want := "This feed no longer exists, so you can't subscribe to it."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if called {
			t.Error("Subscribe called for a gone feed")
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := subscriptionServer(t, feed, nil, &fakeSubscriptionStore{subscribeFn: func(context.Context, string, string) error {
			return errors.New("db down")
		}})
		rr, _ := postSubscription(t, s, s.feedSubscribeHandler, "user-1", testFeedID, "")
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestFeedUnsubscribeHandler(t *testing.T) {
	feed := rss.Feed{Title: "Example Feed", Url: "https://example.com/feed.xml"}

	tests := []struct {
		name      string
		feed      rss.Feed
		storeErr  error
		wantCode  int
		wantFlash string
	}{
		{"success", feed, nil, http.StatusSeeOther, "Unsubscribed from Example Feed."},
		{"empty title uses the URL", rss.Feed{Url: "https://example.com/feed.xml"}, nil, http.StatusSeeOther, "Unsubscribed from https://example.com/feed.xml."},
		{"gone feed", rss.Feed{Title: "Old Feed", GoneAt: time.Now()}, nil, http.StatusSeeOther, "Unsubscribed from Old Feed."},
		{"store error", feed, errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []subscriptionCall
			s := subscriptionServer(t, tt.feed, nil, &fakeSubscriptionStore{unsubscribeFn: func(ctx context.Context, userID, feedID string) error {
				calls = append(calls, subscriptionCall{userID, feedID})
				return tt.storeErr
			}})

			rr, flash := postSubscription(t, s, s.feedUnsubscribeHandler, "user-1", testFeedID, "/sites/"+testSiteID)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if tt.wantCode == http.StatusSeeOther {
				if loc := rr.Header().Get("Location"); loc != "/sites/"+testSiteID {
					t.Errorf("Location = %q, want the return_to path", loc)
				}
			}
			if flash != tt.wantFlash {
				t.Errorf("flash = %q, want %q", flash, tt.wantFlash)
			}
			if len(calls) != 1 || calls[0] != (subscriptionCall{"user-1", testFeedID}) {
				t.Errorf("Unsubscribe calls = %+v, want one for user-1 and %s", calls, testFeedID)
			}
		})
	}

	t.Run("malformed id", func(t *testing.T) {
		s := newTestServer(t)
		rr, _ := postSubscription(t, s, s.feedUnsubscribeHandler, "user-1", "abc", "")
		assertNotFoundHTML(t, rr)
	})

	t.Run("unknown feed", func(t *testing.T) {
		s := subscriptionServer(t, rss.Feed{}, rss.ErrNoRecord, &fakeSubscriptionStore{})
		rr, _ := postSubscription(t, s, s.feedUnsubscribeHandler, "user-1", testFeedID, "")
		assertNotFoundHTML(t, rr)
	})
}

func TestFeedHandlerSubscribeControl(t *testing.T) {
	const (
		subscribeForm   = `action="/feeds/` + testFeedID + `/subscribe"`
		unsubscribeForm = `action="/feeds/` + testFeedID + `/unsubscribe"`
	)
	tests := []struct {
		name       string
		feed       rss.Feed
		subscribed bool
		want       []string
		notWant    []string
	}{
		{
			name: "not subscribed",
			feed: rss.Feed{Title: "Example Feed", Url: "https://example.com/feed.xml"},
			want: []string{subscribeForm, `value="/feeds/` + testFeedID + `"`, `aria-label="Subscribe to Example Feed"`},
			notWant: []string{
				unsubscribeForm, "Subscribed ✓", "/edit",
			},
		},
		{
			name:       "subscribed",
			feed:       rss.Feed{Title: "Example Feed", Url: "https://example.com/feed.xml"},
			subscribed: true,
			want:       []string{unsubscribeForm, `aria-label="Unsubscribe from Example Feed"`, `href="/feeds/` + testFeedID + `/edit?return_to=/feeds/` + testFeedID + `"`},
			notWant:    []string{subscribeForm},
		},
		{
			name: "empty title labels the button with the URL",
			feed: rss.Feed{Url: "https://example.com/feed.xml"},
			want: []string{`aria-label="Subscribe to https://example.com/feed.xml"`},
		},
		{
			name:    "gone and not subscribed",
			feed:    rss.Feed{Title: "Old Feed", GoneAt: time.Now()},
			notWant: []string{subscribeForm, unsubscribeForm},
		},
		{
			name:       "gone and subscribed",
			feed:       rss.Feed{Title: "Old Feed", GoneAt: time.Now()},
			subscribed: true,
			want:       []string{unsubscribeForm},
			notWant:    []string{subscribeForm},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotUser, gotFeed string
			s := subscriptionServer(t, tt.feed, nil, &fakeSubscriptionStore{
				getFn: func(ctx context.Context, userID, feedID string) (rss.Subscription, error) {
					gotUser, gotFeed = userID, feedID
					if !tt.subscribed {
						return rss.Subscription{}, rss.ErrNoRecord
					}
					return rss.Subscription{}, nil
				},
			})
			s.services.ArticleService = &fakeArticleStore{}
			s.services.ReadService = &fakeReadStore{}
			s.services.SaveService = &fakeSaveStore{}
			s.services.SiteService = &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
				return rss.Site{ID: id, Host: "example.com"}, nil
			}}

			rr := serveFeedAs(t, s, "user-1", testFeedID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if gotUser != "user-1" || gotFeed != testFeedID {
				t.Errorf("Get(%q, %q), want (user-1, %s)", gotUser, gotFeed, testFeedID)
			}
			body := rr.Body.String()
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body does not contain %q: %s", w, body)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(body, w) {
					t.Errorf("body contains %q: %s", w, body)
				}
			}
		})
	}
}

func TestFeedHandlerSubscriptionName(t *testing.T) {
	const feedTitleLine = `· Feed title “Example Feed Title”`
	tests := []struct {
		name    string
		get     func(context.Context, string, string) (rss.Subscription, error)
		want    []string
		notWant []string
	}{
		{
			name: "renamed",
			get: func(context.Context, string, string) (rss.Subscription, error) {
				return rss.Subscription{Title: "Pro Football Talk"}, nil
			},
			want: []string{
				"<title>Paper Round - Pro Football Talk</title>",
				"<h1>Pro Football Talk</h1>",
				`aria-label="Unsubscribe from Pro Football Talk"`,
				`From <a href="/sites/` + testSiteID + `">Example Site</a> ` + feedTitleLine + `</p>`,
			},
			notWant: []string{"<h1>Example Feed Title</h1>"},
		},
		{
			name: "subscribed without a name",
			get: func(context.Context, string, string) (rss.Subscription, error) {
				return rss.Subscription{}, nil
			},
			want:    []string{"<h1>Example Feed Title</h1>", `aria-label="Unsubscribe from Example Feed Title"`},
			notWant: []string{"Feed title “"},
		},
		{
			name: "not subscribed",
			get: func(context.Context, string, string) (rss.Subscription, error) {
				return rss.Subscription{}, rss.ErrNoRecord
			},
			want:    []string{"<h1>Example Feed Title</h1>", `aria-label="Subscribe to Example Feed Title"`},
			notWant: []string{"Feed title “", "Unsubscribe"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := feedServer(t, nil)
			s.services.SubscriptionService = &fakeSubscriptionStore{getFn: tt.get}

			rr := serveFeed(t, s, testFeedID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()
			assertBodyContains(t, body, tt.want...)
			for _, w := range tt.notWant {
				if strings.Contains(body, w) {
					t.Errorf("body contains %q: %s", w, body)
				}
			}
		})
	}
}

// serveFeedAs is serveFeed for a logged-in user.
func serveFeedAs(t *testing.T, s *Server, userID, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.feedHandler)).ServeHTTP(rr, req)
	return rr
}

func TestFeedHandlerSiteIcon(t *testing.T) {
	s := feedServer(t, nil)
	testSiteIcons(t, s, testSiteID, "E", func(t *testing.T) string {
		rr := serveFeed(t, s, testFeedID)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}

func TestFeedsHandlerSiteIcons(t *testing.T) {
	subs := listing(rss.Subscription{
		Site: rss.Site{ID: testSiteID, Host: "example.com", Title: "Example Site"},
		Feed: rss.Feed{ID: testFeedID, SiteID: testSiteID, Title: "Daily", Url: "https://example.com/feed.xml"},
	})
	s := newTestServerWith(t, Services{SubscriptionService: subs, SiteService: &fakeSiteStore{}})
	testSiteIcons(t, s, testSiteID, "D", func(t *testing.T) string {
		req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
		req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.feedsHandler)).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}

// serveSubscriptionEdit serves handler for the edit subscription page of
// feedID as user-1. form is posted when not nil, with query appended to the
// URL either way. It returns the response and the flash left in the session.
func serveSubscriptionEdit(t *testing.T, s *Server, handler http.HandlerFunc, feedID, query string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/feeds/x/edit"+query, nil)
	if form != nil {
		req = httptest.NewRequest(http.MethodPost, "/feeds/x/edit"+query, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", feedID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, "user-1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

type renameCall struct{ userID, feedID, title string }

type setTagsCall struct {
	userID, feedID   string
	tagIDs, newNames []string
}

// assertNewTagsError checks that body re-renders the form with wantError on
// new_tags, the typed newTags kept and the ticked tag (userTags[0]) still
// ticked.
func assertNewTagsError(t *testing.T, body, wantError, newTags string) {
	t.Helper()
	for _, want := range []string{
		`<p class="error" id="new_tags-error">` + wantError + `</p>`,
		`aria-describedby="new_tags-hint new_tags-error"`,
		`value="` + newTags + `"`,
		`value="` + userTags[0].ID + `" checked>`,
		`value="` + userTags[1].ID + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	if !regexp.MustCompile(`id="new_tags"[^>]*aria-invalid="true"[^>]*autofocus`).MatchString(body) {
		t.Errorf("new_tags is not invalid and focused: %s", body)
	}
}

// editSubscriptionServer serves sub to user-1 as their subscription to
// testFeedID and records the Rename calls in renames. The user has no tags.
func editSubscriptionServer(t *testing.T, sub rss.Subscription, renames *[]renameCall) *Server {
	t.Helper()
	return editSubscriptionServerWithTags(t, sub, renames, &fakeTagStore{})
}

// editSubscriptionServerWithTags is editSubscriptionServer with tags as the
// TagService.
func editSubscriptionServerWithTags(t *testing.T, sub rss.Subscription, renames *[]renameCall, tags *fakeTagStore) *Server {
	t.Helper()
	sub.Feed.ID = testFeedID
	return newTestServerWith(t, Services{TagService: tags, SubscriptionService: &fakeSubscriptionStore{
		getFn: func(ctx context.Context, userID, feedID string) (rss.Subscription, error) {
			if userID != "user-1" || feedID != testFeedID {
				return rss.Subscription{}, rss.ErrNoRecord
			}
			return sub, nil
		},
		renameFn: func(ctx context.Context, userID, feedID, title string) error {
			*renames = append(*renames, renameCall{userID, feedID, title})
			return nil
		},
	}})
}

// userTags are user-1's tags in the Edit subscription tests.
var userTags = []rss.Tag{
	{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "AI", Feeds: 2},
	{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Name: "News", Feeds: 0},
	{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Name: "Sports", Feeds: 1},
}

func listUserTags(ctx context.Context, userID string) ([]rss.Tag, error) {
	if userID != "user-1" {
		return nil, nil
	}
	return userTags, nil
}

func TestSubscriptionEdit(t *testing.T) {
	sub := rss.Subscription{
		Title: "Pro Football Talk",
		Feed:  rss.Feed{Title: "NBC Sports", Url: "https://example.com/feed.xml", SiteID: testSiteID},
		Site:  rss.Site{ID: testSiteID, Host: "example.com", Title: "Example Site"},
	}
	var renames []renameCall

	t.Run("shows the current name and the feed's title as placeholder", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		for _, want := range []string{
			"<h1>Edit subscription</h1>",
			`value="Pro Football Talk"`,
			`placeholder="NBC Sports"`,
			"From Example Site",
			"<code>https://example.com/feed.xml</code>",
			`action="/feeds/` + testFeedID + `/edit"`,
			`<a class="btn" href="/feeds">Cancel</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
	})

	t.Run("return_to goes into the hidden field", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "?return_to=/feeds/"+testFeedID, nil)
		body := rr.Body.String()
		for _, want := range []string{
			`name="return_to" value="/feeds/` + testFeedID + `"`,
			`<a class="btn" href="/feeds/` + testFeedID + `">Cancel</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
	})

	t.Run("Unsubscribe returns to My feeds", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		// Even when the page itself returns to the feed page, which would
		// only offer to subscribe again.
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "?return_to=/feeds/"+testFeedID, nil)
		assertBodyContains(t, rr.Body.String(),
			`<form class="danger-zone" action="/feeds/`+testFeedID+`/unsubscribe" method="post">
        <input type="hidden" name="return_to" value="/feeds">`,
			`aria-label="Unsubscribe from Pro Football Talk">Unsubscribe</button>`,
		)
	})

	t.Run("checkboxes for the user's tags, the subscription's ticked", func(t *testing.T) {
		tagged := sub
		tagged.Tags = []rss.Tag{userTags[0], userTags[2]}
		s := editSubscriptionServerWithTags(t, tagged, &renames, &fakeTagStore{listFn: listUserTags})
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		for _, want := range []string{
			"<legend>Tags</legend>",
			`<input type="checkbox" name="tags" value="` + userTags[0].ID + `" checked> AI</label>`,
			`<input type="checkbox" name="tags" value="` + userTags[1].ID + `"> News</label>`,
			`<input type="checkbox" name="tags" value="` + userTags[2].ID + `" checked> Sports</label>`,
			`<label for="new_tags">New tags</label>`,
			`aria-describedby="new_tags-hint"`,
			`<p class="hint" id="new_tags-hint">Separate tags with commas. Only you see your tags.</p>`,
			`<a href="/tags">Manage tags</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
	})

	t.Run("no tags: only the New tags field", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "", nil)
		body := rr.Body.String()
		if strings.Contains(body, `type="checkbox"`) {
			t.Errorf("body has a checkbox: %s", body)
		}
		if !strings.Contains(body, `id="new_tags"`) {
			t.Errorf("body has no New tags field: %s", body)
		}
	})

	t.Run("listing tags fails", func(t *testing.T) {
		s := editSubscriptionServerWithTags(t, sub, &renames, &fakeTagStore{listFn: func(context.Context, string) ([]rss.Tag, error) {
			return nil, errors.New("boom")
		}})
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, testFeedID, "", nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("non-UUID id", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, "abc", "", nil)
		assertNotFoundHTML(t, rr)
	})

	t.Run("not subscribed", func(t *testing.T) {
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditHandler, "22222222-2222-4222-8222-222222222222", "", nil)
		assertNotFoundHTML(t, rr)
	})
}

func TestSubscriptionEditPost(t *testing.T) {
	sub := rss.Subscription{
		Feed: rss.Feed{Title: "NBC Sports", Url: "https://example.com/feed.xml", SiteID: testSiteID},
		Site: rss.Site{ID: testSiteID, Host: "example.com"},
	}
	tooLong := strings.Repeat("é", 101)
	maxLen := strings.Repeat("é", 100)

	tests := []struct {
		name         string
		title        string
		returnTo     string
		wantCode     int
		wantLocation string
		wantRename   []renameCall // nil: Rename not called
	}{
		{"valid name", "  Pro Football Talk ", "", http.StatusSeeOther, "/feeds", []renameCall{{"user-1", testFeedID, "Pro Football Talk"}}},
		{"blank name", "   ", "", http.StatusSeeOther, "/feeds", []renameCall{{"user-1", testFeedID, ""}}},
		{"feed's title", " NBC Sports ", "", http.StatusSeeOther, "/feeds", []renameCall{{"user-1", testFeedID, ""}}},
		{"101 runes", tooLong, "", http.StatusUnprocessableEntity, "", nil},
		{"100 runes", maxLen, "", http.StatusSeeOther, "/feeds", []renameCall{{"user-1", testFeedID, maxLen}}},
		{"return_to the feed page", "PFT", "/feeds/" + testFeedID, http.StatusSeeOther, "/feeds/" + testFeedID, []renameCall{{"user-1", testFeedID, "PFT"}}},
		{"return_to another host", "PFT", "//evil.example", http.StatusSeeOther, "/feeds", []renameCall{{"user-1", testFeedID, "PFT"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var renames []renameCall
			s := editSubscriptionServer(t, sub, &renames)
			form := url.Values{"title": {tt.title}}
			if tt.returnTo != "" {
				form.Set("return_to", tt.returnTo)
			}

			rr, flash := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, testFeedID, "", form)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if fmt.Sprint(renames) != fmt.Sprint(tt.wantRename) {
				t.Errorf("Rename calls = %v, want %v", renames, tt.wantRename)
			}
			if tt.wantCode == http.StatusSeeOther {
				if loc := rr.Header().Get("Location"); loc != tt.wantLocation {
					t.Errorf("Location = %q, want %q", loc, tt.wantLocation)
				}
				if flash != "Subscription updated." {
					t.Errorf("flash = %q, want %q", flash, "Subscription updated.")
				}
				return
			}
			body := rr.Body.String()
			for _, want := range []string{
				`<p class="error" id="title-error">Must be 100 characters or fewer.</p>`,
				`aria-invalid="true"`,
				`aria-describedby="title-hint title-error"`,
				"autofocus",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q: %s", want, body)
				}
			}
		})
	}

	t.Run("tags", func(t *testing.T) {
		tooLongTag := strings.Repeat("é", 51)
		maxTag := strings.Repeat("é", 50)
		tests := []struct {
			name      string
			tags      []string
			newTags   string
			wantCode  int
			wantSet   *setTagsCall // nil: SetForSubscription not called
			wantError string
		}{
			{"ticked and new", []string{userTags[2].ID, userTags[0].ID}, "Cooking", http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, []string{userTags[2].ID, userTags[0].ID}, []string{"Cooking"}}, ""},
			{"none: removes all", nil, "", http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, nil, nil}, ""},
			{"an ID not the user's is dropped", []string{"dddddddd-dddd-4ddd-8ddd-dddddddddddd", userTags[1].ID, "abc"}, "", http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, []string{userTags[1].ID}, nil}, ""},
			{"an ID ticked twice", []string{userTags[1].ID, userTags[1].ID}, "", http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, []string{userTags[1].ID}, nil}, ""},
			{"new names split and normalized", nil, "News, ai , ,News,  Machine   learning ,AI", http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, nil, []string{"News", "ai", "Machine learning"}}, ""},
			{"50 runes", nil, maxTag, http.StatusSeeOther,
				&setTagsCall{"user-1", testFeedID, nil, []string{maxTag}}, ""},
			{"51 runes", []string{userTags[0].ID}, "News, " + tooLongTag, http.StatusUnprocessableEntity,
				nil, "Each tag must be 50 characters or fewer."},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var renames []renameCall
				var sets []setTagsCall
				s := editSubscriptionServerWithTags(t, sub, &renames, &fakeTagStore{
					listFn: listUserTags,
					setForSubscriptionFn: func(ctx context.Context, userID, feedID string, tagIDs, newNames []string) error {
						sets = append(sets, setTagsCall{userID, feedID, tagIDs, newNames})
						return nil
					},
				})
				form := url.Values{"title": {"PFT"}, "tags": tt.tags, "new_tags": {tt.newTags}}

				rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, testFeedID, "", form)

				if rr.Code != tt.wantCode {
					t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
				}
				var wantSets []setTagsCall
				if tt.wantSet != nil {
					wantSets = []setTagsCall{*tt.wantSet}
				}
				if fmt.Sprint(sets) != fmt.Sprint(wantSets) {
					t.Errorf("SetForSubscription calls = %v, want %v", sets, wantSets)
				}
				if tt.wantCode == http.StatusSeeOther {
					if want := []renameCall{{"user-1", testFeedID, "PFT"}}; fmt.Sprint(renames) != fmt.Sprint(want) {
						t.Errorf("Rename calls = %v, want %v", renames, want)
					}
					return
				}
				if len(renames) != 0 {
					t.Errorf("Rename calls = %v, want none", renames)
				}
				assertNewTagsError(t, rr.Body.String(), tt.wantError, tt.newTags)
			})
		}
	})

	t.Run("too many tags", func(t *testing.T) {
		var renames []renameCall
		s := editSubscriptionServerWithTags(t, sub, &renames, &fakeTagStore{
			listFn: listUserTags,
			setForSubscriptionFn: func(context.Context, string, string, []string, []string) error {
				return rss.ErrTooManyTags
			},
		})
		form := url.Values{"title": {"PFT"}, "tags": {userTags[0].ID}, "new_tags": {"Cooking"}}

		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, testFeedID, "", form)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		if len(renames) != 0 {
			t.Errorf("Rename calls = %v, want none", renames)
		}
		assertNewTagsError(t, rr.Body.String(), "You can have up to 100 tags.", "Cooking")
	})

	t.Run("SetForSubscription finds no subscription", func(t *testing.T) {
		var renames []renameCall
		s := editSubscriptionServerWithTags(t, sub, &renames, &fakeTagStore{
			setForSubscriptionFn: func(context.Context, string, string, []string, []string) error {
				return rss.ErrNoRecord
			},
		})
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, testFeedID, "", url.Values{"title": {"PFT"}})
		assertNotFoundHTML(t, rr)
	})

	t.Run("SetForSubscription fails", func(t *testing.T) {
		var renames []renameCall
		s := editSubscriptionServerWithTags(t, sub, &renames, &fakeTagStore{
			setForSubscriptionFn: func(context.Context, string, string, []string, []string) error {
				return errors.New("boom")
			},
		})
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, testFeedID, "", url.Values{"title": {"PFT"}})
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("not subscribed", func(t *testing.T) {
		var renames []renameCall
		s := editSubscriptionServer(t, sub, &renames)
		rr, _ := serveSubscriptionEdit(t, s, s.subscriptionEditPostHandler, "22222222-2222-4222-8222-222222222222", "", url.Values{"title": {"PFT"}})
		assertNotFoundHTML(t, rr)
		if len(renames) != 0 {
			t.Errorf("Rename calls = %v, want none", renames)
		}
	})
}
