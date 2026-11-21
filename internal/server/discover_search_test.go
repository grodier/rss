package server

import (
	"cmp"
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
	subs   []rss.Subscription
	sites  []rss.SiteWithFeeds
	groups []rss.SiteGroup
	// hostSites are the sites the search returns narrowed to a host.
	hostSites map[string][]rss.SiteWithFeeds
	followed  map[string]bool // feed IDs the reader follows, of the sites' feeds
	saved     []rss.SavedArticle
	hasSaves  bool
	lookups   map[string]rss.Lookup // by site key; none means never looked up
	fail      string                // the source that fails: "subs", "saved", "sites", "followed", "hasSaves" or "lookup"

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
		SearchService: &fakeSearchStore{searchFn: func(_ context.Context, _, _, host string, limit int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
			f.limits["sites"] = limit
			if host != "" {
				return f.hostSites[host], nil, err("sites")
			}
			return f.sites, f.groups, err("sites")
		}},
		LookupService: &fakeLookupStore{getBySiteKeyFn: func(_ context.Context, key string) (rss.Lookup, error) {
			l, ok := f.lookups[key]
			if !ok {
				return rss.Lookup{}, cmp.Or(err("lookup"), rss.ErrNoRecord)
			}
			return l, err("lookup")
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

// breadFixture is the sources TestDiscoverSearchResults and
// TestDiscoverSuggestions search for "bread": four followed feeds (one
// gone, one failing), five sites (one with one feed, one with two, one
// with a followed feed and one not, one fully followed, one with none) and
// a saved article.
func breadFixture(now time.Time) *searchFixture {
	gone := searchSub(now, "gone", "Bread Gone", "")
	gone.Feed.GoneAt = now
	failing := searchSub(now, "failing", "Bread Failing", "")
	failing.Feed.ConsecutiveFailures = failingThreshold
	return &searchFixture{
		subs: []rss.Subscription{searchSub(now, "pastry", "Bread & Pastry", "The Daily Crumb"), searchSub(now, "lab", "The Bread Lab", "The Bread Lab"), gone, failing},
		sites: []rss.SiteWithFeeds{
			searchSite("ahead", "Bread Ahead", "Bakery school recipes", rss.Feed{ID: "ahead-journal", Title: "Bread Ahead Journal", Url: "http://www.ahead.example/journal.xml"}),
			searchSite("butter", "Bread and Butter", "Money, plainly", rss.Feed{ID: "butter-money", Title: "Money"}, rss.Feed{ID: "butter-work", Title: "Work"}),
			searchSite("crumb", "The Daily Crumb", "", rss.Feed{ID: "crumb-pastry", Title: "Bread & Pastry"}, rss.Feed{ID: "crumb-world", Title: "World"}),
			searchSite("followed", "Breadline", "", rss.Feed{ID: "followed-1", Title: "Breadline"}),
			searchSite("bare", "Bread Bare", ""),
		},
		followed: map[string]bool{"crumb-pastry": true, "followed-1": true},
		lookups:  map[string]rss.Lookup{"ahead.example": {URL: "https://ahead.example/", HomePage: true, Status: rss.LookupDone, SiteID: "ahead"}},
		saved: []rss.SavedArticle{{
			Article: rss.Article{ID: "a1", Title: "Breadcrumbs from stale bread", Excerpt: "Don't throw out the heel."},
			Feed:    rss.Feed{SiteID: "site-a1", Title: "Small Plates"},
			SavedAt: now.Add(-9 * 24 * time.Hour),
		}},
		hasSaves: true,
	}
}

// searchSub is a followed feed, its latest article two days old.
func searchSub(now time.Time, id, title, site string) rss.Subscription {
	return rss.Subscription{
		Feed:            rss.Feed{ID: id, SiteID: "site-" + id, Title: title, PostsPerWeek: 3},
		Site:            rss.Site{ID: "site-" + id, Host: id + ".example", Title: site},
		LatestArticleAt: now.Add(-48 * time.Hour),
	}
}

func searchSite(id, title, desc string, feeds ...rss.Feed) rss.SiteWithFeeds {
	return rss.SiteWithFeeds{Site: rss.Site{ID: id, Host: id + ".example", Title: title, Description: desc}, Feeds: feeds}
}

// getDiscover requests /discover?q=query as user-1 from a server over f's
// sources.
func getDiscover(t *testing.T, f *searchFixture, query string) (*html.Node, string, int) {
	t.Helper()
	s := newTestServerWith(t, f.services())
	rr := discoverRequestAs(t, s, "user-1", query)
	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	return doc, rr.Body.String(), rr.Code
}

// searchRows are a page's result or suggestion rows, without Look up.
func searchRows(doc *html.Node) []*html.Node {
	return findAll(doc, func(n *html.Node) bool {
		return n.Data == "li" && hasClass(n, "search-result") && !hasClass(n, "search-lookup")
	})
}

func searchRowTitles(doc *html.Node) []string {
	var titles []string
	for _, r := range searchRows(doc) {
		titles = append(titles, textContent(findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-title") })[0]))
	}
	return titles
}

func searchRowByTitle(t *testing.T, doc *html.Node, title string) *html.Node {
	t.Helper()
	for _, r := range searchRows(doc) {
		for _, a := range findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-title") }) {
			if textContent(a) == title {
				return r
			}
		}
	}
	t.Fatalf("no row titled %q", title)
	return nil
}

func searchRowFacts(r *html.Node) string {
	var facts string
	for _, p := range findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-facts") }) {
		facts += textContent(p)
	}
	return strings.ReplaceAll(facts, "\n", "")
}

func searchRowFollowing(r *html.Node) bool {
	return len(findAll(r, func(n *html.Node) bool { return hasClass(n, "following-label") })) == 1
}

// searchAnnounce is the results container's data-announce.
func searchAnnounce(t *testing.T, doc *html.Node) string {
	t.Helper()
	cs := findAll(doc, func(n *html.Node) bool { return hasAttr(n, "data-search-results") })
	if len(cs) != 1 {
		t.Fatalf("found %d results containers, want 1", len(cs))
	}
	return attr(cs[0], "data-announce")
}

// blueskyFixture is the sources of a search for "bsky": the bsky.app
// site, the profiles on it counted as a group (or, narrowed to bsky.app,
// listed), a followed profile, and a followed feed on another host.
func blueskyFixture(now time.Time) *searchFixture {
	alice := searchSub(now, "alice", "Alice", "Alice (@alice.bsky.social) — Bluesky")
	alice.Site.Host = "bsky.app/profile/alice.bsky.social"
	return &searchFixture{
		subs:   []rss.Subscription{alice, searchSub(now, "news", "Bsky News", "")},
		sites:  []rss.SiteWithFeeds{{Site: rss.Site{ID: "bsky", Host: "bsky.app", Title: "Bluesky"}}},
		groups: []rss.SiteGroup{{Host: "bsky.app", Count: 214}},
		hostSites: map[string][]rss.SiteWithFeeds{"bsky.app": {
			{Site: rss.Site{ID: "bob", Host: "bsky.app/profile/bob.bsky.social", Title: "Bob"}, Feeds: []rss.Feed{{ID: "bob-feed", Title: "Bob"}}},
			{Site: rss.Site{ID: "cat", Host: "bsky.app/profile/cat.bsky.social", Title: "Cat"}, Feeds: []rss.Feed{{ID: "cat-feed", Title: "Cat"}}},
		}},
		hasSaves: true,
	}
}

// searchRowHref is a row's link.
func searchRowHref(r *html.Node) string {
	return attr(findAll(r, func(n *html.Node) bool { return hasClass(n, "search-result-title") })[0], "href")
}

// checkGroupRow checks the row of the bsky.app group in doc: its count,
// its link to the sites narrowed to the host, and no Follow.
func checkGroupRow(t *testing.T, doc *html.Node) {
	t.Helper()
	r := searchRowByTitle(t, doc, "bsky.app")
	if got, want := searchRowFacts(r), "214 more sites on bsky.app match"; got != want {
		t.Errorf("group facts = %q, want %q", got, want)
	}
	if got, want := searchRowHref(r), "/discover?host=bsky.app&q=bsky&scope=feeds"; got != want {
		t.Errorf("group href = %q, want %q", got, want)
	}
	if n := len(findAll(r, func(n *html.Node) bool { return n.Data == "form" || n.Data == "button" })); n != 0 {
		t.Errorf("group row has %d forms or buttons, want none", n)
	}
	if !searchRowFollowing(searchRowByTitle(t, doc, "Alice")) {
		t.Error("the followed profile has no row with ✓ Following")
	}
}

func TestDiscoverSearchResults(t *testing.T) {
	now := time.Now()
	fixture := func() *searchFixture { return breadFixture(now) }
	// get asks for the results, as a submit does: with scope, Top unless
	// the query names one.
	get := func(t *testing.T, f *searchFixture, query string) (*html.Node, string, int) {
		t.Helper()
		if !strings.Contains(query, "scope=") {
			query += "&scope=top"
		}
		return getDiscover(t, f, query)
	}
	rows := searchRows
	rowByTitle := searchRowByTitle
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
	announce := searchAnnounce
	sub := func(id, title, site string) rss.Subscription { return searchSub(now, id, title, site) }

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
		titles := searchRowTitles(doc)
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
		facts, following := searchRowFacts, searchRowFollowing
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
			if got := facts(r); got != tt.facts {
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

	t.Run("a pasted address gets Look up, unless a result has it", func(t *testing.T) {
		f := fixture()
		_, body, _ := get(t, f, url.QueryEscape("https://new.example/"))
		for _, want := range []string{`class="search-result search-lookup"`, "Find the feeds this site publishes", `action="/lookups"`, `name="q" value="new.example"`, `aria-label="Look up new.example"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		_, body, _ = get(t, f, url.QueryEscape("https://ahead.example/"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for the home page of a site found")
		}
		_, body, _ = get(t, f, url.QueryEscape("ahead.example/journal.xml"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for a feed of a site found")
		}
		doc, body, _ := get(t, f, url.QueryEscape("ahead.example/hidden.xml"))
		if !strings.Contains(body, `class="search-result search-lookup"`) || !strings.Contains(body, `aria-label="Look up ahead.example/hidden.xml"`) {
			t.Error("no Look up for another address on a site found")
		}
		rowByTitle(t, doc, "Bread Ahead") // and the site's own row is still there

		// A site found from another address hasn't had its home page looked
		// at, nor has one never looked up (found from a feed on another host).
		for name, l := range map[string]*rss.Lookup{
			"found from a feed": {URL: "https://ahead.example/journal.xml", Status: rss.LookupDone, SiteID: "ahead"},
			"never looked up":   nil,
		} {
			f := fixture()
			delete(f.lookups, "ahead.example")
			if l != nil {
				f.lookups["ahead.example"] = *l
			}
			_, body, _ := get(t, f, url.QueryEscape("ahead.example"))
			if !strings.Contains(body, `aria-label="Look up ahead.example"`) {
				t.Errorf("%s: no Look up for the home page", name)
			}
		}

		// Each author on a shared host is a site of its own.
		alice := searchSite("alice", "Alice – Medium", "", rss.Feed{ID: "alice-feed", Title: "Stories by Alice", Url: "https://medium.com/feed/@alice"})
		alice.Site.Host = "medium.com/@alice"
		shared := fixture()
		shared.sites = append(shared.sites, alice)
		shared.lookups["medium.com/@alice"] = rss.Lookup{URL: "https://medium.com/@alice", HomePage: true, Status: rss.LookupDone, SiteID: "alice"}
		_, body, _ = get(t, shared, url.QueryEscape("https://medium.com/@alice"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for the home page of an author found")
		}
		_, body, _ = get(t, shared, url.QueryEscape("https://medium.com/@bob"))
		if !strings.Contains(body, `aria-label="Look up medium.com/@bob"`) {
			t.Error("no Look up for another author on the host of an author found")
		}

		f.fail = "lookup"
		if _, _, code := get(t, f, url.QueryEscape("ahead.example")); code != http.StatusInternalServerError {
			t.Errorf("a failed lookup read: status = %d, want 500", code)
		}
		_, body, _ = get(t, f, url.QueryEscape("new.example")+"&scope=saved")
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered on the Saved tab")
		}
	})

	t.Run("sites on a shared host are grouped", func(t *testing.T) {
		doc, _, _ := get(t, blueskyFixture(now), "bsky")
		checkGroupRow(t, doc)
		_, body, _ := get(t, blueskyFixture(now), "bsky&scope=feeds")
		if !strings.Contains(body, "214 more sites on bsky.app match") {
			t.Error("the Feeds tab has no group row")
		}
	})

	t.Run("narrowed to a shared host", func(t *testing.T) {
		f := blueskyFixture(now)
		doc, _, code := get(t, f, "bsky&scope=feeds&host=bsky.app")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		// The followed profile and the host's sites; not the followed feed
		// on another host, the host's own site, or a group.
		if got, want := searchRowTitles(doc), []string{"Alice", "Bob", "Cat"}; !slices.Equal(got, want) {
			t.Errorf("rows = %q, want %q", got, want)
		}
		if got := tabs(doc); len(got) != 0 {
			t.Errorf("tabs = %q, want none", got)
		}
		narrowed := findAll(doc, func(n *html.Node) bool { return hasClass(n, "search-narrowed") })
		if len(narrowed) != 1 {
			t.Fatalf("found %d narrowed lines, want 1", len(narrowed))
		}
		if got, want := strings.Join(strings.Fields(textContent(narrowed[0])), " "), "Sites on bsky.app that match “bsky” · All results"; got != want {
			t.Errorf("narrowed line = %q, want %q", got, want)
		}
		if a := findAll(narrowed[0], func(n *html.Node) bool { return n.Data == "a" }); len(a) != 1 || attr(a[0], "href") != "/discover?q=bsky&scope=top" {
			t.Error("the narrowed line doesn't link to all the results")
		}
		if f.limits["sites"] != searchTabLimit+1 {
			t.Errorf("sites limit = %d, want %d", f.limits["sites"], searchTabLimit+1)
		}
	})

	t.Run("narrowed to an unknown host", func(t *testing.T) {
		doc, body, code := get(t, blueskyFixture(now), "bsky&scope=feeds&host=nope.example")
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
		if n := len(searchRows(doc)); n != 0 {
			t.Errorf("listed %d rows, want none", n)
		}
		if !strings.Contains(body, "No sites on nope.example match “bsky”.") {
			t.Error("no empty state")
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

func TestDiscoverSuggestions(t *testing.T) {
	now := time.Now()
	// get asks for the suggestions, as live search does: q without scope.
	// Saved articles wait for Enter, so reading them fails the test.
	get := func(t *testing.T, f *searchFixture, query string) (*html.Node, string, int) {
		t.Helper()
		services := f.services()
		services.SaveService = &fakeSaveStore{
			listSavedFn: func(context.Context, string, string, rss.ArticleCursor, int) ([]rss.SavedArticle, error) {
				t.Error("suggestions read saved articles")
				return nil, nil
			},
			hasSavesFn: func(context.Context, string) (bool, error) {
				t.Error("suggestions read whether the reader has saved anything")
				return false, nil
			},
		}
		s := newTestServerWith(t, services)
		rr := discoverRequestAs(t, s, "user-1", query)
		doc, err := html.Parse(strings.NewReader(rr.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		return doc, rr.Body.String(), rr.Code
	}
	isSuggestions := func(doc *html.Node) bool {
		return len(findAll(doc, func(n *html.Node) bool { return hasClass(n, "search-suggestions") })) == 1
	}

	t.Run("q without scope gives suggestions, with scope results", func(t *testing.T) {
		f := breadFixture(now)
		doc, _, code := get(t, f, "bread")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		if !isSuggestions(doc) {
			t.Error("q without scope: no suggestions")
		}
		if want := map[string]int{"subs": 6, "sites": 12}; !maps.Equal(f.limits, want) {
			t.Errorf("limits = %v, want %v", f.limits, want)
		}
		doc, _, _ = getDiscover(t, breadFixture(now), "bread&scope=top")
		if isSuggestions(doc) || len(findAll(doc, func(n *html.Node) bool { return hasClass(n, "search-tabs") })) != 1 {
			t.Error("q with scope: want the results, with their tabs, not suggestions")
		}
	})

	t.Run("at most 6, ranked as the Feeds tab", func(t *testing.T) {
		doc, _, _ := get(t, breadFixture(now), "bread")
		// As the Feeds tab: the followed prefix matches, then the sites'
		// prefix matches; The Bread Lab (a word start) and The Daily Crumb
		// (a match elsewhere) are cut.
		want := []string{"Bread & Pastry", "Bread Failing", "Bread Gone", "Bread Ahead", "Bread and Butter", "Bread Bare"}
		if got := searchRowTitles(doc); !slices.Equal(got, want) {
			t.Errorf("rows = %q, want %q", got, want)
		}
		if got := searchAnnounce(t, doc); got != "6 feeds" {
			t.Errorf("data-announce = %q, want 6 feeds", got)
		}

		f := breadFixture(now)
		f.subs, f.sites = nil, nil
		for i := range 10 {
			f.subs = append(f.subs, searchSub(now, fmt.Sprintf("s%02d", i), fmt.Sprintf("Bread %02d", i), ""))
		}
		doc, _, _ = get(t, f, "bread")
		if n := len(searchRows(doc)); n != 6 {
			t.Errorf("listed %d suggestions, want 6", n)
		}
	})

	t.Run("each facts line, ✓ Following only on followed feeds, and no buttons", func(t *testing.T) {
		f := breadFixture(now)
		f.subs = f.subs[:2] // Bread & Pastry, The Bread Lab, so every site fits in 6
		doc, _, _ := get(t, f, "bread")
		tests := []struct {
			title, facts string
			following    bool
		}{
			{"Bread & Pastry", "From The Daily Crumb" + "Latest 2 days ago", true},
			{"The Bread Lab", "Latest 2 days ago", true},
			{"Bread Ahead", "Bread Ahead Journal" + "ahead.example", false},
			{"Bread and Butter", "butter.example" + "2 feeds", false},
			{"The Daily Crumb", "World" + "crumb.example", false},
			{"Bread Bare", "bare.example", false},
		}
		if n := len(searchRows(doc)); n != len(tests) {
			t.Errorf("listed %d suggestions, want %d", n, len(tests))
		}
		for _, tt := range tests {
			r := searchRowByTitle(t, doc, tt.title)
			if got := searchRowFacts(r); got != tt.facts {
				t.Errorf("%s: facts = %q, want %q", tt.title, got, tt.facts)
			}
			if got := searchRowFollowing(r); got != tt.following {
				t.Errorf("%s: ✓ Following = %v, want %v", tt.title, got, tt.following)
			}
			if n := len(findAll(r, func(n *html.Node) bool { return n.Data == "form" || n.Data == "button" })); n != 0 {
				t.Errorf("%s: %d forms or buttons, want none", tt.title, n)
			}
		}
		if n := len(findAll(doc, func(n *html.Node) bool { return hasClass(n, "search-result-title") && textContent(n) == "Breadline" })); n != 0 {
			t.Error("a site whose feeds the reader all follows is listed")
		}
	})

	t.Run("sites on a shared host are grouped", func(t *testing.T) {
		doc, _, _ := get(t, blueskyFixture(now), "bsky")
		checkGroupRow(t, doc)
	})

	t.Run("a pasted address gets Look up, unless a suggestion has it", func(t *testing.T) {
		_, body, _ := get(t, breadFixture(now), url.QueryEscape("https://new.example/"))
		for _, want := range []string{`class="search-result search-lookup"`, `action="/lookups"`, `name="q" value="new.example"`} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		_, body, _ = get(t, breadFixture(now), url.QueryEscape("https://ahead.example/"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for the home page of a site found")
		}
		_, body, _ = get(t, breadFixture(now), url.QueryEscape("ahead.example/journal.xml"))
		if strings.Contains(body, "search-lookup") {
			t.Error("Look up offered for a feed of a site found")
		}
		doc, body, _ := get(t, breadFixture(now), url.QueryEscape("ahead.example/hidden.xml"))
		if !strings.Contains(body, `class="search-result search-lookup"`) || !strings.Contains(body, `aria-label="Look up ahead.example/hidden.xml"`) {
			t.Error("no Look up for another address on a site found")
		}
		searchRowByTitle(t, doc, "Bread Ahead")

		f := breadFixture(now)
		f.lookups["ahead.example"] = rss.Lookup{URL: "https://ahead.example/journal.xml", Status: rss.LookupDone, SiteID: "ahead"}
		if _, body, _ := get(t, f, url.QueryEscape("ahead.example")); !strings.Contains(body, `aria-label="Look up ahead.example"`) {
			t.Error("no Look up for the home page of a site found from a feed")
		}
	})

	t.Run("the last row links to the results", func(t *testing.T) {
		doc, _, _ := get(t, breadFixture(now), "bread")
		last := findAll(doc, func(n *html.Node) bool { return n.Data == "li" && hasClass(n, "search-everything") })
		if len(last) != 1 {
			t.Fatalf("found %d Search everything rows, want 1", len(last))
		}
		if l := lastElementChild(last[0].Parent); l != last[0] {
			t.Error("Search everything isn't the last row")
		}
		links := findAll(last[0], func(n *html.Node) bool { return n.Data == "a" })
		if len(links) != 1 || attr(links[0], "href") != "/discover?q=bread&scope=top" {
			t.Fatalf("Search everything's links = %d, want one to the results' Top tab", len(links))
		}
		if got := textContent(links[0]); got != "Search everything for “bread”Enter" {
			t.Errorf("Search everything reads %q", got)
		}
		// Enter's hint is the keyboard's, so screen readers skip it.
		if n := len(findAll(links[0], func(n *html.Node) bool {
			return n.Data == "kbd" && textContent(n) == "Enter" && attr(n, "aria-hidden") == "true"
		})); n != 1 {
			t.Errorf("found %d hidden Enter hints, want 1", n)
		}
	})

	t.Run("no feeds, or one", func(t *testing.T) {
		doc, body, _ := get(t, &searchFixture{}, "zzz")
		if n := len(searchRows(doc)); n != 0 {
			t.Errorf("listed %d suggestions, want none", n)
		}
		if !strings.Contains(body, "search-everything") || strings.Contains(body, `class="empty"`) {
			t.Error("want only the Search everything row, without a no-results line")
		}
		if got := searchAnnounce(t, doc); got != "No feeds" {
			t.Errorf("data-announce = %q, want No feeds", got)
		}
		doc, _, _ = get(t, &searchFixture{subs: []rss.Subscription{searchSub(now, "one", "Bread", "")}}, "bread")
		if got := searchAnnounce(t, doc); got != "1 feed" {
			t.Errorf("data-announce = %q, want 1 feed", got)
		}
	})

	t.Run("the form's scope keeps the results' tab", func(t *testing.T) {
		scope := func(doc *html.Node) string {
			for _, n := range findAll(doc, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "scope" }) {
				return attr(n, "value")
			}
			return ""
		}
		doc, _, _ := getDiscover(t, breadFixture(now), "bread&scope=saved")
		if got := scope(doc); got != "saved" {
			t.Errorf("on the Saved tab, scope = %q, want saved", got)
		}
		doc, _, _ = get(t, breadFixture(now), "bread")
		if got := scope(doc); got != "top" {
			t.Errorf("on the suggestions, scope = %q, want top", got)
		}
	})

	for _, source := range []string{"subs", "sites", "followed"} {
		t.Run(source+" error is a 500", func(t *testing.T) {
			f := breadFixture(now)
			f.fail = source
			if _, _, code := get(t, f, "bread"); code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", code)
			}
		})
	}
}

// lastElementChild is n's last child element, or nil.
func lastElementChild(n *html.Node) *html.Node {
	for c := n.LastChild; c != nil; c = c.PrevSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}
