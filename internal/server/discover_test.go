package server

import (
	"bytes"
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
	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/discovery"
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
	return &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		t.Error("store must not be called")
		return nil, nil, nil
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
		var all []string
		for _, a := range findAll(c, func(n *html.Node) bool {
			return n.Data == "a" && strings.HasPrefix(attr(n, "href"), "/discover/sections/")
		}) {
			all = append(all, attr(a, "href")+" "+strings.Join(strings.Fields(textContent(a)), " "))
		}
		if want := []string{"/discover/sections/food All 5 Food feeds ›", "/discover/sections/film-and-tv All 1 Film and TV feeds ›"}; !slices.Equal(all, want) {
			t.Errorf("All N links = %q, want %q", all, want)
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
		if got, want := textOf(r1, "discover-meta"), "About 4 posts a week · Latest post 2 days ago"; got != want {
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

// TestLiveSearchMarkup checks the markup live-search.ts relies on, in
// every state of the page, since a page in any state can be typed into: the
// app entry's script, a GET
// form[data-live-search] to /discover (without scope in its action, which
// live search fetches with q alone) with a q input and a hidden scope, which
// only a submit sends, and one [data-search-results] container holding the
// Look up row, the suggestions or results (li.search-result) and the
// no-results message, and, with no query, Discover's sections (empty here,
// with an empty catalog).
func TestLiveSearchMarkup(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(_ context.Context, q, _, _ string, _ int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		if q == "zzz" {
			return nil, nil, nil
		}
		return []rss.SiteWithFeeds{
			{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}}},
			{Site: rss.Site{ID: "site-2", Host: "two.example"}},
		}, nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})

	tests := []struct {
		name, q    string
		status     int
		results    int    // result rows in the container
		lookup     bool   // the lookup offer is in the container
		empty      bool   // the no-results message is in the container
		noChildren bool   // the container has no elements
		importCard bool   // the container holds only the import card (an empty catalog, a new reader)
		recent     string // the container's data-recent-query, "" for none
		everything bool   // the suggestions' "Search everything" row is in the container
		scope      string // the form's hidden scope
	}{
		{name: "no query", q: "", status: http.StatusOK, importCard: true, scope: "top"},
		{name: "suggestions", q: "ex", status: http.StatusOK, results: 2, everything: true, scope: "top"},
		{name: "address suggestions", q: "npr.org", status: http.StatusOK, results: 2, lookup: true, everything: true, scope: "top"},
		{name: "no suggestions", q: "zzz", status: http.StatusOK, everything: true, scope: "top"},
		// A reader who has saved nothing gets the Feeds tab's rows, without tabs.
		{name: "results", q: "ex&scope=top", status: http.StatusOK, results: 2, recent: "ex", scope: "feeds"},
		{name: "address results", q: "npr.org&scope=top", status: http.StatusOK, results: 2, lookup: true, recent: "npr.org", scope: "feeds"},
		{name: "no results", q: "zzz&scope=top", status: http.StatusOK, empty: true, recent: "zzz", scope: "feeds"},
		{name: "trimmed", q: url.QueryEscape("  ex ") + "&scope=top", status: http.StatusOK, results: 2, recent: "ex", scope: "feeds"},
		{name: "invalid query", q: strings.Repeat("a", 201), status: http.StatusUnprocessableEntity, noChildren: true, scope: "top"},
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
			scopes := findAll(forms[0], func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "scope" })
			if len(scopes) != 1 || attr(scopes[0], "type") != "hidden" || attr(scopes[0], "value") != tt.scope {
				t.Errorf("live search form's scope inputs = %d, want 1 hidden with value %q", len(scopes), tt.scope)
			}

			containers := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })
			if len(containers) != 1 {
				t.Fatalf("found %d [data-search-results], want 1", len(containers))
			}
			c := containers[0]
			if got := attr(c, "data-recent-query"); got != tt.recent || hasAttr(c, "data-recent-query") != (tt.recent != "") {
				t.Errorf("container's data-recent-query = %q (set %v), want %q", got, hasAttr(c, "data-recent-query"), tt.recent)
			}

			// recent-searches.ts clones the template's list and its first
			// row, filling the row's link and data-recent-text.
			templates := findAll(doc, func(n *html.Node) bool { return n.Data == "template" && hasAttr(n, "data-recent-template") })
			if len(templates) != 1 {
				t.Fatalf("found %d template[data-recent-template], want 1", len(templates))
			}
			tpl := templates[0]
			if n := len(findAll(tpl, func(n *html.Node) bool { return n.Data == "button" && hasAttr(n, "data-recent-clear") })); n != 1 {
				t.Errorf("template has %d Clear buttons, want 1", n)
			}
			if n := len(findAll(tpl, func(n *html.Node) bool {
				return n.Data == "li" && len(findAll(n, func(n *html.Node) bool { return n.Data == "a" })) == 1 &&
					len(findAll(n, func(n *html.Node) bool { return hasAttr(n, "data-recent-text") })) == 1
			})); n != 1 {
				t.Errorf("template has %d rows with a link and data-recent-text, want 1", n)
			}
			if n := len(findAll(c, func(n *html.Node) bool {
				return n.Data == "li" && hasClass(n, "search-result") && !hasClass(n, "search-lookup")
			})); n != tt.results {
				t.Errorf("container has %d result rows, want %d", n, tt.results)
			}
			if got := len(findAll(c, func(n *html.Node) bool { return hasClass(n, "search-lookup") })) == 1; got != tt.lookup {
				t.Errorf("lookup offer in container = %v, want %v", got, tt.lookup)
			}
			if got := len(findAll(c, func(n *html.Node) bool { return hasClass(n, "empty") })) == 1; got != tt.empty {
				t.Errorf("no-results message in container = %v, want %v", got, tt.empty)
			}
			if got := len(findAll(c, func(n *html.Node) bool { return hasClass(n, "search-everything") })) == 1; got != tt.everything {
				t.Errorf("Search everything in container = %v, want %v", got, tt.everything)
			}
			if got := len(findAll(c, func(*html.Node) bool { return true })) == 0; got != tt.noChildren {
				t.Errorf("container empty = %v, want %v", got, tt.noChildren)
			}
			var top []string
			for n := c.FirstChild; n != nil; n = n.NextSibling {
				if n.Type == html.ElementNode {
					top = append(top, n.Data+"."+attr(n, "class"))
				}
			}
			if got := slices.Equal(top, []string{"section.import-card"}); got != tt.importCard {
				t.Errorf("container's elements = %q, want only the import card %v", top, tt.importCard)
			}
		})
	}
}

func TestSearchAppLink(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: "site-1", Host: "one.example"}}}, nil, nil
	}}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://paperround.app"}, Services{SearchService: store, SubscriptionService: &fakeSubscriptionStore{}})

	tests := []struct {
		q       string
		appLink bool
	}{
		{"https://paperround.app/shared/abc", true},
		{"www.paperround.app/shared/abc", true},
		{"paperround.app/@x", true},
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

func TestSearchStoreError(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return nil, nil, errors.New("boom")
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := discoverRequest(t, s, "ex")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestSearchEscapesQuery(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return nil, nil, nil
	}}
	s := newTestServerWith(t, Services{SearchService: store})
	rr := discoverRequest(t, s, "%3Cscript%3E")
	if strings.Contains(rr.Body.String(), "<script>") {
		t.Error("query rendered unescaped")
	}
}

func TestSearchSiteIcons(t *testing.T) {
	store := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: testSiteID, Host: "one.example", Title: "One"}}}, nil, nil
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

// sectionRequest gets a section's page, path relative to
// /discover/sections/, through the router, logged in as user-1.
func sectionRequest(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/discover/sections/"+path, nil)
	ctx, err := s.sessionManager.Load(req.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, "user-1")
	rctx := chi.NewRouteContext()
	slug, rest, _ := strings.Cut(strings.SplitN(path, "?", 2)[0], "/")
	rctx.URLParams.Add("slug", slug)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	if rest == "posts" {
		s.discoverSectionPostsHandler(rr, req)
	} else {
		s.discoverSectionHandler(rr, req)
	}
	return rr
}

func TestDiscoverSectionPage(t *testing.T) {
	science := rss.Section{ID: "sec-1", Slug: "science", Name: "Science"}
	feeds := []rss.DiscoverFeed{
		{Feed: rss.Feed{ID: "feed-1", Title: "Orbit", PostsPerWeek: 2}, Site: rss.Site{ID: "site-1", Host: "orbit.example", Title: "Orbit"}},
		{Feed: rss.Feed{ID: "feed-2", Title: "Cells", PostsPerWeek: 5}, Site: rss.Site{ID: "site-2", Host: "cells.example", Title: "Bench"}, Following: true},
	}
	posts := func(n int) []rss.SectionPost {
		var out []rss.SectionPost
		for i := range n {
			f := feeds[i%2]
			out = append(out, rss.SectionPost{
				Article: rss.Article{ID: fmt.Sprintf("art-%d", i), FeedID: f.Feed.ID, Title: fmt.Sprintf("Post %d", i), PublishedAt: time.Now().Add(-time.Duration(i) * time.Hour)},
				Feed:    f,
			})
		}
		return out
	}
	store := func(posted []rss.SectionPost, err error) *fakeDiscoverStore {
		return &fakeDiscoverStore{
			sectionFn: func(_ context.Context, slug string) (rss.Section, error) {
				if slug != "science" {
					return rss.Section{}, rss.ErrNoRecord
				}
				return science, nil
			},
			sectionFeedsFn: func(_ context.Context, userID, sectionID string) ([]rss.DiscoverFeed, error) {
				if userID != "user-1" || sectionID != "sec-1" {
					t.Errorf("SectionFeeds(%q, %q); want user-1, sec-1", userID, sectionID)
				}
				return feeds, err
			},
			sectionPostsFn: func(_ context.Context, userID, sectionID string, _ rss.ArticleCursor, limit int) ([]rss.SectionPost, error) {
				if userID != "user-1" || sectionID != "sec-1" || limit != sectionPageSize+1 {
					t.Errorf("SectionPosts(%q, %q, %d); want user-1, sec-1, %d", userID, sectionID, limit, sectionPageSize+1)
				}
				return posted, err
			},
		}
	}
	server := func(d *fakeDiscoverStore) *Server {
		return newTestServerWith(t, Services{DiscoverService: d, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}})
	}
	parse := func(t *testing.T, rr *httptest.ResponseRecorder) *html.Node {
		t.Helper()
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		doc, err := html.Parse(rr.Body)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	currentTab := func(doc *html.Node) string {
		tabs := findAll(doc, func(n *html.Node) bool { return n.Data == "nav" && hasClass(n, "section-tabs") })
		if len(tabs) != 1 {
			return ""
		}
		cur := findAll(tabs[0], func(n *html.Node) bool { return n.Data == "a" && attr(n, "aria-current") == "page" })
		if len(cur) != 1 {
			return ""
		}
		return attr(cur[0], "href")
	}

	t.Run("feeds", func(t *testing.T) {
		doc := parse(t, sectionRequest(t, server(store(nil, nil)), "science"))
		if got := textContent(findAll(doc, func(n *html.Node) bool { return n.Data == "h1" })[0]); got != "Science" {
			t.Errorf("h1 = %q, want Science", got)
		}
		if got := currentTab(doc); got != "/discover/sections/science" {
			t.Errorf("current tab = %q, want the Feeds tab", got)
		}
		// feed-1 has Follow, returning to its row; followed feed-2 links to its page.
		forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "follow-form") })
		if len(forms) != 1 || attr(forms[0], "action") != "/feeds/feed-1/subscribe" {
			t.Fatalf("Follow forms = %d; want one, for feed-1", len(forms))
		}
		ret := findAll(forms[0], func(n *html.Node) bool { return attr(n, "name") == "return_to" })
		if len(ret) != 1 || attr(ret[0], "value") != "/discover/sections/science#feed-feed-1" {
			t.Errorf("return_to = %v; want /discover/sections/science#feed-feed-1", ret)
		}
		checkFollowOnce(t, forms[0], "feed-1")
		label := findAll(doc, isShownFollowingLabel)
		if len(label) != 1 || attr(label[0], "href") != "/feeds/feed-2" || !strings.Contains(textContent(label[0]), "Following") {
			t.Errorf("followed feed's label = %v; want one linking to /feeds/feed-2", label)
		}
	})

	t.Run("recent posts", func(t *testing.T) {
		doc := parse(t, sectionRequest(t, server(store(posts(sectionPageSize+1), nil)), "science/posts"))
		if got := currentTab(doc); got != "/discover/sections/science/posts" {
			t.Errorf("current tab = %q, want the Recent posts tab", got)
		}
		rows := findAll(doc, func(n *html.Node) bool { return n.Data == "article" && hasClass(n, "article-row") })
		if len(rows) != sectionPageSize {
			t.Fatalf("%d rows, want %d", len(rows), sectionPageSize)
		}
		// Even rows are feed-1's (not followed): Follow; odd are feed-2's: none.
		for i, row := range rows[:2] {
			forms := findAll(row, func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "follow-form") })
			if want := 1 - i; len(forms) != want {
				t.Errorf("row %d has %d Follow forms, want %d", i, len(forms), want)
			}
		}
		follow := findAll(rows[0], func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "follow-form") })[0]
		ret := findAll(follow, func(n *html.Node) bool { return attr(n, "name") == "return_to" })
		if want := "/discover/sections/science/posts#article-art-0"; len(ret) != 1 || attr(ret[0], "value") != want {
			t.Errorf("Follow's return_to = %v; want %q", ret, want)
		}
		checkFollowOnce(t, follow, "feed-1")
		next := findAll(doc, func(n *html.Node) bool { return n.Data == "a" && attr(n, "rel") == "next" })
		if len(next) != 1 || !strings.HasPrefix(attr(next[0], "href"), "/discover/sections/science/posts?before=") {
			t.Errorf("Older posts link = %v", next)
		}
	})

	t.Run("no posts", func(t *testing.T) {
		rr := sectionRequest(t, server(store(nil, nil)), "science/posts")
		if body := rr.Body.String(); rr.Code != http.StatusOK || !strings.Contains(body, "No posts in Science yet.") {
			t.Errorf("status %d; want 200 with the empty state", rr.Code)
		}
	})

	t.Run("unknown slug", func(t *testing.T) {
		for _, path := range []string{"nope", "nope/posts"} {
			if rr := sectionRequest(t, server(store(nil, nil)), path); rr.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", path, rr.Code)
			}
		}
	})

	t.Run("bad cursor", func(t *testing.T) {
		if rr := sectionRequest(t, server(store(nil, nil)), "science/posts?before=zzz"); rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("store error", func(t *testing.T) {
		boom := errors.New("boom")
		for _, path := range []string{"science", "science/posts"} {
			if rr := sectionRequest(t, server(store(nil, boom)), path); rr.Code != http.StatusInternalServerError {
				t.Errorf("%s: status = %d, want 500", path, rr.Code)
			}
		}
		section := store(nil, nil)
		section.sectionFn = func(context.Context, string) (rss.Section, error) { return rss.Section{}, boom }
		if rr := sectionRequest(t, server(section), "science"); rr.Code != http.StatusInternalServerError {
			t.Errorf("Section error: status = %d, want 500", rr.Code)
		}
	})
}

func TestDiscoverImportCard(t *testing.T) {
	finished := rss.ImportSummary{Import: rss.Import{ID: "imp-1", Filename: "feedly.opml"}, Total: 120, Failed: 3}
	running := rss.ImportSummary{Import: rss.Import{ID: "imp-2", Filename: "feedly.opml"}, Total: 120, Pending: 86, Failed: 2}

	tests := []struct {
		name          string
		q             string
		imports       []rss.ImportSummary
		subscriptions int
		offer         bool
		running       bool
		line          bool
	}{
		{name: "no imports and no feeds", offer: true},
		{name: "no imports and 9 feeds", subscriptions: 9, offer: true},
		{name: "no imports and 10 feeds", subscriptions: 10, line: true},
		{name: "a finished import", imports: []rss.ImportSummary{finished}, line: true},
		{name: "a running import", imports: []rss.ImportSummary{running}, subscriptions: 40, running: true},
		{name: "a running import of one feed", imports: []rss.ImportSummary{{Import: rss.Import{ID: "imp-3", Filename: "one.opml"}, Total: 1, Pending: 1}}, running: true},
		{name: "with a query", q: "ex", imports: []rss.ImportSummary{running}},
		{name: "with an invalid query", q: strings.Repeat("a", 201)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotLimit int
			imports := &fakeImportStore{listByUserFn: func(_ context.Context, userID string, limit int) ([]rss.ImportSummary, error) {
				if userID != "user-1" {
					t.Errorf("ListByUser for %q, want user-1", userID)
				}
				gotLimit = limit
				return tt.imports, nil
			}}
			subs := &fakeSubscriptionStore{countFn: func(_ context.Context, userID string) (int, error) {
				if userID != "user-1" {
					t.Errorf("Count for %q, want user-1", userID)
				}
				return tt.subscriptions, nil
			}}
			search := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
				return nil, nil, nil
			}}
			s := newTestServerWith(t, Services{SearchService: search, ImportService: imports, SubscriptionService: subs})
			rr := discoverRequestAs(t, s, "user-1", tt.q)
			doc, err := html.Parse(rr.Body)
			if err != nil {
				t.Fatal(err)
			}
			if tt.q == "" && gotLimit != 1 {
				t.Errorf("ListByUser limit = %d, want 1", gotLimit)
			}

			cards := findAll(doc, func(n *html.Node) bool { return hasClass(n, "import-card") })
			lines := findAll(doc, func(n *html.Node) bool { return hasClass(n, "import-line") })
			if want := tt.offer || tt.running; (len(cards) == 1) != want || len(cards) > 1 {
				t.Fatalf("%d import cards, want card %v", len(cards), want)
			}
			if (len(lines) == 1) != tt.line || len(lines) > 1 {
				t.Errorf("%d import lines, want line %v", len(lines), tt.line)
			}
			text := func(n *html.Node) string { return strings.Join(strings.Fields(textContent(n)), " ") }
			links := func(n *html.Node) []string {
				var got []string
				for _, a := range findAll(n, func(n *html.Node) bool { return n.Data == "a" }) {
					got = append(got, attr(a, "href")+" "+text(a))
				}
				return got
			}

			if tt.line {
				if got, want := text(lines[0]), "Coming from another reader? Import your subscriptions"; got != want {
					t.Errorf("line = %q, want %q", got, want)
				}
				if got, want := links(lines[0]), []string{"/settings/import Import your subscriptions"}; !slices.Equal(got, want) {
					t.Errorf("line links = %q, want %q", got, want)
				}
			}
			if tt.offer {
				c := cards[0]
				if got, want := text(c), "Coming from another reader? Import your subscriptions from Feedly, Inoreader, NetNewsWire, Reeder or any reader that exports OPML. It takes a minute. Import"; got != want {
					t.Errorf("offer = %q, want %q", got, want)
				}
				if got, want := links(c), []string{"/settings/import Import"}; !slices.Equal(got, want) {
					t.Errorf("offer links = %q, want %q", got, want)
				}
				if n := len(findAll(c, func(n *html.Node) bool { return n.Data == "progress" })); n != 0 {
					t.Errorf("offer has %d progress bars", n)
				}
			}
			if tt.running {
				c := cards[0]
				if tt.imports[0].Total == 1 {
					if got, want := text(c), "Importing one.opml 0 of 1 feed so far. You can keep browsing; they'll show up on Home as they're added. Details of this import"; got != want {
						t.Errorf("progress = %q, want %q", got, want)
					}
					return
				}
				if got, want := text(c), "Importing feedly.opml 34 of 120 feeds so far. You can keep browsing; they'll show up on Home as they're added. Details of this import"; got != want {
					t.Errorf("progress = %q, want %q", got, want)
				}
				bars := findAll(c, func(n *html.Node) bool { return n.Data == "progress" })
				if len(bars) != 1 || attr(bars[0], "value") != "34" || attr(bars[0], "max") != "120" {
					t.Errorf("progress bar wrong: %d bars", len(bars))
				}
				if got, want := links(c), []string{"/settings/imports/imp-2 Details of this import"}; !slices.Equal(got, want) {
					t.Errorf("progress links = %q, want %q", got, want)
				}
			}
		})
	}

	t.Run("a failing query is a 500", func(t *testing.T) {
		for name, svc := range map[string]Services{
			"imports": {ImportService: &fakeImportStore{listByUserFn: func(context.Context, string, int) ([]rss.ImportSummary, error) {
				return nil, errors.New("boom")
			}}},
			"subscriptions": {SubscriptionService: &fakeSubscriptionStore{countFn: func(context.Context, string) (int, error) {
				return 0, errors.New("boom")
			}}},
		} {
			rr := discoverRequestAs(t, newTestServerWith(t, svc), "user-1", "")
			if rr.Code != http.StatusInternalServerError {
				t.Errorf("%s: status = %d, want 500", name, rr.Code)
			}
		}
	})
}

func TestDiscoverSectionOrderNote(t *testing.T) {
	const note = `<p class="section-order-note">Sections you follow most come first.</p>`
	section := func(name string, position int) rss.DiscoverSection {
		slug := strings.ToLower(name)
		return rss.DiscoverSection{Name: name, Slug: slug, Position: position, Total: 1, Feeds: []rss.DiscoverFeed{
			{Feed: rss.Feed{ID: "feed-" + slug}, Site: rss.Site{ID: "site-" + slug, Host: slug + ".example"}},
		}}
	}
	tests := []struct {
		name     string
		sections []rss.DiscoverSection
		wantNote bool
	}{
		{"the default order", []rss.DiscoverSection{section("News", 1), section("Food", 9), section("Travel", 15)}, false},
		{"one section", []rss.DiscoverSection{section("Food", 9)}, false},
		{"a section moved up", []rss.DiscoverSection{section("Food", 9), section("News", 1), section("Travel", 15)}, true},
		{"sections sharing a position", []rss.DiscoverSection{section("Food", 9), section("Baking", 9)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeDiscoverStore{sectionsFn: func(context.Context, string, int) ([]rss.DiscoverSection, error) {
				return tt.sections, nil
			}}
			s := newTestServerWith(t, Services{SearchService: failingSearchStore(t), DiscoverService: store})
			body := discoverRequestAs(t, s, "user-1", "").Body.String()
			if got := strings.Contains(body, note); got != tt.wantNote {
				t.Errorf("note shown = %v, want %v", got, tt.wantNote)
			}
			if tt.wantNote && !strings.Contains(body, "</nav>\n      "+note) {
				t.Error("the note isn't right under the chips")
			}
			// The chips follow the sections' order.
			last := -1
			for _, sec := range tt.sections {
				i := strings.Index(body, `<a class="chip" href="#section-`+sec.Slug+`">`)
				if i < last {
					t.Errorf("the %s chip is out of order", sec.Name)
				}
				last = i
			}
		})
	}

	t.Run("not with a query", func(t *testing.T) {
		store := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
			return nil, nil, nil
		}}
		s := newTestServerWith(t, Services{SearchService: store, DiscoverService: failingDiscoverStore(t)})
		if body := discoverRequestAs(t, s, "user-1", "food").Body.String(); strings.Contains(body, "section-order-note") {
			t.Error("the note is shown with a query")
		}
	})
}

// TestSearchBoxSubmit checks Discover's and Saved's search boxes submit from
// the field's magnifier, with no text Search button beside it (#665).
func TestSearchBoxSubmit(t *testing.T) {
	ts, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	var icon bytes.Buffer
	if err := ts["discover.html"].ExecuteTemplate(&icon, "icon-search", nil); err != nil {
		t.Fatal(err)
	}
	iconDoc, err := html.Parse(&icon)
	if err != nil {
		t.Fatal(err)
	}
	svgs := findAll(iconDoc, func(n *html.Node) bool { return n.Data == "svg" })
	if len(svgs) != 1 {
		t.Fatalf("icon-search has %d svgs, want 1", len(svgs))
	}
	var wantIcon bytes.Buffer
	if err := html.Render(&wantIcon, svgs[0]); err != nil {
		t.Fatal(err)
	}

	search := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return []rss.SiteWithFeeds{{Site: rss.Site{ID: "site-1", Host: "one.example"}, Feeds: []rss.Feed{{ID: "feed-1", Title: "F1"}}}}, nil, nil
	}}
	saves := &fakeSaveStore{listSavedFn: func(context.Context, string, string, rss.ArticleCursor, int) ([]rss.SavedArticle, error) {
		return savedItems(2), nil
	}}
	s := newTestServerWith(t, Services{SearchService: search, SaveService: saves, ReadService: &fakeReadStore{}})

	tests := []struct {
		name   string
		render func(t *testing.T) *httptest.ResponseRecorder
	}{
		{"discover without a query", func(t *testing.T) *httptest.ResponseRecorder { return discoverRequest(t, s, "") }},
		{"discover with a query", func(t *testing.T) *httptest.ResponseRecorder { return discoverRequest(t, s, "ex") }},
		{"saved", func(t *testing.T) *httptest.ResponseRecorder { return getSaved(t, s, "/saved") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := tt.render(t)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			doc, err := html.Parse(rr.Body)
			if err != nil {
				t.Fatal(err)
			}
			forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasClass(n, "page-search") })
			if len(forms) != 1 {
				t.Fatalf("found %d search forms, want 1", len(forms))
			}
			submits := findAll(forms[0], func(n *html.Node) bool {
				return (n.Data == "button" && attr(n, "type") != "button") || (n.Data == "input" && attr(n, "type") == "submit")
			})
			if len(submits) != 1 {
				t.Fatalf("found %d submit buttons, want 1", len(submits))
			}
			b := submits[0]
			if b.Parent == nil || !hasClass(b.Parent, "search-field") {
				t.Error("the submit button isn't in .search-field")
			}
			if got := attr(b, "aria-label"); got != "Search" {
				t.Errorf("aria-label = %q, want Search", got)
			}
			if got := strings.TrimSpace(textContent(b)); got != "" {
				t.Errorf("the submit button has text %q, want only the icon", got)
			}
			var got bytes.Buffer
			for c := b.FirstChild; c != nil; c = c.NextSibling {
				if err := html.Render(&got, c); err != nil {
					t.Fatal(err)
				}
			}
			if got.String() != wantIcon.String() {
				t.Errorf("the submit button holds %s, want the search icon %s", got.String(), wantIcon.String())
			}
		})
	}
}

func TestKnownAddress(t *testing.T) {
	sites := []rss.SiteWithFeeds{
		{Site: rss.Site{Host: "example.com"}, Feeds: []rss.Feed{
			{Url: "https://example.com/feed.xml"},
			{Url: "http://www.example.com/rss?section=world"},
			{Url: "https://feeds.host.example/example"}, // on another host
		}},
		{Site: rss.Site{Host: "other.example"}},
	}
	// The home page counts only when it has been looked up (homeChecked).
	if !knownAddress(mustParseInput(t, "example.com"), sites, true) {
		t.Error("knownAddress(home page, homeChecked) = false")
	}
	tests := []struct {
		q    string
		want bool
	}{
		{"example.com", false},
		{"other.example", false},
		{"example.com/feed.xml", true},
		{"http://WWW.example.com/feed.xml#top", true},
		{"example.com/rss?section=world", true},
		{"feeds.host.example/example", true},
		{"example.com/?page=2", false},
		{"example.com/hidden.xml", false},
		{"example.com/rss?section=sport", false},
		{"example.com/rss", false},
		{"example.com:8443/feed.xml", false},
		{"new.example", false},
	}
	for _, tt := range tests {
		if got := knownAddress(mustParseInput(t, tt.q), sites, false); got != tt.want {
			t.Errorf("knownAddress(%q) = %v, want %v", tt.q, got, tt.want)
		}
	}
}

func mustParseInput(t *testing.T, q string) *url.URL {
	t.Helper()
	u, err := discovery.ParseInput(q)
	if err != nil {
		t.Fatalf("ParseInput(%q): %v", q, err)
	}
	return u
}
