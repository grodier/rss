package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

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
	return &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
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
	var gotQ, gotKey string
	var gotLimit int
	store := &fakeSearchStore{searchFn: func(_ context.Context, q, siteKey string, limit int) ([]rss.SiteWithFeeds, error) {
		gotQ, gotKey, gotLimit = q, siteKey, limit
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
	if gotQ != "ex" || gotKey != "" || gotLimit != 20 {
		t.Errorf("Search called with %q, %q, %d", gotQ, gotKey, gotLimit)
	}
	body := rr.Body.String()
	for _, want := range []string{`href="/sites/site-1"`, `href="/sites/site-2"`, `href="/feeds/feed-1"`, `href="/feeds/feed-2"`, ">two.example</a>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	// An address is also matched by its site key; the query stays as typed.
	searchRequest(t, s, "https://www.NPR.org/news")
	if gotQ != "https://www.NPR.org/news" || gotKey != "npr.org" {
		t.Errorf("Search called with %q, %q; want the query as typed and site key npr.org", gotQ, gotKey)
	}
}

// TestLiveSearchMarkup checks the markup app.js's live search relies on, in
// every state of the page, since a page in any state can be typed into: the
// app.js script, a GET
// form[data-live-search] to /search with a q input, and one
// [data-search-results] container holding the lookup offer, the results
// (.result) and the no-results message, and empty with no query.
func TestLiveSearchMarkup(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(_ context.Context, q, _ string, _ int) ([]rss.SiteWithFeeds, error) {
		if q == "zzz" {
			return nil, nil
		}
		return []rss.SiteWithFeeds{
			{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}}},
			{Site: rss.Site{ID: "site-2", Host: "two.example"}},
		}, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})

	tests := []struct {
		name, q    string
		status     int
		results    int  // .result sections in the container
		lookup     bool // the lookup offer is in the container
		empty      bool // the no-results message is in the container
		noChildren bool // the container has no elements
	}{
		{name: "no query", q: "", status: http.StatusOK, noChildren: true},
		{name: "results", q: "ex", status: http.StatusOK, results: 2},
		{name: "address", q: "npr.org", status: http.StatusOK, results: 2, lookup: true},
		{name: "no results", q: "zzz", status: http.StatusOK, empty: true},
		{name: "invalid query", q: strings.Repeat("a", 201), status: http.StatusUnprocessableEntity, noChildren: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := searchRequest(t, s, tt.q)
			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d", rr.Code, tt.status)
			}
			doc, err := html.Parse(rr.Body)
			if err != nil {
				t.Fatal(err)
			}

			scripts := findAll(doc, func(n *html.Node) bool {
				return n.Data == "script" && strings.Contains(attr(n, "src"), "js/app.") && hasAttr(n, "defer")
			})
			if len(scripts) != 1 {
				t.Errorf("found %d deferred app.js scripts, want 1", len(scripts))
			}

			forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-live-search") })
			if len(forms) != 1 {
				t.Fatalf("found %d form[data-live-search], want 1", len(forms))
			}
			if a, m := attr(forms[0], "action"), attr(forms[0], "method"); a != "/search" || m != "get" {
				t.Errorf("live search form action=%q method=%q, want GET /search", a, m)
			}
			if qs := findAll(forms[0], func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "q" }); len(qs) != 1 {
				t.Errorf("live search form has %d q inputs, want 1", len(qs))
			}

			containers := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })
			if len(containers) != 1 {
				t.Fatalf("found %d [data-search-results], want 1", len(containers))
			}
			c := containers[0]
			if n := len(findAll(c, func(n *html.Node) bool { return n.Data == "section" && hasClass(n, "result") })); n != tt.results {
				t.Errorf("container has %d .result sections, want %d", n, tt.results)
			}
			if got := len(findAll(c, func(n *html.Node) bool { return hasClass(n, "lookup-cta") })) == 1; got != tt.lookup {
				t.Errorf("lookup offer in container = %v, want %v", got, tt.lookup)
			}
			if got := len(findAll(c, func(n *html.Node) bool { return hasClass(n, "empty") })) == 1; got != tt.empty {
				t.Errorf("no-results message in container = %v, want %v", got, tt.empty)
			}
			if got := len(findAll(c, func(*html.Node) bool { return true })) == 0; got != tt.noChildren {
				t.Errorf("container empty = %v, want %v", got, tt.noChildren)
			}
		})
	}
}

func TestSearchAppLink(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: "site-1", Host: "one.example"}}}, nil
	}}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://paperround.app"}, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})

	tests := []struct {
		q       string
		appLink bool
	}{
		{"https://paperround.app/shared/abc", true},
		{"www.paperround.app/shared/abc", true},
		{"example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			rr := searchRequest(t, s, url.QueryEscape(tt.q))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			body := rr.Body.String()
			if got := strings.Contains(body, "paste its link into another feed reader"); got != tt.appLink {
				t.Errorf("app link message shown = %v, want %v", got, tt.appLink)
			}
			if got := strings.Contains(body, `action="/lookups"`); got == tt.appLink {
				t.Errorf("lookup offered = %v, want %v", got, !tt.appLink)
			}
			if !strings.Contains(body, `href="/sites/site-1"`) {
				t.Error("search results missing")
			}
		})
	}
}

func TestSearchInputAttributes(t *testing.T) {
	s := newTestServerWith(t, Services{SearchService: failingSearchStore(t)})
	body := searchRequest(t, s, "").Body.String()
	for _, want := range []string{`type="search" id="q"`, `autocapitalize="none"`, `autocorrect="off"`, `spellcheck="false"`, `enterkeyhint="search"`} {
		if !strings.Contains(body, want) {
			t.Errorf("search input missing %s", want)
		}
	}
}

func TestSearchNoResults(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "zzz")
	if !strings.Contains(rr.Body.String(), "No sites or feeds match") {
		t.Error("empty-state message missing")
	}
}

func TestSearchEmptyFeedTitleShowsURL(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return []rss.SiteWithFeeds{{
			Site: rss.Site{ID: "site-1", Host: "one.example"},
			Feeds: []rss.Feed{
				{ID: "feed-1", Title: "Titled", Url: "https://one.example/a.xml"},
				{ID: "feed-2", Url: "https://one.example/b.xml"},
			},
		}}, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})
	body := searchRequest(t, s, "one").Body.String()

	for _, want := range []string{
		`<a href="/feeds/feed-1">Titled</a>`,
		`<a href="/feeds/feed-2">https://one.example/b.xml</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestSearchStoreError(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, errors.New("boom")
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "ex")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestSearchEscapesQuery(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := searchRequest(t, s, "%3Cscript%3E")
	if strings.Contains(rr.Body.String(), "<script>") {
		t.Error("query rendered unescaped")
	}
}

func TestSearchHandlerSubscribedMarker(t *testing.T) {
	results := []rss.SiteWithFeeds{
		{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}, {ID: "feed-2", Title: "F2"}}},
		{Site: rss.Site{ID: "site-2", Host: "two.example"}, Feeds: []rss.Feed{{ID: "feed-3", Title: "F3"}}},
	}
	search := func(sites []rss.SiteWithFeeds) *fakeSearchStore {
		return &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
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

func TestSearchSiteIcons(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: testSiteID, Host: "one.example", Title: "One"}}}, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}, SiteService: &fakeSiteStore{}})
	testSiteIcons(t, s, testSiteID, "O", func(t *testing.T) string {
		rr := searchRequest(t, s, "one")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}
