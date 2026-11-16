package server

import (
	"context"
	"errors"
	"maps"
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

// searchRequest gets /search?q=q (escaped) as user-1.
func searchRequest(t *testing.T, s *Server, q string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(q), nil)
	ctx, err := s.sessionManager.Load(req.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, "user-1")
	rr := httptest.NewRecorder()
	s.searchHandler(rr, req.WithContext(ctx))
	return rr
}

// searchStores are Search's three sources, each returning its results for
// any query, and recording the limits it was asked for.
type searchStores struct {
	feeds   []rss.Subscription
	saved   []rss.SavedArticle
	sites   []rss.SiteWithFeeds
	err     error // returned by the source named in failing
	failing string
	limits  map[string]int
	calls   int
}

func (st *searchStores) services() Services {
	st.limits = map[string]int{}
	fail := func(name string) error {
		if st.failing == name {
			return st.err
		}
		return nil
	}
	return Services{
		SubscriptionService: &fakeSubscriptionStore{
			searchFn: func(_ context.Context, userID, _ string, limit int) ([]rss.Subscription, error) {
				st.calls++
				st.limits["feeds"] = limit
				if userID != "user-1" {
					return nil, errors.New("wrong user")
				}
				return st.feeds, fail("feeds")
			},
			subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
				return map[string]bool{"site-feed-followed": true}, nil
			},
		},
		SaveService: &fakeSaveStore{listSavedFn: func(_ context.Context, userID, _ string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
			st.calls++
			st.limits["saved"] = limit
			if userID != "user-1" || !before.IsZero() {
				return nil, errors.New("wrong user or cursor")
			}
			return st.saved, fail("saved")
		}},
		SearchService: &fakeSearchStore{searchFn: func(_ context.Context, _, _ string, limit int) ([]rss.SiteWithFeeds, error) {
			st.calls++
			st.limits["sites"] = limit
			return st.sites, fail("sites")
		}},
	}
}

func TestSearchPage(t *testing.T) {
	feed := func(id, title string) rss.Subscription {
		return rss.Subscription{Feed: rss.Feed{ID: id, SiteID: "site-" + id, Title: title}, Site: rss.Site{ID: "site-" + id, Host: id + ".example"}}
	}
	saved := func(id, title string) rss.SavedArticle {
		return rss.SavedArticle{Article: rss.Article{ID: id, Title: title}, Feed: rss.Feed{Title: "Bake Club"}, SavedAt: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)}
	}
	all := searchStores{
		feeds: []rss.Subscription{feed("f1", "Bread Daily"), feed("f2", "Bread Weekly"), feed("f3", "Bread Monthly")},
		saved: []rss.SavedArticle{saved("a1", "Sourdough at home"), saved("a2", "Rye bread"), saved("a3", "Bread and butter")},
		sites: []rss.SiteWithFeeds{
			{Site: rss.Site{ID: "s1", Host: "bread.example", Title: "Bread"}, Feeds: []rss.Feed{{ID: "site-feed-followed", Title: "Bread posts"}, {ID: "site-feed-other", Title: "Bread comments"}}},
		},
	}

	// groups returns the h2s of the results' groups, in order.
	groups := func(t *testing.T, doc *html.Node) []string {
		t.Helper()
		var got []string
		for _, sec := range findAll(doc, func(n *html.Node) bool { return n.Data == "section" && hasClass(n, "search-group") }) {
			for _, h := range findAll(sec, func(n *html.Node) bool { return n.Data == "h2" && n.Parent != nil && hasClass(n.Parent, "list-head") }) {
				got = append(got, textContent(h))
			}
		}
		return got
	}
	parse := func(t *testing.T, rr *httptest.ResponseRecorder) *html.Node {
		t.Helper()
		doc, err := html.Parse(strings.NewReader(rr.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	container := func(t *testing.T, doc *html.Node) *html.Node {
		t.Helper()
		cs := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })
		if len(cs) != 1 {
			t.Fatalf("found %d [data-search-results], want 1", len(cs))
		}
		return cs[0]
	}

	t.Run("before typing, says what it covers and focuses the box", func(t *testing.T) {
		st := all
		s := newTestServerWith(t, st.services())
		rr := searchRequest(t, s, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if st.calls != 0 {
			t.Errorf("stores called %d times with no query, want 0", st.calls)
		}
		body := rr.Body.String()
		for _, want := range []string{
			"Search the sites you follow, what you've saved, and new sites to follow.",
			"Paste a site's address to follow it.",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		doc := parse(t, rr)
		inputs := findAll(doc, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "q" })
		if len(inputs) != 1 || !hasAttr(inputs[0], "autofocus") || attr(inputs[0], "placeholder") != "Articles or sites" {
			t.Errorf("want one q input with autofocus and the placeholder Articles or sites")
		}
		if g := groups(t, doc); len(g) != 0 {
			t.Errorf("groups = %q, want none", g)
		}
		if hasAttr(container(t, doc), "data-announce") {
			t.Error("the results container has data-announce with no query")
		}
	})

	t.Run("each group with up to 3, in order", func(t *testing.T) {
		st := all
		s := newTestServerWith(t, st.services())
		rr := searchRequest(t, s, "bread")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if want := map[string]int{"feeds": 3, "saved": 3, "sites": 3}; !maps.Equal(st.limits, want) {
			t.Errorf("limits = %v, want %v", st.limits, want)
		}
		doc := parse(t, rr)
		if g, want := groups(t, doc), []string{"Your feeds", "Saved", "New sites"}; !slices.Equal(g, want) {
			t.Errorf("groups = %q, want %q", g, want)
		}
		body := rr.Body.String()
		for _, want := range []string{
			`href="/feeds/f1"`, `href="/feeds/f3"`, "From <a",
			`href="/feeds/f1/edit?return_to=%2fsearch%3fq%3dbread"`,
			`href="/articles/a1"`, ">Rye bread</a>", "Bake Club · Saved",
			`href="/sites/s1"`, `href="/feeds/site-feed-other"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		if n := len(findAll(doc, func(n *html.Node) bool { return n.Data == "li" && hasClass(n, "my-feed") })); n != 3 {
			t.Errorf("found %d feed rows, want 3", n)
		}
		if n := len(findAll(doc, func(n *html.Node) bool { return n.Data == "li" && hasClass(n, "search-article") })); n != 3 {
			t.Errorf("found %d saved rows, want 3", n)
		}
		if got := attr(container(t, doc), "data-announce"); got != "7 results" {
			t.Errorf("data-announce = %q, want 7 results", got)
		}
		if strings.Contains(body, "No results") {
			t.Error("no-results message shown with results")
		}
	})

	t.Run("a followed site's feed is marked Following", func(t *testing.T) {
		st := all
		s := newTestServerWith(t, st.services())
		doc := parse(t, searchRequest(t, s, "bread"))
		var marked []string
		for _, li := range findAll(doc, func(n *html.Node) bool {
			return n.Data == "li" && n.Parent != nil && n.Parent.Parent != nil && hasClass(n.Parent.Parent, "result")
		}) {
			if len(findAll(li, func(n *html.Node) bool { return hasClass(n, "subscribed") })) == 1 {
				marked = append(marked, textContent(li))
			}
		}
		if len(marked) != 1 || !strings.Contains(marked[0], "Bread posts") {
			t.Errorf("rows marked Following = %q, want only Bread posts", marked)
		}
	})

	t.Run("empty groups are left out", func(t *testing.T) {
		st := searchStores{saved: all.saved[:1]}
		s := newTestServerWith(t, st.services())
		rr := searchRequest(t, s, "sourdough")
		doc := parse(t, rr)
		if g, want := groups(t, doc), []string{"Saved"}; !slices.Equal(g, want) {
			t.Errorf("groups = %q, want %q", g, want)
		}
		if got := attr(container(t, doc), "data-announce"); got != "1 result" {
			t.Errorf("data-announce = %q, want 1 result", got)
		}
	})

	t.Run("no results", func(t *testing.T) {
		st := searchStores{}
		s := newTestServerWith(t, st.services())
		rr := searchRequest(t, s, "zzz")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "No results for “zzz”.") {
			t.Error("no-results message missing")
		}
		if strings.Contains(body, `action="/lookups"`) {
			t.Error("Look up offered for a query that isn't an address")
		}
		doc := parse(t, rr)
		if g := groups(t, doc); len(g) != 0 {
			t.Errorf("groups = %q, want none", g)
		}
		if got := attr(container(t, doc), "data-announce"); got != "No results" {
			t.Errorf("data-announce = %q, want No results", got)
		}
	})

	t.Run("no results for an address offers Look up", func(t *testing.T) {
		st := searchStores{}
		s := newTestServerWith(t, st.services())
		body := searchRequest(t, s, "https://npr.org/").Body.String()
		for _, want := range []string{"No results for “https://npr.org/”.", `action="/lookups"`, `name="q" value="npr.org"`, "Look up npr.org"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	t.Run("live search markup", func(t *testing.T) {
		st := all
		s := newTestServerWith(t, st.services())
		doc := parse(t, searchRequest(t, s, "bread"))
		forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-live-search") })
		if len(forms) != 1 || attr(forms[0], "action") != "/search" || attr(forms[0], "method") != "get" {
			t.Fatalf("want one GET form[data-live-search] to /search")
		}
		scripts := findAll(doc, func(n *html.Node) bool { return n.Data == "script" && attr(n, "src") == entryJS("search") })
		if len(scripts) != 1 {
			t.Errorf("found %d search entry scripts, want 1", len(scripts))
		}
		container(t, doc)
	})

	t.Run("too long", func(t *testing.T) {
		st := all
		s := newTestServerWith(t, st.services())
		rr := searchRequest(t, s, strings.Repeat("a", 201))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", rr.Code)
		}
		if st.calls != 0 {
			t.Errorf("stores called %d times, want 0", st.calls)
		}
		if !strings.Contains(rr.Body.String(), "Search must be 200 characters or fewer") {
			t.Error("error message missing")
		}
	})

	for _, source := range []string{"feeds", "saved", "sites"} {
		t.Run(source+" error is a 500", func(t *testing.T) {
			st := all
			st.failing, st.err = source, errors.New("boom")
			s := newTestServerWith(t, st.services())
			if rr := searchRequest(t, s, "bread"); rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rr.Code)
			}
		})
	}
}
