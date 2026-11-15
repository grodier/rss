package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/rss"
)

func discoverRequest(t *testing.T, s *Server, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/discover?q="+q, nil)
	// scs needs a session in the context for PopString.
	ctx, err := s.sessionManager.Load(req.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.discoverHandler(rr, req.WithContext(ctx))
	return rr
}

func failingSearchStore(t *testing.T) *fakeSearchStore {
	return &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		t.Error("store must not be called")
		return nil, nil
	}}
}

// emptyCatalog is a DiscoverStore with nothing in Discover's catalog.
func emptyCatalog() *fakeDiscoverStore {
	return &fakeDiscoverStore{sectionsFn: func(context.Context, string, int) ([]rss.DiscoverSection, error) {
		return nil, nil
	}}
}

func failingDiscoverStore(t *testing.T) *fakeDiscoverStore {
	return &fakeDiscoverStore{sectionsFn: func(context.Context, string, int) ([]rss.DiscoverSection, error) {
		t.Error("Discover's store must not be called")
		return nil, nil
	}}
}

// discoverRequestAs is discoverRequest by a logged-in userID.
func discoverRequestAs(t *testing.T, s *Server, userID, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/discover?q="+q, nil)
	ctx, err := s.sessionManager.Load(req.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	rr := httptest.NewRecorder()
	s.discoverHandler(rr, req.WithContext(ctx))
	return rr
}

func TestDiscoverNoQuery(t *testing.T) {
	now := time.Now()
	latest := []rss.Article{
		{ID: "art-1", Title: "Newest post", PublishedAt: now.Add(-50 * time.Hour)},
		{ID: "art-2", Title: "Older post", PublishedAt: now.Add(-80 * time.Hour)},
		{ID: "art-3", PageTitle: "From its page", CreatedAt: now.Add(-100 * time.Hour)},
	}
	sections := []rss.DiscoverSection{
		{Name: "Food", Slug: "food", Total: 5, Feeds: []rss.DiscoverFeed{
			// The feed's title is the site's name: not shown again.
			{Feed: rss.Feed{ID: "feed-1", Title: "Bake Club", PostsPerWeek: 4.2}, Site: rss.Site{ID: "site-1", Host: "bake.example", Title: "Bake Club"}, Latest: latest},
			// A feed with its own title, of a site with no title.
			{Feed: rss.Feed{ID: "feed-2", Title: "Recipes only", PostsPerWeek: 0.5}, Site: rss.Site{ID: "site-2", Host: "cook.example"}},
		}},
		{Name: "Film and TV", Slug: "film-and-tv", Total: 1, Feeds: []rss.DiscoverFeed{
			{Feed: rss.Feed{ID: "feed-3", Title: "Reviews", PostsPerWeek: 2}, Site: rss.Site{ID: "site-3", Host: "reel.example", Title: "Reel"}, Latest: latest[:1]},
		}},
	}

	t.Run("sections", func(t *testing.T) {
		var gotUser string
		var gotPer int
		store := &fakeDiscoverStore{sectionsFn: func(_ context.Context, userID string, perSection int) ([]rss.DiscoverSection, error) {
			gotUser, gotPer = userID, perSection
			return sections, nil
		}}
		s := newTestServerWith(t, Services{SearchService: failingSearchStore(t), DiscoverService: store})
		rr := discoverRequestAs(t, s, "user-1", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if gotUser != "user-1" || gotPer != 3 {
			t.Errorf("Sections called with %q, %d; want user-1, 3", gotUser, gotPer)
		}
		doc, err := html.Parse(rr.Body)
		if err != nil {
			t.Fatal(err)
		}
		c := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })[0]

		var chips []string
		for _, a := range findAll(c, func(n *html.Node) bool { return n.Data == "a" && hasClass(n, "chip") }) {
			chips = append(chips, attr(a, "href")+" "+textContent(a))
		}
		if want := []string{"#section-food Food", "#section-film-and-tv Film and TV"}; !slices.Equal(chips, want) {
			t.Errorf("chips = %q, want %q", chips, want)
		}
		for _, id := range []string{"section-food", "section-film-and-tv"} {
			if len(findAll(c, func(n *html.Node) bool { return n.Data == "section" && attr(n, "id") == id })) != 1 {
				t.Errorf("no section #%s", id)
			}
		}

		row := func(feedID string) *html.Node {
			t.Helper()
			rows := findAll(c, func(n *html.Node) bool { return n.Data == "li" && attr(n, "id") == "feed-"+feedID })
			if len(rows) != 1 {
				t.Fatalf("%d rows #feed-%s, want 1", len(rows), feedID)
			}
			return rows[0]
		}
		textOf := func(n *html.Node, class string) string {
			found := findAll(n, func(n *html.Node) bool { return hasClass(n, class) })
			if len(found) == 0 {
				return ""
			}
			return strings.Join(strings.Fields(textContent(found[0])), " ")
		}

		r1 := row("feed-1")
		if got, want := textOf(r1, "discover-feed-name"), "Bake Club bake.example"; got != want {
			t.Errorf("feed-1 name = %q, want %q", got, want)
		}
		if got := textOf(r1, "discover-feed-title"); got != "" {
			t.Errorf("feed-1 shows its title %q, though it's the site's name", got)
		}
		if got, want := textOf(r1, "discover-meta"), "About 4 posts a week · Latest 2 days ago"; got != want {
			t.Errorf("feed-1 meta = %q, want %q", got, want)
		}
		peek := findAll(r1, func(n *html.Node) bool { return n.Data == "details" && hasClass(n, "discover-peek") })
		if len(peek) != 1 {
			t.Fatalf("feed-1 has %d peeks, want 1", len(peek))
		}
		if s := findAll(peek[0], func(n *html.Node) bool { return n.Data == "summary" }); len(s) != 1 || textContent(s[0]) != "Latest posts" {
			t.Errorf("peek summary wrong")
		}
		var links []string
		for _, a := range findAll(peek[0], func(n *html.Node) bool { return n.Data == "a" }) {
			links = append(links, attr(a, "href")+" "+textContent(a))
		}
		if want := []string{"/articles/art-1 Newest post", "/articles/art-2 Older post", "/articles/art-3 From its page"}; !slices.Equal(links, want) {
			t.Errorf("peek links = %q, want %q", links, want)
		}

		r2 := row("feed-2")
		if got, want := textOf(r2, "discover-feed-name"), "cook.example cook.example"; got != want {
			t.Errorf("feed-2 name = %q, want %q (the host names an untitled site)", got, want)
		}
		if got := textOf(r2, "discover-feed-title"); got != "Recipes only" {
			t.Errorf("feed-2 title = %q, want Recipes only", got)
		}
		if got, want := textOf(r2, "discover-meta"), "Less than one post a week"; got != want {
			t.Errorf("feed-2 meta = %q, want %q (no Latest without articles)", got, want)
		}
		if len(findAll(r2, func(n *html.Node) bool { return n.Data == "details" })) != 0 {
			t.Error("feed-2 has a peek without articles")
		}

		for _, id := range []string{"feed-1", "feed-2", "feed-3"} {
			forms := findAll(row(id), func(n *html.Node) bool { return n.Data == "form" })
			if len(forms) != 1 {
				t.Fatalf("%s has %d forms, want 1", id, len(forms))
			}
			f := forms[0]
			if attr(f, "action") != "/feeds/"+id+"/subscribe" || attr(f, "method") != "post" || !hasAttr(f, "data-toggle") {
				t.Errorf("%s Follow form: action %q method %q data-toggle %v", id, attr(f, "action"), attr(f, "method"), hasAttr(f, "data-toggle"))
			}
			ret := findAll(f, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "return_to" })
			if len(ret) != 1 || attr(ret[0], "value") != "/discover#feed-"+id {
				t.Errorf("%s return_to wrong", id)
			}
			b := findAll(f, func(n *html.Node) bool { return n.Data == "button" })
			if len(b) != 1 || attr(b[0], "aria-pressed") != "false" {
				t.Errorf("%s Follow button isn't aria-pressed=false", id)
			}
		}
		if got := attr(findAll(row("feed-1"), func(n *html.Node) bool { return n.Data == "form" })[0], "data-announce-pressed"); got != "Following Bake Club" {
			t.Errorf("data-announce-pressed = %q, want Following Bake Club", got)
		}
		// A feed with a title of its own is named by both, so a site's two
		// catalog feeds aren't announced alike.
		b2 := findAll(row("feed-2"), func(n *html.Node) bool { return n.Data == "button" })[0]
		if got, want := attr(b2, "aria-label"), "Follow cook.example – Recipes only"; got != want {
			t.Errorf("feed-2 Follow aria-label = %q, want %q", got, want)
		}
	})

	t.Run("empty catalog", func(t *testing.T) {
		s := newTestServerWith(t, Services{SearchService: failingSearchStore(t), DiscoverService: emptyCatalog()})
		rr := discoverRequestAs(t, s, "user-1", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `name="q"`) || strings.Contains(body, "section-chips") {
			t.Error("want the search box alone")
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := &fakeDiscoverStore{sectionsFn: func(context.Context, string, int) ([]rss.DiscoverSection, error) {
			return nil, errors.New("db down")
		}}
		s := newTestServerWith(t, Services{SearchService: failingSearchStore(t), DiscoverService: store})
		if rr := discoverRequestAs(t, s, "user-1", ""); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func TestDiscoverQuery(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}}}}, nil
	}}
	subs := &fakeSubscriptionStore{subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
		return map[string]bool{"feed-1": true}, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: subs, DiscoverService: failingDiscoverStore(t)})
	rr := discoverRequestAs(t, s, "user-1", "one")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `class="result"`) || !strings.Contains(body, `<span class="subscribed">Following</span>`) {
		t.Error("want the result, marked Following")
	}
	if strings.Contains(body, "section-chips") || strings.Contains(body, "discover-section") {
		t.Error("sections shown with a query")
	}
}

func TestSearchTooLong(t *testing.T) {
	s := newTestServerWith(t, Services{SearchService: failingSearchStore(t)})
	rr := discoverRequest(t, s, strings.Repeat("a", 201))
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
	rr := discoverRequest(t, s, "ex")
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
	discoverRequest(t, s, "https://www.NPR.org/news")
	if gotQ != "https://www.NPR.org/news" || gotKey != "npr.org" {
		t.Errorf("Search called with %q, %q; want the query as typed and site key npr.org", gotQ, gotKey)
	}
}

// TestLiveSearchMarkup checks the markup live-search.ts relies on, in
// every state of the page, since a page in any state can be typed into: the
// app entry's script, a GET
// form[data-live-search] to /discover with a q input, and one
// [data-search-results] container holding the lookup offer, the results
// (.result) and the no-results message, and, with no query, Discover's
// sections (empty here, with an empty catalog).
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
			rr := discoverRequest(t, s, tt.q)
			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d", rr.Code, tt.status)
			}
			doc, err := html.Parse(rr.Body)
			if err != nil {
				t.Fatal(err)
			}

			scripts := findAll(doc, func(n *html.Node) bool {
				return n.Data == "script" && attr(n, "src") == entryJS("discover") && attr(n, "type") == "module"
			})
			if len(scripts) != 1 {
				t.Errorf("found %d search entry module scripts, want 1", len(scripts))
			}

			forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-live-search") })
			if len(forms) != 1 {
				t.Fatalf("found %d form[data-live-search], want 1", len(forms))
			}
			if a, m := attr(forms[0], "action"), attr(forms[0], "method"); a != "/discover" || m != "get" {
				t.Errorf("live search form action=%q method=%q, want GET /discover", a, m)
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
			rr := discoverRequest(t, s, url.QueryEscape(tt.q))
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
	body := discoverRequest(t, s, "").Body.String()
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
	rr := discoverRequest(t, s, "zzz")
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
	body := discoverRequest(t, s, "one").Body.String()

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
	rr := discoverRequest(t, s, "ex")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestSearchEscapesQuery(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
		return nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := discoverRequest(t, s, "%3Cscript%3E")
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
		req := httptest.NewRequest(http.MethodGet, "/discover?q=ex", nil)
		ctx, err := s.sessionManager.Load(req.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, authenticatedUserIDContextKey, "user-1")
		rr := httptest.NewRecorder()
		s.discoverHandler(rr, req.WithContext(ctx))
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
		if !strings.Contains(body, `F2</a> <span class="subscribed">Following</span>`) {
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
		rr := discoverRequest(t, s, "one")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}
