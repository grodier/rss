package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/rss"
)

func TestMatchRank(t *testing.T) {
	tests := []struct {
		text, q string
		want    int
	}{
		{"Bread", "bread", 4},
		{"Bread", " bread ", 4},
		{"Bread & Pastry", "bread", 3},
		{"The Bread Lab", "bread", 2},
		{"Shortbread", "bread", 1},
		{"Sourdough", "bread", 0},
		{"Bread", "", 0},
		{"", "bread", 0},
	}
	for _, tt := range tests {
		if got := matchRank(tt.text, tt.q); got != tt.want {
			t.Errorf("matchRank(%q, %q) = %d, want %d", tt.text, tt.q, got, tt.want)
		}
	}
}

func TestSearchRanking(t *testing.T) {
	t.Run("scores", func(t *testing.T) {
		tests := []struct {
			name              string
			titleRank         int
			elsewhere, recent bool
			boost, want       int
		}{
			{"a whole-title match", 4, false, false, 0, 40},
			{"a title match beats any match elsewhere", 1, false, false, 0, 10},
			{"a match only elsewhere", 0, true, false, 0, 5},
			{"a match elsewhere adds nothing to a title match", 2, true, false, 0, 20},
			{"a followed feed", 3, false, false, boostFollowing, 33},
			{"a saved article", 3, false, false, boostSaved, 32},
			{"something recent", 3, false, true, 0, 31},
		}
		for _, tt := range tests {
			if got := searchScore(tt.titleRank, tt.elsewhere, tt.boost, tt.recent); got != tt.want {
				t.Errorf("%s: searchScore = %d, want %d", tt.name, got, tt.want)
			}
		}
	})

	t.Run("order", func(t *testing.T) {
		rows := []searchResult{
			{Title: "other b", Score: 30},
			{Title: "saved", Score: 30, Saved: true},
			{Title: "best", Score: 40},
			{Title: "followed", Score: 30, Following: true},
			{Title: "Other a", Score: 30},
			{Title: "worst", Score: 5, Following: true},
		}
		rankResults(rows)
		var got []string
		for _, r := range rows {
			got = append(got, r.Title)
		}
		// By score, then followed feeds, saved articles and other feeds,
		// then title ignoring case.
		want := []string{"best", "followed", "saved", "Other a", "other b", "worst"}
		if !slices.Equal(got, want) {
			t.Errorf("order = %q, want %q", got, want)
		}
	})
}

// searchFixture is Discover's search's sources for TestDiscoverSearchResults.
type searchFixture struct {
	subs     []rss.Subscription
	sites    []rss.SiteWithFeeds
	followed map[string]bool // feed IDs the reader follows, of the sites' feeds
	saved    []rss.SavedArticle
	hasSaves bool
	fail     string // the source that fails: "subs", "saved", "sites", "followed" or "hasSaves"

	limits map[string]int
}

func (f *searchFixture) services() Services {
	f.limits = map[string]int{}
	err := func(name string) error {
		if f.fail == name {
			return errors.New("boom")
		}
		return nil
	}
	return Services{
		SubscriptionService: &fakeSubscriptionStore{
			searchFn: func(_ context.Context, userID, _ string, limit int) ([]rss.Subscription, error) {
				f.limits["subs"] = limit
				return f.subs, err("subs")
			},
			subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
				return f.followed, err("followed")
			},
		},
		SearchService: &fakeSearchStore{searchFn: func(_ context.Context, _, _ string, limit int) ([]rss.SiteWithFeeds, error) {
			f.limits["sites"] = limit
			return f.sites, err("sites")
		}},
		SaveService: &fakeSaveStore{
			listSavedFn: func(_ context.Context, _, _ string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
				f.limits["saved"] = limit
				if !before.IsZero() {
					return nil, errors.New("want the first page")
				}
				return f.saved, err("saved")
			},
			hasSavesFn: func(context.Context, string) (bool, error) { return f.hasSaves, err("hasSaves") },
		},
	}
}

func TestDiscoverSearchResults(t *testing.T) {
	now := time.Now()
	sub := func(id, title, site string) rss.Subscription {
		return rss.Subscription{
			Feed:            rss.Feed{ID: id, SiteID: "site-" + id, Title: title, PostsPerWeek: 3},
			Site:            rss.Site{ID: "site-" + id, Host: id + ".example", Title: site},
			LatestArticleAt: now.Add(-48 * time.Hour),
		}
	}
	site := func(id, title, desc string, feeds ...rss.Feed) rss.SiteWithFeeds {
		return rss.SiteWithFeeds{Site: rss.Site{ID: id, Host: id + ".example", Title: title, Description: desc}, Feeds: feeds}
	}
	saved := func(id, title, excerpt string) rss.SavedArticle {
		return rss.SavedArticle{Article: rss.Article{ID: id, Title: title, Excerpt: excerpt}, Feed: rss.Feed{SiteID: "site-" + id, Title: "Small Plates"}, SavedAt: now.Add(-9 * 24 * time.Hour)}
	}
	gone := sub("gone", "Bread Gone", "")
	gone.Feed.GoneAt = now
	failing := sub("failing", "Bread Failing", "")
	failing.Feed.ConsecutiveFailures = failingThreshold

	fixture := func() *searchFixture {
		return &searchFixture{
			subs: []rss.Subscription{sub("pastry", "Bread & Pastry", "The Daily Crumb"), sub("lab", "The Bread Lab", "The Bread Lab"), gone, failing},
			sites: []rss.SiteWithFeeds{
				site("ahead", "Bread Ahead", "Bakery school recipes", rss.Feed{ID: "ahead-journal", Title: "Bread Ahead Journal"}),
				site("butter", "Bread and Butter", "Money, plainly", rss.Feed{ID: "butter-money", Title: "Money"}, rss.Feed{ID: "butter-work", Title: "Work"}),
				site("crumb", "The Daily Crumb", "", rss.Feed{ID: "crumb-pastry", Title: "Bread & Pastry"}, rss.Feed{ID: "crumb-world", Title: "World"}),
				site("followed", "Breadline", "", rss.Feed{ID: "followed-1", Title: "Breadline"}),
				site("bare", "Bread Bare", ""),
			},
			followed: map[string]bool{"crumb-pastry": true, "followed-1": true},
			saved:    []rss.SavedArticle{saved("a1", "Breadcrumbs from stale bread", "Don't throw out the heel.")},
			hasSaves: true,
		}
	}
	get := func(t *testing.T, f *searchFixture, query string) (*html.Node, string, int) {
		t.Helper()
		s := newTestServerWith(t, f.services())
		rr := discoverRequestAs(t, s, "user-1", query)
		doc, err := html.Parse(strings.NewReader(rr.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		return doc, rr.Body.String(), rr.Code
	}
	rows := func(doc *html.Node) []*html.Node {
		return findAll(doc, func(n *html.Node) bool {
			return n.Data == "li" && hasClass(n, "search-result") && !hasClass(n, "search-lookup")
		})
	}
	rowByTitle := func(t *testing.T, doc *html.Node, title string) *html.Node {
		t.Helper()
		for _, r := range rows(doc) {
			for _, a := range findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-title") }) {
				if textContent(a) == title {
					return r
				}
			}
		}
		t.Fatalf("no row titled %q", title)
		return nil
	}
	tabs := func(doc *html.Node) []string {
		var got []string
		for _, a := range findAll(doc, func(n *html.Node) bool {
			return n.Data == "a" && n.Parent != nil && n.Parent.Data == "li" && hasClassAncestor(n, "search-tabs")
		}) {
			label := strings.Join(strings.Fields(textContent(a)), " ")
			if attr(a, "aria-current") == "page" {
				label += " (current)"
			}
			got = append(got, label)
		}
		return got
	}
	announce := func(t *testing.T, doc *html.Node) string {
		t.Helper()
		cs := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })
		if len(cs) != 1 {
			t.Fatalf("found %d results containers, want 1", len(cs))
		}
		return attr(cs[0], "data-announce")
	}

	t.Run("each source is read with one more than a tab lists", func(t *testing.T) {
		f := fixture()
		get(t, f, "bread")
		if want := map[string]int{"subs": 51, "saved": 51, "sites": 51}; !maps.Equal(f.limits, want) {
			t.Errorf("limits = %v, want %v", f.limits, want)
		}
	})

	t.Run("tabs, Top current, its rows ranked", func(t *testing.T) {
		doc, _, code := get(t, fixture(), "bread")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		if got, want := tabs(doc), []string{"Top (current)", "Feeds 8", "Saved 1"}; !slices.Equal(got, want) {
			t.Errorf("tabs = %q, want %q", got, want)
		}
		var titles []string
		for _, r := range rows(doc) {
			titles = append(titles, textContent(findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-title") })[0]))
		}
		// Bread & Pastry (prefix, followed) beats Bread Ahead (prefix); the
		// saved Breadcrumbs (prefix, saved) sits between; The Bread Lab is a
		// word start; the fully-followed Breadline isn't a site row.
		want := []string{"Bread & Pastry", "Bread Failing", "Bread Gone", "Breadcrumbs from stale bread", "Bread Ahead", "Bread and Butter", "Bread Bare", "The Bread Lab", "The Daily Crumb"}
		if !slices.Equal(titles, want) {
			t.Errorf("rows = %q, want %q", titles, want)
		}
		if got := announce(t, doc); got != "9 results" {
			t.Errorf("data-announce = %q, want 9 results", got)
		}
	})

	t.Run("each row's facts and state", func(t *testing.T) {
		doc, _, _ := get(t, fixture(), "bread")
		facts := func(r *html.Node) string {
			return textContent(findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-facts") })[0])
		}
		following := func(r *html.Node) bool {
			return len(findAll(r, func(n *html.Node) bool { return hasClass(n, "following-label") })) == 1
		}
		follow := func(r *html.Node) string {
			for _, f := range findAll(r, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-toggle") }) {
				return attr(f, "action")
			}
			return ""
		}
		tests := []struct {
			title, facts, follow string
			following            bool
		}{
			{"Bread & Pastry", "From The Daily Crumb" + "About 3 posts a week" + "Latest 2 days ago", "", true},
			{"The Bread Lab", "About 3 posts a week" + "Latest 2 days ago", "", true},
			{"Bread Gone", "From gone.example" + "About 3 posts a week" + "Latest 2 days ago" + "No longer exists", "", true},
			{"Bread Failing", "From failing.example" + "About 3 posts a week" + "Latest 2 days ago" + "Failing to update", "", true},
			{"Bread Ahead", "Bread Ahead Journal" + "ahead.example", "/feeds/ahead-journal/subscribe", false},
			{"Bread and Butter", "butter.example" + "2 feeds", "", false},
			{"The Daily Crumb", "World" + "crumb.example" + "You follow its other feed", "/feeds/crumb-world/subscribe", false},
			{"Bread Bare", "bare.example" + "No feeds found yet", "", false},
			{"Breadcrumbs from stale bread", "Saved 9 days ago" + "Small Plates", "", false},
		}
		for _, tt := range tests {
			r := rowByTitle(t, doc, tt.title)
			if got := strings.ReplaceAll(facts(r), "\n", ""); got != tt.facts {
				t.Errorf("%s: facts = %q, want %q", tt.title, got, tt.facts)
			}
			if got := following(r); got != tt.following {
				t.Errorf("%s: ✓ Following = %v, want %v", tt.title, got, tt.following)
			}
			if got := follow(r); got != tt.follow {
				t.Errorf("%s: Follow = %q, want %q", tt.title, got, tt.follow)
			}
		}
		if rowsTitled := findAll(doc, func(n *html.Node) bool { return hasClass(n, "search-result-title") && textContent(n) == "Breadline" }); len(rowsTitled) != 0 {
			t.Error("a site whose feeds the reader all follows is listed")
		}
		saved := rowByTitle(t, doc, "Breadcrumbs from stale bread")
		if !hasClass(saved, "search-result-saved") || len(findAll(saved, func(n *html.Node) bool {
			return hasClass(n, "search-result-desc") && textContent(n) == "Don't throw out the heel."
		})) != 1 {
			t.Error("the saved row isn't marked saved, or lacks its excerpt")
		}
	})

	t.Run("the Feeds and Saved tabs, and an unknown scope", func(t *testing.T) {
		doc, _, _ := get(t, fixture(), "bread&scope=feeds")
		if got := tabs(doc); got[1] != "Feeds 8 (current)" {
			t.Errorf("tabs = %q, want Feeds current", got)
		}
		if n := len(rows(doc)); n != 8 {
			t.Errorf("Feeds lists %d rows, want 8", n)
		}
		doc, _, _ = get(t, fixture(), "bread&scope=saved")
		if n := len(rows(doc)); n != 1 {
			t.Errorf("Saved lists %d rows, want 1", n)
		}
		doc, _, _ = get(t, fixture(), "bread&scope=nope")
		if got := tabs(doc); got[0] != "Top (current)" {
			t.Errorf("tabs = %q, want Top current for an unknown scope", got)
		}
	})

	t.Run("no tabs for a reader who has saved nothing", func(t *testing.T) {
		f := fixture()
		f.hasSaves, f.saved = false, nil
		doc, _, _ := get(t, f, "bread&scope=saved")
		if got := tabs(doc); len(got) != 0 {
			t.Errorf("tabs = %q, want none", got)
		}
		if n := len(rows(doc)); n != 8 {
			t.Errorf("rows = %d, want the 8 feeds", n)
		}
	})

	t.Run("Top lists 10 and links the tabs with more", func(t *testing.T) {
		f := fixture()
		f.subs = nil
		for i := range 51 {
			f.subs = append(f.subs, sub(fmt.Sprintf("s%02d", i), fmt.Sprintf("Bread %02d", i), ""))
		}
		f.sites = nil
		doc, body, code := get(t, f, "bread")
		if code != http.StatusOK {
			t.Fatalf("status = %d: %s", code, body)
		}
		if n := len(rows(doc)); n != 10 {
			t.Errorf("Top lists %d rows, want 10", n)
		}
		if got := tabs(doc); got[1] != "Feeds 50+" {
			t.Errorf("tabs = %q, want Feeds 50+", got)
		}
		if !strings.Contains(body, `More in <a href="/discover?q=bread&amp;scope=feeds">Feeds (50&#43;)</a> · <a href="/discover?q=bread&amp;scope=saved">Saved (1)</a>`) {
			i := strings.Index(body, "search-more")
			t.Errorf(`"More in" line missing or wrong: %s`, body[max(0, i-20):min(len(body), i+400)])
		}
		doc, _, _ = get(t, f, "bread&scope=feeds")
		if n := len(rows(doc)); n != 50 {
			t.Errorf("Feeds lists %d rows, want 50", n)
		}
	})

	t.Run("a pasted address gets Look up, unless its site was found", func(t *testing.T) {
		f := fixture()
		_, body, _ := get(t, f, url.QueryEscape("https://new.example/"))
		for _, want := range []string{`class="search-result search-lookup"`, "Find the feeds this site publishes", `action="/lookups"`, `name="q" value="new.example"`, `aria-label="Look up new.example"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		_, body, _ = get(t, f, url.QueryEscape("https://ahead.example/"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for an address whose site was found")
		}
		_, body, _ = get(t, f, url.QueryEscape("new.example")+"&scope=saved")
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered on the Saved tab")
		}
	})

	t.Run("no results", func(t *testing.T) {
		empty := &searchFixture{hasSaves: true}
		tests := []struct{ query, want string }{
			{"zzz", "No results for “zzz”."},
			{"zzz&scope=feeds", "No feeds match “zzz”."},
			{"zzz&scope=saved", "No saved articles match “zzz”."},
		}
		for _, tt := range tests {
			doc, body, _ := get(t, empty, tt.query)
			if !strings.Contains(body, tt.want) {
				t.Errorf("%s: body missing %q", tt.query, tt.want)
			}
			if got := announce(t, doc); got != "No results" {
				t.Errorf("%s: data-announce = %q, want No results", tt.query, got)
			}
		}
		f := &searchFixture{}
		_, body, _ := get(t, f, "zzz")
		if !strings.Contains(body, "No results for “zzz”.") {
			t.Error("no-results line missing without tabs")
		}
	})

	for _, source := range []string{"subs", "saved", "sites", "followed", "hasSaves"} {
		t.Run(source+" error is a 500", func(t *testing.T) {
			f := fixture()
			f.fail = source
			if _, _, code := get(t, f, "bread"); code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", code)
			}
		})
	}
}

// hasClassAncestor reports whether n or one of its ancestors has class.
func hasClassAncestor(n *html.Node, class string) bool {
	for ; n != nil; n = n.Parent {
		if n.Type == html.ElementNode && hasClass(n, class) {
			return true
		}
	}
	return false
}
