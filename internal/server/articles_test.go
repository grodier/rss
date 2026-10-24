package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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
		SaveService: &fakeSaveStore{},
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
		`<time data-local datetime="2024-03-02T10:05:00Z">2 Mar 2024 10:05 UTC</time>`,
		`<script src="/static/js/app.js?v=`+jsVersion+`" defer></script>`,
		`<div class="article-body">`,
		"<strong>world</strong>",
		`src="https://example.com/pic.png"`,
		`href="https://example.com/posts/a" target="_blank" rel="noopener noreferrer" title="Read on Example Site">Read on <span class="btn-label">Example Site</span> ↗`,
	)
	for _, bad := range []string{"<script>", "CONTENT-MARKER", "SUMMARY-MARKER", "<b>title</b>"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q: %s", bad, body)
		}
	}
}

func TestArticleHandlerDateOnly(t *testing.T) {
	day := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC).In(time.FixedZone("EDT", -4*60*60))
	rr := serveArticle(t, articleServer(t, rss.Article{Title: "T", PublishedAt: day, PublishedDateOnly: true}, nil, nil, nil), testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body, `<time datetime="2026-10-09">9 Oct 2026</time>`)
	if strings.Contains(body, "data-local") || strings.Contains(body, "00:00 UTC") {
		t.Errorf("date-only date is localized or shows a time: %s", body)
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

func TestArticleHandlerPageDescriptionWhenNoText(t *testing.T) {
	rr := serveArticle(t, articleServer(t, rss.Article{Title: "T", PageDescription: "From the <page> & more"}, nil, nil, nil), testArticleID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body, `<div class="article-body"><p>From the &lt;page&gt; &amp; more</p></div>`)
	if strings.Contains(body, "This article has no text in its feed.") {
		t.Errorf("body shows the no-text message: %s", body)
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

func TestArticleHandlerLeadImage(t *testing.T) {
	const image = "https://example.com/lead.jpg"
	tests := []struct {
		name    string
		article rss.Article
		want    bool
	}{
		{name: "image only in metadata", article: rss.Article{Title: "T", ImageURL: image, Content: "<p>Text</p>"}, want: true},
		{name: "content starts with the image", article: rss.Article{Title: "T", ImageURL: image, Content: `<img src="/lead.jpg"><p>Text</p>`}, want: false},
		{name: "page image when the feed has none", article: rss.Article{Title: "T", PageImageURL: image, Content: "<p>Text</p>"}, want: true},
		{name: "no image", article: rss.Article{Title: "T", Content: "<p>Text</p>"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := serveArticle(t, articleServer(t, tt.article, nil, nil, nil), testArticleID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()
			if tt.want {
				assertBodyContains(t, body, `<img class="reader-image" src="`+image+`" alt="" referrerpolicy="no-referrer">`)
			} else if strings.Contains(body, "reader-image") {
				t.Errorf("body has a lead image: %s", body)
			}
		})
	}
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

func postArticleRead(t *testing.T, s *Server, userID, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/articles/x/read", strings.NewReader("PING"))
	req.Header.Set("Content-Type", "text/ping")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.articleReadHandler)).ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func TestArticleReadHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotUser, gotArticle string
		calls := 0
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markReadFn: func(ctx context.Context, userID, articleID string) error {
			calls++
			gotUser, gotArticle = userID, articleID
			return nil
		}}})

		rr := postArticleRead(t, s, "user-1", testArticleID)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rr.Body.String())
		}
		if calls != 1 || gotUser != "user-1" || gotArticle != testArticleID {
			t.Errorf("MarkRead called %d times with (%q, %q), want once with (user-1, %q)", calls, gotUser, gotArticle, testArticleID)
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		s := newTestServer(t) // nil stores: any store call would panic
		assertNotFoundHTML(t, postArticleRead(t, s, "user-1", "abc"))
	})

	t.Run("unknown article", func(t *testing.T) {
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markReadFn: func(context.Context, string, string) error {
			return rss.ErrNoRecord
		}}})
		assertNotFoundHTML(t, postArticleRead(t, s, "user-1", testArticleID))
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markReadFn: func(context.Context, string, string) error {
			return errors.New("db down")
		}}})
		rr := postArticleRead(t, s, "user-1", testArticleID)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("logged out", func(t *testing.T) {
		s := newTestServer(t)
		req := httptest.NewRequest(http.MethodPost, "/articles/"+testArticleID+"/read", nil)
		rr := httptest.NewRecorder()

		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}
	})

	t.Run("cross-origin", func(t *testing.T) {
		s := newTestServer(t)
		req := httptest.NewRequest(http.MethodPost, "/articles/"+testArticleID+"/read", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()

		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
	})
}

// assertSaveButtons checks that body has a Save form for savedID showing it
// saved and one for unsavedID showing it not saved, both returning to
// returnTo.
func assertSaveButtons(t *testing.T, body, returnTo, savedID, unsavedID string) {
	t.Helper()
	forms := saveForms(t, body)
	want := map[string]toggleFormMarkup{
		savedID:   {Action: "/articles/" + savedID + "/unsave", Pressed: "true", ReturnTo: returnTo},
		unsavedID: {Action: "/articles/" + unsavedID + "/save", Pressed: "false", ReturnTo: returnTo},
	}
	for id, w := range want {
		if got, ok := forms[id]; !ok {
			t.Errorf("no Save form for %s: %s", id, body)
		} else if got != w {
			t.Errorf("Save form for %s = %+v, want %+v", id, got, w)
		}
	}
}

// assertStarButtons checks that body has a Star form for starredID showing
// it starred and one for unstarredID showing it not starred, both returning
// to returnTo.
func assertStarButtons(t *testing.T, body, returnTo, starredID, unstarredID string) {
	t.Helper()
	forms := starForms(t, body)
	want := map[string]toggleFormMarkup{
		starredID:   {Action: "/articles/" + starredID + "/unstar", Pressed: "true", ReturnTo: returnTo},
		unstarredID: {Action: "/articles/" + unstarredID + "/star", Pressed: "false", ReturnTo: returnTo},
	}
	for id, w := range want {
		if got, ok := forms[id]; !ok {
			t.Errorf("no Star form for %s: %s", id, body)
		} else if got != w {
			t.Errorf("Star form for %s = %+v, want %+v", id, got, w)
		}
	}
}

// starStoreReporting returns a StarStore whose StarredArticleIDs records its
// arguments and reports starredIDs as starred.
func starStoreReporting(gotUser *string, gotIDs *[]string, starredIDs ...string) *fakeStarStore {
	return &fakeStarStore{starredArticleIDsFn: func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
		*gotUser, *gotIDs = userID, articleIDs
		starred := map[string]bool{}
		for _, id := range starredIDs {
			starred[id] = true
		}
		return starred, nil
	}}
}

// saveStoreReporting returns a SaveStore whose SavedArticleIDs records its
// arguments and reports savedIDs as saved.
func saveStoreReporting(gotUser *string, gotIDs *[]string, savedIDs ...string) *fakeSaveStore {
	return &fakeSaveStore{savedArticleIDsFn: func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
		*gotUser, *gotIDs = userID, articleIDs
		saved := map[string]bool{}
		for _, id := range savedIDs {
			saved[id] = true
		}
		return saved, nil
	}}
}

func TestArticleHandlerSaved(t *testing.T) {
	for _, saved := range []bool{true, false} {
		t.Run(fmt.Sprintf("saved=%v", saved), func(t *testing.T) {
			s := articleServer(t, rss.Article{Title: "T"}, nil, nil, nil)
			var gotUser string
			var gotIDs []string
			var ids []string
			if saved {
				ids = []string{testArticleID}
			}
			s.services.SaveService = saveStoreReporting(&gotUser, &gotIDs, ids...)

			rr := serveArticle(t, s, testArticleID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if !slices.Equal(gotIDs, []string{testArticleID}) {
				t.Errorf("SavedArticleIDs(%q, %v), want [%s]", gotUser, gotIDs, testArticleID)
			}
			want := toggleFormMarkup{Action: "/articles/" + testArticleID + "/save", Pressed: "false", ReturnTo: "/articles/x"}
			if saved {
				want.Action, want.Pressed = "/articles/"+testArticleID+"/unsave", "true"
			}
			forms := saveForms(t, rr.Body.String())
			if got := forms[testArticleID]; len(forms) != 1 || got != want {
				t.Errorf("Save forms = %+v, want only %+v", forms, want)
			}
		})
	}

	t.Run("SavedArticleIDs error is 500", func(t *testing.T) {
		s := articleServer(t, rss.Article{Title: "T"}, nil, nil, nil)
		s.services.SaveService = &fakeSaveStore{savedArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("db down")
		}}

		if rr := serveArticle(t, s, testArticleID); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestArticleHandlerStarred(t *testing.T) {
	for _, starred := range []bool{true, false} {
		t.Run(fmt.Sprintf("starred=%v", starred), func(t *testing.T) {
			s := articleServer(t, rss.Article{Title: "T"}, nil, nil, nil)
			var gotUser string
			var gotIDs []string
			var ids []string
			if starred {
				ids = []string{testArticleID}
			}
			s.services.StarService = starStoreReporting(&gotUser, &gotIDs, ids...)

			rr := serveArticle(t, s, testArticleID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if !slices.Equal(gotIDs, []string{testArticleID}) {
				t.Errorf("StarredArticleIDs(%q, %v), want [%s]", gotUser, gotIDs, testArticleID)
			}
			want := toggleFormMarkup{Action: "/articles/" + testArticleID + "/star", Pressed: "false", ReturnTo: "/articles/x"}
			if starred {
				want.Action, want.Pressed = "/articles/"+testArticleID+"/unstar", "true"
			}
			forms := starForms(t, rr.Body.String())
			if got := forms[testArticleID]; len(forms) != 1 || got != want {
				t.Errorf("Star forms = %+v, want only %+v", forms, want)
			}
			// The Star button is icon-only, so its aria-label names it.
			assertBodyContains(t, rr.Body.String(), `aria-label="Star"`)
		})
	}

	t.Run("StarredArticleIDs error is 500", func(t *testing.T) {
		s := articleServer(t, rss.Article{Title: "T"}, nil, nil, nil)
		s.services.StarService = &fakeStarStore{starredArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("db down")
		}}

		if rr := serveArticle(t, s, testArticleID); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func postSave(t *testing.T, s *Server, handler http.HandlerFunc, userID, id, returnTo string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"return_to": {returnTo}}
	req := httptest.NewRequest(http.MethodPost, "/articles/x/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(handler).ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func TestArticleSaveHandler(t *testing.T) {
	testToggleAction(t, "save", func(s *Server) http.HandlerFunc { return s.articleSaveHandler },
		func(fn func(ctx context.Context, userID, articleID string) error) Services {
			return Services{SaveService: &fakeSaveStore{saveFn: fn}}
		})
}

func TestArticleUnsaveHandler(t *testing.T) {
	testToggleAction(t, "unsave", func(s *Server) http.HandlerFunc { return s.articleUnsaveHandler },
		func(fn func(ctx context.Context, userID, articleID string) error) Services {
			return Services{SaveService: &fakeSaveStore{unsaveFn: fn}}
		})
}

func TestArticleStarHandler(t *testing.T) {
	testToggleAction(t, "star", func(s *Server) http.HandlerFunc { return s.articleStarHandler },
		func(fn func(ctx context.Context, userID, articleID string) error) Services {
			return Services{StarService: &fakeStarStore{starFn: fn}}
		})
}

func TestArticleUnstarHandler(t *testing.T) {
	testToggleAction(t, "unstar", func(s *Server) http.HandlerFunc { return s.articleUnstarHandler },
		func(fn func(ctx context.Context, userID, articleID string) error) Services {
			return Services{StarService: &fakeStarStore{unstarFn: fn}}
		})
}

// testToggleAction tests the save, unsave, star or unstar handler (path is
// "save", "unsave", "star" or "unstar"); services returns the services with
// fn as the store method the handler should call.
func testToggleAction(t *testing.T, path string, handler func(*Server) http.HandlerFunc, services func(fn func(ctx context.Context, userID, articleID string) error) Services) {
	t.Helper()
	newServer := func(fn func(ctx context.Context, userID, articleID string) error) *Server {
		return newTestServerWith(t, services(fn))
	}

	t.Run("redirects to return_to at the row", func(t *testing.T) {
		var gotUser, gotArticle string
		calls := 0
		s := newServer(func(ctx context.Context, userID, articleID string) error {
			calls++
			gotUser, gotArticle = userID, articleID
			return nil
		})

		rr := postSave(t, s, handler(s), "user-1", testArticleID, "/feeds/"+testFeedID+"?before=abc")

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if want := "/feeds/" + testFeedID + "?before=abc#article-" + testArticleID; rr.Header().Get("Location") != want {
			t.Errorf("Location = %q, want %q", rr.Header().Get("Location"), want)
		}
		if calls != 1 || gotUser != "user-1" || gotArticle != testArticleID {
			t.Errorf("store called %d times with (%q, %q), want once with (user-1, %q)", calls, gotUser, gotArticle, testArticleID)
		}
		// Saving and starring are quiet: a flash would have written the
		// session cookie.
		if c := rr.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("Set-Cookie = %q, want none (no flash)", c)
		}
	})

	for _, returnTo := range []string{"https://evil.example/", "//evil.example/", ""} {
		t.Run("off-site return_to "+returnTo, func(t *testing.T) {
			s := newServer(nil)
			rr := postSave(t, s, handler(s), "user-1", testArticleID, returnTo)
			if loc := rr.Header().Get("Location"); rr.Code != http.StatusSeeOther || loc != "/#article-"+testArticleID {
				t.Errorf("status %d, Location = %q, want 303 to /#article-%s", rr.Code, loc, testArticleID)
			}
		})
	}

	t.Run("a fragment in return_to is replaced", func(t *testing.T) {
		s := newServer(nil)
		rr := postSave(t, s, handler(s), "user-1", testArticleID, "/#article-other")
		if loc := rr.Header().Get("Location"); loc != "/#article-"+testArticleID {
			t.Errorf("Location = %q, want /#article-%s", loc, testArticleID)
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		s := newServer(func(context.Context, string, string) error {
			t.Error("store called for a malformed id")
			return nil
		})
		assertNotFoundHTML(t, postSave(t, s, handler(s), "user-1", "abc", "/"))
	})

	t.Run("unknown article", func(t *testing.T) {
		s := newServer(func(context.Context, string, string) error { return rss.ErrNoRecord })
		assertNotFoundHTML(t, postSave(t, s, handler(s), "user-1", testArticleID, "/"))
	})

	t.Run("store error", func(t *testing.T) {
		s := newServer(func(context.Context, string, string) error { return errors.New("db down") })
		if rr := postSave(t, s, handler(s), "user-1", testArticleID, "/"); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("logged out", func(t *testing.T) {
		s := newTestServer(t)
		req := httptest.NewRequest(http.MethodPost, "/articles/"+testArticleID+"/"+path, nil)
		rr := httptest.NewRecorder()

		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}
	})
}

func TestArticleHandlerSiteIcon(t *testing.T) {
	s := articleServer(t, rss.Article{Title: "Hello"}, nil, nil, nil)
	testSiteIcons(t, s, testSiteID, "E", func(t *testing.T) string {
		rr := serveArticle(t, s, testArticleID)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}

func TestReadOnLabel(t *testing.T) {
	example := rss.Site{Host: "example.com", Title: "Example Site"}
	tests := []struct {
		name string
		url  string
		site rss.Site
		want string
	}{
		{"same host", "https://example.com/a", example, "Example Site"},
		{"www", "https://www.example.com/a", example, "Example Site"},
		{"article on a subdomain", "https://blog.example.com/a", example, "Example Site"},
		{"site on a subdomain", "https://ycombinator.com/a", rss.Site{Host: "news.ycombinator.com", Title: "Hacker News"}, "Hacker News"},
		{"another site", "https://www.nytimes.com/2026/a.html", example, "nytimes.com"},
		{"suffix that isn't a subdomain", "https://notexample.com/a", example, "notexample.com"},
		{"uppercase host", "https://EXAMPLE.com/a", example, "Example Site"},
		{"no title", "https://example.com/a", rss.Site{Host: "example.com"}, "example.com"},
		{"not http", "mailto:x@example.org", example, "Example Site"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readOnLabel(rss.Article{URL: tt.url}, tt.site); got != tt.want {
				t.Errorf("readOnLabel(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestArticleHandlerReadOnOtherSite(t *testing.T) {
	s := articleServer(t, rss.Article{Title: "A", URL: "https://www.nytimes.com/a"}, nil, nil, nil)
	s.services.SiteService.(*fakeSiteStore).getByIDFn = func(ctx context.Context, id string) (rss.Site, error) {
		return rss.Site{ID: id, Host: "news.ycombinator.com", Title: "Hacker News"}, nil
	}

	rr := serveArticle(t, s, testArticleID)

	assertBodyContains(t, rr.Body.String(), `Read on <span class="btn-label">nytimes.com</span> ↗`)
}
