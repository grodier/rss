package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	return newTestServerWith(t, Services{
		FeedService: &fakeFeedStore{
			getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
				return rss.Feed{
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

func TestFeedsHandlerLinksToFeedPage(t *testing.T) {
	const otherID = "22222222-2222-4222-8222-222222222222"
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		getLatestFn: func(ctx context.Context) ([]rss.Feed, error) {
			return []rss.Feed{
				{ID: testFeedID, Title: "Example Feed", Description: "About the feed", Url: "https://example.org/feed.xml"},
				{ID: otherID, Url: "https://example.org/empty.xml"},
			}, nil
		},
	}})
	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rr := httptest.NewRecorder()

	s.feedsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`href="/feeds/` + testFeedID + `"`,
		"Example Feed",
		`href="/feeds/` + otherID + `"`,
		`>https://example.org/empty.xml</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<p></p>") {
		t.Errorf("body contains an empty paragraph: %s", body)
	}
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
