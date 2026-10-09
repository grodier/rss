package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+id, nil)
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
		listByFeedFn: func(ctx context.Context, feedID string, limit int) ([]rss.Article, error) {
			gotFeedID, gotLimit = feedID, limit
			return []rss.Article{
				{
					Title:       "A <b>bold</b> title",
					URL:         "https://example.com/a",
					PublishedAt: time.Date(2024, time.March, 2, 10, 0, 0, 0, time.UTC),
					Summary:     "<script>SUMMARY-MARKER</script>",
					Content:     "<script>SUMMARY-MARKER</script>",
				},
				{URL: "https://example.com/b"},
				{Title: "No link title"},
			}, nil
		},
	})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotFeedID != testFeedID || gotLimit != 50 {
		t.Errorf("ListByFeed(%q, %d), want (%q, 50)", gotFeedID, gotLimit, testFeedID)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`href="https://example.com/a"`,
		"A &lt;b&gt;bold&lt;/b&gt; title",
		"(untitled)",
		`href="https://example.com/b"`,
		"No link title",
		"2 Mar 2024",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
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
	if strings.Contains(body, ">No link title</a>") {
		t.Errorf("article without a URL was linked: %s", body)
	}
}

func TestFeedHandlerNoArticles(t *testing.T) {
	rr := serveFeed(t, feedServer(t, nil), testFeedID)

	if !strings.Contains(rr.Body.String(), "No articles yet.") {
		t.Errorf("body does not contain %q: %s", "No articles yet.", rr.Body.String())
	}
}

func TestFeedHandlerArticlesError(t *testing.T) {
	s := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, limit int) ([]rss.Article, error) {
			return nil, errors.New("boom")
		},
	})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestFeedHandlerSiteError(t *testing.T) {
	rr := serveFeed(t, feedServer(t, errors.New("boom")), testFeedID)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
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

	t.Run("groups feeds under their site", func(t *testing.T) {
		rr, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}, LatestArticleAt: latest},
			rss.Subscription{Site: a, Feed: rss.Feed{ID: otherID, Url: "https://a.example.com/two.xml"}},
			rss.Subscription{Site: b, Feed: rss.Feed{ID: "33333333-3333-4333-8333-333333333333", Title: "Three"}},
		))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertContains(t, body,
			"<h1>My feeds</h1>",
			`<a href="/sites/`+siteA+`">Site A</a>`,
			`<a href="/sites/`+siteB+`">b.example.com</a>`,
			`<a href="/feeds/`+testFeedID+`">One</a>`,
			`<a href="/feeds/`+otherID+`">https://a.example.com/two.xml</a>`,
		)
		if n := strings.Count(body, "<h2>"); n != 2 {
			t.Errorf("got %d sites, want 2: %s", n, body)
		}
		if strings.Index(body, "One") > strings.Index(body, "Three") {
			t.Errorf("site A's feeds should come before site B's: %s", body)
		}
	})

	t.Run("latest article", func(t *testing.T) {
		_, body := getFeeds(t, listing(
			rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}, LatestArticleAt: latest},
			rss.Subscription{Site: a, Feed: rss.Feed{ID: otherID, Title: "Two"}},
		))
		assertContains(t, body, "Latest article 4 Mar 2026", "No articles yet.")
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

	t.Run("unsubscribe form", func(t *testing.T) {
		_, body := getFeeds(t, listing(rss.Subscription{Site: a, Feed: rss.Feed{ID: testFeedID, Title: "One"}}))
		assertContains(t, body,
			`action="/feeds/`+testFeedID+`/unsubscribe"`,
			`name="return_to" value="/feeds"`,
		)
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
	}{
		{"never fetched", time.Time{}, []string{"Never fetched."}},
		{"fetched", time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), []string{"Last fetched 2 Jan 2026 15:04 UTC."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
				ArticleService:      &fakeArticleStore{},
				FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
					return rss.Feed{ID: id, Title: "T", Url: "https://example.com/feed.xml", SiteID: testSiteID, LastFetched: tt.lastFetched}, nil
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
		})
	}
}

func TestFeedHandlerFetchStatus(t *testing.T) {
	attempt := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	next := time.Date(2026, 1, 3, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		failures int
		want     []string
		notWant  []string
	}{
		{"no failures", 0, []string{"Next check around 3 Jan 2026 09:30 UTC."}, []string{"fetch-error"}},
		{"one failure", 1, []string{
			"The last attempt to fetch this feed failed (2 Jan 2026 15:04 UTC). We'll keep trying.",
			"Next check around 3 Jan 2026 09:30 UTC.",
		}, []string{"The last 1 attempts"}},
		{"three failures", 3, []string{
			"The last 3 attempts to fetch this feed failed, most recently 2 Jan 2026 15:04 UTC.",
			"Next check around 3 Jan 2026 09:30 UTC.",
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{
				SubscriptionService: &fakeSubscriptionStore{},
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

func TestFeedRefreshRouteRequiresAuthentication(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/feeds/"+testFeedID+"/refresh", nil)
	rr := httptest.NewRecorder()

	s.router().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
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

func TestSubscriptionRoutesRequireAuthentication(t *testing.T) {
	s := newTestServer(t)
	for _, action := range []string{"subscribe", "unsubscribe"} {
		t.Run(action, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/feeds/"+testFeedID+"/"+action, nil)
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
				unsubscribeForm, "Subscribed ✓",
			},
		},
		{
			name:       "subscribed",
			feed:       rss.Feed{Title: "Example Feed", Url: "https://example.com/feed.xml"},
			subscribed: true,
			want:       []string{unsubscribeForm, "Subscribed ✓", `aria-label="Unsubscribe from Example Feed"`},
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
			var gotUser string
			var gotIDs []string
			s := subscriptionServer(t, tt.feed, nil, &fakeSubscriptionStore{
				subscribedFeedIDsFn: func(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
					gotUser, gotIDs = userID, feedIDs
					return map[string]bool{testFeedID: tt.subscribed}, nil
				},
			})
			s.services.ArticleService = &fakeArticleStore{}
			s.services.SiteService = &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
				return rss.Site{ID: id, Host: "example.com"}, nil
			}}

			rr := serveFeedAs(t, s, "user-1", testFeedID)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if gotUser != "user-1" || len(gotIDs) != 1 || gotIDs[0] != testFeedID {
				t.Errorf("SubscribedFeedIDs(%q, %v), want (user-1, [%s])", gotUser, gotIDs, testFeedID)
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

func TestFeedHandlerSubscribedFeedIDsError(t *testing.T) {
	s := feedServer(t, nil)
	s.services.SubscriptionService = &fakeSubscriptionStore{
		subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("db down")
		},
	}

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
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
