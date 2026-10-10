package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const testArticleID = "22222222-2222-4222-8222-222222222222"

func serveArticle(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/articles/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.articleHandler)).ServeHTTP(rr, req)
	return rr
}

// articleServer returns a server whose article store returns article (or
// articleErr), and whose feed and site stores return feedErr and siteErr when
// set.
func articleServer(t *testing.T, article rss.Article, articleErr, feedErr, siteErr error) *Server {
	t.Helper()
	article.ID = testArticleID
	article.FeedID = testFeedID
	return newTestServerWith(t, Services{
		ReadService: &fakeReadStore{},
		ArticleService: &fakeArticleStore{getByIDFn: func(ctx context.Context, id string) (rss.Article, error) {
			if articleErr != nil {
				return rss.Article{}, articleErr
			}
			return article, nil
		}},
		FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			if feedErr != nil {
				return rss.Feed{}, feedErr
			}
			return rss.Feed{ID: id, Title: "Example Feed Title", Url: "https://example.com/feed.xml", SiteID: testSiteID}, nil
		}},
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			if siteErr != nil {
				return rss.Site{}, siteErr
			}
			return rss.Site{ID: id, Host: "example.com", Title: "Example Site"}, nil
		}},
	})
}

func assertBodyContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestArticleHandler(t *testing.T) {
	var gotID string
	article := rss.Article{
		Title:       "A <b>title</b>",
		URL:         "https://example.com/posts/a",
		Content:     `<p>Hello <strong>world</strong></p><script>CONTENT-MARKER</script><img src="/pic.png">`,
		Summary:     "SUMMARY-MARKER",
		PublishedAt: time.Date(2024, time.March, 2, 10, 5, 0, 0, time.UTC),
	}
	s := articleServer(t, article, nil, nil, nil)
	s.services.ArticleService.(*fakeArticleStore).getByIDFn = func(ctx context.Context, id string) (rss.Article, error) {
		gotID = id
		article.ID, article.FeedID = testArticleID, testFeedID
		return article, nil
	}

	rr := serveArticle(t, s, testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotID != testArticleID {
		t.Errorf("GetByID(%q), want %q", gotID, testArticleID)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		"<title>rss - A &lt;b&gt;title&lt;/b&gt;</title>",
		"<h1>A &lt;b&gt;title&lt;/b&gt;</h1>",
		`<a href="/feeds/`+testFeedID+`">Example Feed Title</a>`,
		`datetime="2024-03-02T10:05:00Z"`,
		"2 Mar 2024 10:05 UTC",
		`<div class="article-body">`,
		"<strong>world</strong>",
		`src="https://example.com/pic.png"`,
		`href="https://example.com/posts/a" target="_blank" rel="noopener noreferrer">Read on Example Site`,
	)
	for _, bad := range []string{"<script", "CONTENT-MARKER", "SUMMARY-MARKER", "<b>title</b>"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q: %s", bad, body)
		}
	}
}

func TestArticleHandlerSummaryWhenNoContent(t *testing.T) {
	rr := serveArticle(t, articleServer(t, rss.Article{Title: "T", Summary: "<p>Just a summary</p>"}, nil, nil, nil), testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	assertBodyContains(t, rr.Body.String(), "<p>Just a summary</p>")
}

func TestArticleHandlerNoText(t *testing.T) {
	rr := serveArticle(t, articleServer(t, rss.Article{Title: "T"}, nil, nil, nil), testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body, "This article has no text in its feed.")
	if strings.Contains(body, "article-body") {
		t.Errorf("empty article rendered a body: %s", body)
	}
}

func TestArticleHandlerNoURL(t *testing.T) {
	rr := serveArticle(t, articleServer(t, rss.Article{Title: "T", Content: "<p>x</p>"}, nil, nil, nil), testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); strings.Contains(body, "Read on") {
		t.Errorf("article without a URL has a Read on link: %s", body)
	}
}

func TestArticleHandlerRelativeURLsUseFeedURLWithoutArticleURL(t *testing.T) {
	rr := serveArticle(t, articleServer(t, rss.Article{Content: `<img src="/pic.png">`}, nil, nil, nil), testArticleID)

	assertBodyContains(t, rr.Body.String(), `src="https://example.com/pic.png"`)
}

func TestArticleHandlerUntitled(t *testing.T) {
	t.Run("excerpt", func(t *testing.T) {
		rr := serveArticle(t, articleServer(t, rss.Article{Excerpt: "Microblog post start"}, nil, nil, nil), testArticleID)
		assertBodyContains(t, rr.Body.String(), "<h1>Microblog post start</h1>")
	})
	t.Run("nothing", func(t *testing.T) {
		rr := serveArticle(t, articleServer(t, rss.Article{}, nil, nil, nil), testArticleID)
		assertBodyContains(t, rr.Body.String(), "<h1>(untitled)</h1>")
	})
}

func TestArticleHandlerMalformedID(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{"abc", " "} {
		t.Run(id, func(t *testing.T) { assertNotFoundHTML(t, serveArticle(t, s, id)) })
	}
}

func TestArticleHandlerErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name                         string
		articleErr, feedErr, siteErr error
		want                         int
	}{
		{"no record", rss.ErrNoRecord, nil, nil, http.StatusNotFound},
		{"article error", boom, nil, nil, http.StatusInternalServerError},
		{"feed error", nil, boom, nil, http.StatusInternalServerError},
		{"site error", nil, nil, boom, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := serveArticle(t, articleServer(t, rss.Article{}, tt.articleErr, tt.feedErr, tt.siteErr), testArticleID)
			if rr.Code != tt.want {
				t.Errorf("status = %d, want %d", rr.Code, tt.want)
			}
			if tt.want == http.StatusNotFound {
				assertNotFoundHTML(t, rr)
			}
		})
	}
}

func TestArticleRouteRequiresAuthentication(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/articles/"+testArticleID, nil)
	rr := httptest.NewRecorder()

	s.router().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestArticleHandlerMarksRead(t *testing.T) {
	var gotUser, gotArticle string
	calls := 0
	s := articleServer(t, rss.Article{Title: "T"}, nil, nil, nil)
	s.services.ReadService = &fakeReadStore{markReadFn: func(ctx context.Context, userID, articleID string) error {
		calls++
		gotUser, gotArticle = userID, articleID
		return nil
	}}

	rr := serveArticleAs(t, s, "user-1", testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if calls != 1 || gotUser != "user-1" || gotArticle != testArticleID {
		t.Errorf("MarkRead called %d times with (%q, %q), want once with (user-1, %q)", calls, gotUser, gotArticle, testArticleID)
	}
}

func TestArticleHandlerMarkReadErrorStillRenders(t *testing.T) {
	s := articleServer(t, rss.Article{Title: "Still here"}, nil, nil, nil)
	s.services.ReadService = &fakeReadStore{markReadFn: func(context.Context, string, string) error {
		return errors.New("db down")
	}}

	rr := serveArticleAs(t, s, "user-1", testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	assertBodyContains(t, rr.Body.String(), "<h1>Still here</h1>")
}

func TestArticleHandlerDoesNotMarkMissingArticles(t *testing.T) {
	tests := []struct {
		name string
		id   string
		err  error
	}{
		{"malformed ID", "abc", nil},
		{"unknown ID", testArticleID, rss.ErrNoRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := articleServer(t, rss.Article{}, tt.err, nil, nil)
			s.services.ReadService = &fakeReadStore{markReadFn: func(context.Context, string, string) error {
				t.Error("MarkRead was called")
				return nil
			}}

			assertNotFoundHTML(t, serveArticleAs(t, s, "user-1", tt.id))
		})
	}
}

// serveArticleAs is serveArticle for a logged-in user.
func serveArticleAs(t *testing.T, s *Server, userID, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/articles/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.articleHandler)).ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

// readStoreReporting returns a read store reporting readIDs as read, recording
// the user and IDs it was asked about.
func readStoreReporting(gotUser *string, gotIDs *[]string, readIDs ...string) *fakeReadStore {
	return &fakeReadStore{readArticleIDsFn: func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
		*gotUser, *gotIDs = userID, articleIDs
		read := map[string]bool{}
		for _, id := range readIDs {
			read[id] = true
		}
		return read, nil
	}}
}

// assertReadMarkers checks that the row for readID is marked read and the row
// for unreadID isn't.
func assertReadMarkers(t *testing.T, body, readID, unreadID string) {
	t.Helper()
	row := func(id string) string {
		start := strings.Index(body, `id="article-`+id+`"`)
		if start < 0 {
			t.Fatalf("no row for %s: %s", id, body)
		}
		start = strings.LastIndex(body[:start], "<article")
		end := strings.Index(body[start:], "</article>")
		return body[start : start+end]
	}
	read, unread := row(readID), row(unreadID)
	if !strings.Contains(read, `class="article-row read"`) || !strings.Contains(read, `<span class="visually-hidden"> (read)</span>`) {
		t.Errorf("read row isn't marked read: %s", read)
	}
	if strings.Contains(unread, " read\"") || strings.Contains(unread, "(read)") {
		t.Errorf("unread row is marked read: %s", unread)
	}
}
