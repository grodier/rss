package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

func searchRequest(t *testing.T, s *Server, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search?q="+q, nil)
	// scs needs a session in the context for PopString.
	ctx, err := s.sessionManager.Load(req.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.searchHandler(rr, req.WithContext(ctx))
	return rr
}

func failingSearchStore(t *testing.T) *fakeSearchStore {
	return &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
		t.Error("store must not be called")
		return nil, nil
	}}
}

func TestSearchNoQuery(t *testing.T) {
	s := newTestServerWith(t, Services{SearchService: failingSearchStore(t)})
	rr := searchRequest(t, s, "")
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `name="q"`) {
		t.Error("form missing")
	}
}

func TestSearchTooLong(t *testing.T) {
	s := newTestServerWith(t, Services{SearchService: failingSearchStore(t)})
	rr := searchRequest(t, s, strings.Repeat("a", 201))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Search must be 200 characters or fewer") {
		t.Error("error message missing")
	}
}

func TestSearchResults(t *testing.T) {
	var gotQ string
	var gotLimit int
	store := &fakeSearchStore{searchFn: func(_ context.Context, q string, limit int) ([]rss.SiteWithFeeds, error) {
		gotQ, gotLimit = q, limit
		return []rss.SiteWithFeeds{
			{Site: rss.Site{ID: "site-1", Host: "one.example", Title: "One"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}, {ID: "feed-2", Title: "F2"}}},
			{Site: rss.Site{ID: "site-2", Host: "two.example"}},
		}, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})
	rr := searchRequest(t, s, "ex")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if gotQ != "ex" || gotLimit != 20 {
		t.Errorf("Search called with %q, %d", gotQ, gotLimit)
	}
	body := rr.Body.String()
	for _, want := range []string{`href="/sites/site-1"`, `href="/sites/site-2"`, `href="/feeds/feed-1"`, `href="/feeds/feed-2"`, ">two.example</a>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSearchNoResults(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "zzz")
	if !strings.Contains(rr.Body.String(), "No sites or feeds match") {
		t.Error("empty-state message missing")
	}
}

func TestSearchStoreError(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, errors.New("boom")
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "ex")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestSearchEscapesQuery(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "%3Cscript%3E")
	if strings.Contains(rr.Body.String(), "<script>") {
		t.Error("query rendered unescaped")
	}
}

func TestSearchRequiresAuthentication(t *testing.T) {
	h := newTestServer(t).router()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Errorf("got %d %q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSearchHandlerSubscribedMarker(t *testing.T) {
	results := []rss.SiteWithFeeds{
		{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}, {ID: "feed-2", Title: "F2"}}},
		{Site: rss.Site{ID: "site-2", Host: "two.example"}, Feeds: []rss.Feed{{ID: "feed-3", Title: "F3"}}},
	}
	search := func(sites []rss.SiteWithFeeds) *fakeSearchStore {
		return &fakeSearchStore{searchFn: func(context.Context, string, int) ([]rss.SiteWithFeeds, error) {
			return sites, nil
		}}
	}
	// get searches as user-1.
	get := func(t *testing.T, s *Server) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/search?q=ex", nil)
		ctx, err := s.sessionManager.Load(req.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, authenticatedUserIDContextKey, "user-1")
		rr := httptest.NewRecorder()
		s.searchHandler(rr, req.WithContext(ctx))
		return rr
	}

	t.Run("marks subscribed feeds only", func(t *testing.T) {
		var gotUser string
		var gotIDs []string
		subs := &fakeSubscriptionStore{subscribedFeedIDsFn: func(_ context.Context, userID string, feedIDs []string) (map[string]bool, error) {
			gotUser, gotIDs = userID, feedIDs
			return map[string]bool{"feed-2": true}, nil
		}}
		s := newTestServerWith(t, Services{SearchService: search(results), SubscriptionService: subs})
		rr := get(t, s)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if gotUser != "user-1" {
			t.Errorf("SubscribedFeedIDs user = %q, want user-1", gotUser)
		}
		if strings.Join(gotIDs, ",") != "feed-1,feed-2,feed-3" {
			t.Errorf("SubscribedFeedIDs ids = %v, want [feed-1 feed-2 feed-3]", gotIDs)
		}
		body := rr.Body.String()
		if n := strings.Count(body, `class="subscribed"`); n != 1 {
			t.Errorf("found %d Subscribed markers, want 1", n)
		}
		if !strings.Contains(body, `F2</a> <span class="subscribed">Subscribed</span>`) {
			t.Error("subscribed feed F2 missing its marker")
		}
	})

	for _, tc := range []struct {
		name  string
		sites []rss.SiteWithFeeds
	}{
		{"no results", nil},
		{"no feeds", []rss.SiteWithFeeds{{Site: rss.Site{ID: "site-1", Host: "one.example"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subs := &fakeSubscriptionStore{subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
				t.Error("SubscribedFeedIDs must not be called")
				return nil, nil
			}}
			s := newTestServerWith(t, Services{SearchService: search(tc.sites), SubscriptionService: subs})
			if rr := get(t, s); rr.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rr.Code)
			}
		})
	}

	t.Run("store error", func(t *testing.T) {
		subs := &fakeSubscriptionStore{subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("boom")
		}}
		s := newTestServerWith(t, Services{SearchService: search(results), SubscriptionService: subs})
		if rr := get(t, s); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}
