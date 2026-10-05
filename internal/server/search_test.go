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
	s := newTestServerWith(t, Services{SearchService: store})
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
