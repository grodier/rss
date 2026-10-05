package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const testLookupID = "33333333-3333-4333-8333-333333333333"

func postLookup(t *testing.T, s *Server, q string) *httptest.ResponseRecorder {
	t.Helper()
	return postLookupAs(t, s, "user-1", q)
}

func postLookupAs(t *testing.T, s *Server, userID, q string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"q": {q}}
	req := httptest.NewRequest(http.MethodPost, "/lookups", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, userID))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.lookupCreateHandler)).ServeHTTP(rr, req)
	return rr
}

func serveLookup(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/lookups/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.lookupHandler)).ServeHTTP(rr, req)
	return rr
}

func lookupServer(t *testing.T, store *fakeLookupStore, wait time.Duration) *Server {
	t.Helper()
	s := newTestServerWith(t, Services{LookupService: store})
	s.lookupWait = wait
	return s
}

func requestReturns(l rss.Lookup, err error) func(context.Context, string, string, time.Duration, time.Duration) (rss.Lookup, error) {
	return func(context.Context, string, string, time.Duration, time.Duration) (rss.Lookup, error) {
		return l, err
	}
}

func noGetByID(t *testing.T) func(context.Context, string) (rss.Lookup, error) {
	return func(context.Context, string) (rss.Lookup, error) {
		t.Error("GetByID must not be called")
		return rss.Lookup{}, nil
	}
}

func assertRedirect(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
		t.Errorf("got %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), want)
	}
}

func TestLookupCreateInvalidInput(t *testing.T) {
	store := &fakeLookupStore{
		requestFn: func(context.Context, string, string, time.Duration, time.Duration) (rss.Lookup, error) {
			t.Error("Request must not be called")
			return rss.Lookup{}, nil
		},
		getByIDFn: noGetByID(t),
	}
	rr := postLookup(t, lookupServer(t, store, 0), "hacker news")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Enter a website address like example.com") {
		t.Error("error message missing")
	}
}

func TestLookupCreateRequestArgs(t *testing.T) {
	var gotKey, gotURL string
	var gotDone, gotFailed time.Duration
	store := &fakeLookupStore{
		requestFn: func(_ context.Context, siteKey, url string, doneTTL, failedTTL time.Duration) (rss.Lookup, error) {
			gotKey, gotURL, gotDone, gotFailed = siteKey, url, doneTTL, failedTTL
			return rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, nil
		},
		getByIDFn: noGetByID(t),
	}
	postLookup(t, lookupServer(t, store, 0), "Example.com")
	if gotKey != "example.com" || gotURL != "https://example.com/" {
		t.Errorf("Request called with %q, %q", gotKey, gotURL)
	}
	if gotDone != 24*time.Hour || gotFailed != time.Hour {
		t.Errorf("TTLs = %v, %v, want 24h, 1h", gotDone, gotFailed)
	}
}

func TestLookupCreateCacheHit(t *testing.T) {
	store := &fakeLookupStore{
		requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupDone, SiteID: testSiteID}, nil),
		getByIDFn: noGetByID(t),
	}
	rr := postLookup(t, lookupServer(t, store, time.Second), "example.com")
	assertRedirect(t, rr, "/sites/"+testSiteID)
}

func TestLookupCreateFinishesWhileWaiting(t *testing.T) {
	calls := 0
	store := &fakeLookupStore{
		requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, nil),
		getByIDFn: func(_ context.Context, id string) (rss.Lookup, error) {
			calls++
			if id != testLookupID {
				t.Errorf("GetByID(%q)", id)
			}
			if calls < 2 {
				return rss.Lookup{ID: testLookupID, Status: rss.LookupRunning}, nil
			}
			return rss.Lookup{ID: testLookupID, Status: rss.LookupDone, SiteID: testSiteID}, nil
		},
	}
	rr := postLookup(t, lookupServer(t, store, time.Second), "example.com")
	assertRedirect(t, rr, "/sites/"+testSiteID)
	if calls != 2 {
		t.Errorf("GetByID called %d times, want 2", calls)
	}
}

func TestLookupCreateStillPending(t *testing.T) {
	store := &fakeLookupStore{
		requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, nil),
		getByIDFn: noGetByID(t),
	}
	rr := postLookup(t, lookupServer(t, store, 0), "example.com")
	assertRedirect(t, rr, "/lookups/"+testLookupID)
}

func TestLookupCreateFailedRedirectsToStatus(t *testing.T) {
	store := &fakeLookupStore{
		requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupFailed}, nil),
		getByIDFn: noGetByID(t),
	}
	rr := postLookup(t, lookupServer(t, store, time.Second), "example.com")
	assertRedirect(t, rr, "/lookups/"+testLookupID)
}

func TestLookupCreateStopsWhenRequestCanceled(t *testing.T) {
	store := &fakeLookupStore{
		requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, nil),
		getByIDFn: noGetByID(t),
	}
	s := lookupServer(t, store, time.Minute)
	form := url.Values{"q": {"example.com"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/lookups", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	start := time.Now()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.lookupCreateHandler)).ServeHTTP(rr, req)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("handler took %v after the request was canceled", d)
	}
}

func TestLookupCreateStoreErrors(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeLookupStore
	}{
		{"Request", &fakeLookupStore{
			requestFn: requestReturns(rss.Lookup{}, errors.New("boom")),
			getByIDFn: noGetByID(t),
		}},
		{"GetByID", &fakeLookupStore{
			requestFn: requestReturns(rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, nil),
			getByIDFn: func(context.Context, string) (rss.Lookup, error) { return rss.Lookup{}, errors.New("boom") },
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postLookup(t, lookupServer(t, tt.store, time.Second), "example.com")
			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rr.Code)
			}
		})
	}
}

func lookupGetServer(t *testing.T, l rss.Lookup, err error) *Server {
	t.Helper()
	return lookupServer(t, &fakeLookupStore{
		getByIDFn: func(context.Context, string) (rss.Lookup, error) { return l, err },
	}, 0)
}

func TestLookupHandlerNotFound(t *testing.T) {
	t.Run("malformed id", func(t *testing.T) {
		s := lookupServer(t, &fakeLookupStore{getByIDFn: noGetByID(t)}, 0)
		if rr := serveLookup(t, s, "not-a-uuid"); rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
	t.Run("no record", func(t *testing.T) {
		s := lookupGetServer(t, rss.Lookup{}, rss.ErrNoRecord)
		if rr := serveLookup(t, s, testLookupID); rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}

func TestLookupHandlerStoreError(t *testing.T) {
	s := lookupGetServer(t, rss.Lookup{}, errors.New("boom"))
	if rr := serveLookup(t, s, testLookupID); rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestLookupHandlerInProgress(t *testing.T) {
	for _, status := range []rss.LookupStatus{rss.LookupPending, rss.LookupRunning} {
		t.Run(string(status), func(t *testing.T) {
			s := lookupGetServer(t, rss.Lookup{ID: testLookupID, SiteKey: "example.com", Status: status}, nil)
			rr := serveLookup(t, s, testLookupID)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			body := rr.Body.String()
			for _, want := range []string{`http-equiv="refresh"`, "Looking for feeds on example.com", `href="/lookups/` + testLookupID + `"`} {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
		})
	}
}

func TestLookupHandlerDoneWithSite(t *testing.T) {
	s := lookupGetServer(t, rss.Lookup{ID: testLookupID, Status: rss.LookupDone, SiteID: testSiteID}, nil)
	assertRedirect(t, serveLookup(t, s, testLookupID), "/sites/"+testSiteID)
}

func TestLookupHandlerDoneNoFeeds(t *testing.T) {
	s := lookupGetServer(t, rss.Lookup{ID: testLookupID, SiteKey: "example.com", Status: rss.LookupDone}, nil)
	rr := serveLookup(t, s, testLookupID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "No feeds found on example.com") {
		t.Error("no-feeds message missing")
	}
	if !strings.Contains(body, `href="/search"`) {
		t.Error("link back to search missing")
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("finished lookup must not refresh")
	}
}

func TestLookupHandlerFailed(t *testing.T) {
	s := lookupGetServer(t, rss.Lookup{ID: testLookupID, SiteKey: "example.com", Status: rss.LookupFailed, Error: "dial tcp: secret detail"}, nil)
	rr := serveLookup(t, s, testLookupID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "couldn't reach example.com") {
		t.Error("failure message missing")
	}
	if strings.Contains(body, "secret detail") {
		t.Error("page shows the internal error")
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("finished lookup must not refresh")
	}
}

func TestSearchOffersLookup(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})

	body := searchRequest(t, s, "example.com").Body.String()
	for _, want := range []string{`action="/lookups"`, `name="q" value="example.com"`, "Look up example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	if body := searchRequest(t, s, "hacker+news").Body.String(); strings.Contains(body, `action="/lookups"`) {
		t.Error("lookup offered for a non-URL query")
	}
}

func TestLookupCrossOriginPost(t *testing.T) {
	h := newTestServer(t).router()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/lookups", strings.NewReader("q=example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestLookupCreateRateLimited(t *testing.T) {
	calls := 0
	store := &fakeLookupStore{
		requestFn: func(context.Context, string, string, time.Duration, time.Duration) (rss.Lookup, error) {
			calls++
			return rss.Lookup{ID: testLookupID, Status: rss.LookupDone, SiteID: "site-1"}, nil
		},
	}
	s := lookupServer(t, store, 0)

	for i := 1; i <= 10; i++ {
		if rr := postLookupAs(t, s, "user-1", "example.com"); rr.Code != http.StatusSeeOther {
			t.Fatalf("request %d: status = %d, want 303", i, rr.Code)
		}
	}
	rr := postLookupAs(t, s, "user-1", "example.com")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
	secs, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 600 {
		t.Errorf("Retry-After = %q, want 1-600 seconds", rr.Header().Get("Retry-After"))
	}
	if calls != 10 {
		t.Errorf("Request called %d times, want 10", calls)
	}

	if rr := postLookupAs(t, s, "user-2", "example.com"); rr.Code != http.StatusSeeOther {
		t.Errorf("other user: status = %d, want 303", rr.Code)
	}
}
