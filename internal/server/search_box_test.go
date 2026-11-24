package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"golang.org/x/net/html"
)

// TestSearchBox checks every page's search box is the search-box partial
// with that page's settings: its query, the clear button's link (the page
// without the query), whether it clears in place, whether it's hidden, and
// whether the box keeps spellcheck.
func TestSearchBox(t *testing.T) {
	search := &fakeSearchStore{searchFn: func(context.Context, string, string, string, int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
		return nil, nil, nil
	}}
	saves := &fakeSaveStore{listSavedFn: func(context.Context, string, string, rss.ArticleCursor, int) ([]rss.SavedArticle, error) {
		return savedItems(2), nil
	}}
	s := newTestServerWith(t, Services{SearchService: search, SaveService: saves, ReadService: &fakeReadStore{}, DiscoverService: emptyCatalog()})
	subs := listing(rss.Subscription{Site: rss.Site{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Host: "a.example.com"}, Feed: rss.Feed{ID: testFeedID, Title: "One"}})

	tests := []struct {
		name      string
		render    func(t *testing.T) *httptest.ResponseRecorder
		id        string
		value     string
		clearURL  string
		inPlace   bool
		prose     bool
		clearShow bool
	}{
		{"discover without a query", func(t *testing.T) *httptest.ResponseRecorder { return discoverRequest(t, s, "") },
			"q", "", "/discover", true, false, false},
		{"discover with a query", func(t *testing.T) *httptest.ResponseRecorder { return discoverRequest(t, s, "ex") },
			"q", "ex", "/discover", true, false, true},
		{"saved without a query", func(t *testing.T) *httptest.ResponseRecorder { return getSaved(t, s, "/saved") },
			"saved-q", "", "/saved", false, true, false},
		{"saved with a query", func(t *testing.T) *httptest.ResponseRecorder { return getSaved(t, s, "/saved?q=bread") },
			"saved-q", "bread", "/saved", false, true, true},
		{"my feeds without a query", func(t *testing.T) *httptest.ResponseRecorder { rr, _ := getFeedsURL(t, subs, "/feeds"); return rr },
			"feeds-q", "", "/feeds", true, false, false},
		{"my feeds with a query and a sort", func(t *testing.T) *httptest.ResponseRecorder {
			rr, _ := getFeedsURL(t, subs, "/feeds?sort=updated&q=one")
			return rr
		}, "feeds-q", "one", "/feeds?sort=updated", true, false, true},
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
			fields := findAll(doc, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "search-field") })
			if len(fields) != 1 {
				t.Fatalf("found %d search fields, want 1", len(fields))
			}
			inputs := findAll(fields[0], func(n *html.Node) bool { return n.Data == "input" })
			if len(inputs) != 1 {
				t.Fatalf("found %d inputs in the field, want 1", len(inputs))
			}
			in := inputs[0]
			for key, want := range map[string]string{"type": "search", "id": tt.id, "name": "q", "value": tt.value, "enterkeyhint": "search"} {
				if got := attr(in, key); got != want {
					t.Errorf("input %s = %q, want %q", key, got, want)
				}
			}
			// A query isn't prose: no capitals, corrections or spellcheck.
			for key, off := range map[string]string{"autocapitalize": "none", "autocorrect": "off", "spellcheck": "false"} {
				if got := attr(in, key); (got == off) == tt.prose || hasAttr(in, key) == tt.prose {
					t.Errorf("input %s = %q (set %v), want %q unless prose (prose %v)", key, got, hasAttr(in, key), off, tt.prose)
				}
			}
			if hasAttr(in, "aria-invalid") || hasAttr(in, "autofocus") {
				t.Error("a valid query's input is aria-invalid or autofocused")
			}

			clears := findAll(fields[0], func(n *html.Node) bool { return n.Data == "a" && hasClass(n, "search-clear") })
			if len(clears) != 1 {
				t.Fatalf("found %d clear buttons, want 1", len(clears))
			}
			c := clears[0]
			if got := attr(c, "href"); got != tt.clearURL {
				t.Errorf("clear href = %q, want %q", got, tt.clearURL)
			}
			if got := attr(c, "aria-label"); got != "Clear search" {
				t.Errorf("clear aria-label = %q, want Clear search", got)
			}
			if got := strings.TrimSpace(textContent(c)); got != "" {
				t.Errorf("clear has text %q, want only the icon", got)
			}
			if got, want := attr(c, "data-search-clear"), map[bool]string{true: "in-place", false: ""}[tt.inPlace]; got != want || !hasAttr(c, "data-search-clear") {
				t.Errorf("data-search-clear = %q, want %q", got, want)
			}
			if got := hasAttr(c, "hidden"); got == tt.clearShow {
				t.Errorf("clear hidden = %v, want %v", got, !tt.clearShow)
			}
		})
	}
}

// TestSearchBoxError checks Discover's query errors follow the form fields'
// pattern, from its own search and from a refused lookup: after the field,
// as #q-error, which the input is described by, with the input
// aria-invalid and autofocused, and the clear button shown.
func TestSearchBoxError(t *testing.T) {
	tests := []struct {
		name, msg string
		render    func(t *testing.T) *httptest.ResponseRecorder
	}{
		{"a query over 200 characters", "Search must be 200 characters or fewer", func(t *testing.T) *httptest.ResponseRecorder {
			return discoverRequest(t, newTestServerWith(t, Services{SearchService: failingSearchStore(t)}), strings.Repeat("a", 201))
		}},
		{"a lookup of an invalid address", "Enter a website address like example.com", func(t *testing.T) *httptest.ResponseRecorder {
			return postLookup(t, newTestServer(t), "not a url")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := tt.render(t)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rr.Code)
			}
			doc, err := html.Parse(rr.Body)
			if err != nil {
				t.Fatal(err)
			}
			ins := findAll(doc, func(n *html.Node) bool { return n.Data == "input" && attr(n, "id") == "q" })
			if len(ins) != 1 {
				t.Fatalf("found %d inputs #q, want 1", len(ins))
			}
			in := ins[0]
			if attr(in, "aria-invalid") != "true" || attr(in, "aria-describedby") != "q-error" || !hasAttr(in, "autofocus") {
				t.Errorf("input aria-invalid = %q, aria-describedby = %q, autofocus %v; want true, q-error, set",
					attr(in, "aria-invalid"), attr(in, "aria-describedby"), hasAttr(in, "autofocus"))
			}
			errs := findAll(doc, func(n *html.Node) bool { return n.Data == "p" && attr(n, "id") == "q-error" })
			if len(errs) != 1 {
				t.Fatalf("found %d #q-error, want 1", len(errs))
			}
			if !hasClass(errs[0], "error") || strings.TrimSpace(textContent(errs[0])) != tt.msg {
				t.Errorf("#q-error = %q (class %q), want %q", textContent(errs[0]), attr(errs[0], "class"), tt.msg)
			}
			sib := in.Parent.NextSibling
			for sib != nil && sib.Type != html.ElementNode {
				sib = sib.NextSibling
			}
			if sib != errs[0] {
				t.Error("the error isn't right after the search field")
			}
			clears := findAll(in.Parent, func(n *html.Node) bool { return hasClass(n, "search-clear") })
			if len(clears) != 1 || hasAttr(clears[0], "hidden") {
				t.Error("the clear button isn't shown for the refused query")
			}
		})
	}
}
