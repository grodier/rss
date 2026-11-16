package admin

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
)

// testSeedList is the seed list the seed tests' server has.
var testSeedList = []seed.Entry{
	{URL: "https://found.example/", SiteKey: "found.example"},
	{URL: "https://failed.example/", SiteKey: "failed.example"},
	{URL: "https://feeds.example/rss.xml", SiteKey: "feeds.example"},
}

// testSeedEntries are an entry in each state: found with feeds in
// Discover, found with none in it, failed with an error that's markup,
// failed after finding a site, no feeds, waiting, never looked up, and
// running.
func testSeedEntries() []rss.AdminSeedEntry {
	at := testNow.Add(-time.Hour)
	entry := func(key string, l rss.Lookup) rss.AdminSeedEntry {
		if l.Status != "" {
			l.ID, l.SiteKey = "l-"+key, key
		}
		return rss.AdminSeedEntry{URL: "https://" + key + "/", SiteKey: key, Lookup: l}
	}
	found := entry("found.example", rss.Lookup{Status: rss.LookupDone, SiteID: siteID(1), FinishedAt: at})
	found.SiteHost, found.Feeds, found.InDiscover = "www.found.example", 3, 2
	single := entry("single.example", rss.Lookup{Status: rss.LookupDone, SiteID: siteID(2), FinishedAt: at})
	single.SiteHost, single.Feeds = "single.example", 1
	refailed := entry("refailed.example", rss.Lookup{Status: rss.LookupFailed, SiteID: siteID(3), Error: "timeout", FinishedAt: at})
	refailed.SiteHost, refailed.Feeds = "refailed.example", 2
	feedURL := entry("feeds.example", rss.Lookup{Status: rss.LookupPending, RequestedAt: at})
	feedURL.URL = "https://feeds.example/rss.xml"
	return []rss.AdminSeedEntry{
		found,
		single,
		entry("failed.example", rss.Lookup{Status: rss.LookupFailed, Error: `<script>alert("seed")</script>`, FinishedAt: at}),
		refailed,
		entry("empty.example", rss.Lookup{Status: rss.LookupDone, FinishedAt: at}),
		feedURL,
		entry("new.example", rss.Lookup{}),
		entry("running.example", rss.Lookup{Status: rss.LookupRunning, StartedAt: at}),
	}
}

// seedServer returns a logged-in browser on a server with testSeedList
// and feeds.
func seedServer(t *testing.T, feeds *fakeFeeds) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001", AppURL: testAppURL, SeedList: testSeedList}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Feeds: feeds, Audit: &fakeAudit{}})
	return loggedIn(t, s)
}

func TestSeedPage(t *testing.T) {
	b := seedServer(t, &fakeFeeds{seed: testSeedEntries()})

	rr := b.get(t, "/seed")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /seed = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/seed" aria-current="page">Seed list</a>`,
		// The filters with their counts; All is current.
		`<a href="/seed" aria-current="page">All <span class="seg-count">8</span></a>`,
		`<a href="/seed?filter=not-found">Not found <span class="seg-count">3</span></a>`,
		`<a href="/seed?filter=waiting">Waiting <span class="seg-count">3</span></a>`,
		`<a href="/seed?filter=not-in-discover">Found but not in Discover <span class="seg-count">1</span></a>`,
		// Each state, in words, the error escaped.
		`<strong>Found 3 feeds</strong>`,
		`<strong>Found 1 feed</strong>`,
		`<span class="problem problem-bad" title="&lt;script&gt;alert(&#34;seed&#34;)&lt;/script&gt;"><strong>Failed</strong><span class="problem-detail"> · &lt;script&gt;alert(&#34;seed&#34;)&lt;/script&gt;</span></span>`,
		`<span class="problem problem-bad"><strong>No feeds</strong></span>`,
		`<strong>Waiting</strong><span class="problem-detail"> · not queued yet</span>`,
		`<strong>Running</strong>`,
		// The site found, linking to its page, and its feeds in Discover.
		`<a href="/sites/` + siteID(1) + `">www.found.example</a>`,
		`<td class="num">2</td>`,
		// An entry that isn't a home page shows its address.
		`feeds.example<small>https://feeds.example/rss.xml</small>`,
		// Look up again posts the entry, even one whose lookup found a
		// site.
		`<input type="hidden" name="site_key" value="refailed.example" />`,
		`<input type="hidden" name="site_key" value="failed.example" />`,
		`<input type="hidden" name="site_key" value="empty.example" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("seed page lacks %s", want)
		}
	}
	if strings.Contains(body, `<script>alert("seed")`) {
		t.Error("seed page renders a lookup's error as markup")
	}
	if n := strings.Count(body, ">Look up again</button>"); n != 3 {
		t.Errorf("seed page has %d Look up again buttons, want 3 (the failed and no-feeds entries)", n)
	}
}

func TestSeedPageFilters(t *testing.T) {
	b := seedServer(t, &fakeFeeds{seed: testSeedEntries()})
	tests := []struct {
		query     string
		want, not []string
	}{
		{"?filter=not-found", []string{"failed.example", "refailed.example", "empty.example"}, []string{"found.example<", "new.example", "running.example"}},
		{"?filter=waiting", []string{"feeds.example", "new.example", "running.example"}, []string{"failed.example", "single.example"}},
		{"?filter=not-in-discover", []string{"single.example"}, []string{"www.found.example", "failed.example", "new.example"}},
		// A filter the page doesn't offer is All.
		{"?filter=bogus", []string{"found.example", "running.example"}, nil},
	}
	for _, tt := range tests {
		rr := b.get(t, "/seed"+tt.query)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /seed%s = %d, want 200", tt.query, rr.Code)
		}
		body := rr.Body.String()
		for _, want := range tt.want {
			if !strings.Contains(body, want) {
				t.Errorf("GET /seed%s lacks %s", tt.query, want)
			}
		}
		for _, not := range tt.not {
			if strings.Contains(body, not) {
				t.Errorf("GET /seed%s shows %s", tt.query, not)
			}
		}
	}

	if rr := b.get(t, "/seed?filter=not-found"); !strings.Contains(rr.Body.String(), `<input type="hidden" name="filter" value="not-found" />`) {
		t.Error("Look up again doesn't keep the filter")
	}
}

func TestSeedPageEmpty(t *testing.T) {
	b := seedServer(t, &fakeFeeds{})
	if body := b.get(t, "/seed").Body.String(); !strings.Contains(body, "The seed list is empty.") {
		t.Error("empty seed list doesn't say so")
	}
	b = seedServer(t, &fakeFeeds{seed: testSeedEntries()[:1]})
	if body := b.get(t, "/seed?filter=not-found").Body.String(); !strings.Contains(body, "No entries here.") {
		t.Error("an empty filter doesn't say so")
	}
}

func TestSeedLookup(t *testing.T) {
	feeds := &fakeFeeds{}
	b := seedServer(t, feeds)

	rr := b.post(t, "/seed/lookup", url.Values{"site_key": {"failed.example"}, "filter": {"not-found"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/seed?filter=not-found" {
		t.Fatalf("POST /seed/lookup = %d to %q, want 303 to /seed?filter=not-found", rr.Code, rr.Header().Get("Location"))
	}
	if len(feeds.calls) != 1 || feeds.calls[0].Action != "seed-lookup" || feeds.calls[0].ID != "failed.example" || feeds.calls[0].By.AdminEmail != testEmail {
		t.Errorf("calls = %+v, want one seed-lookup of failed.example by %s", feeds.calls, testEmail)
	}
	if body := html.UnescapeString(b.get(t, "/seed?filter=not-found").Body.String()); !strings.Contains(body, "Queued a lookup of failed.example") {
		t.Error("seed page doesn't flash the lookup")
	}

	// A lookup already running is left to finish.
	feeds.seedStatus = rss.LookupRunning
	b.post(t, "/seed/lookup", url.Values{"site_key": {"feeds.example"}})
	if body := html.UnescapeString(b.get(t, "/seed").Body.String()); !strings.Contains(body, "A lookup of feeds.example is already running") {
		t.Error("seed page doesn't say the lookup was already running")
	}
	feeds.seedStatus = ""

	// A filter the page doesn't offer goes back to All.
	if rr := b.post(t, "/seed/lookup", url.Values{"site_key": {"found.example"}, "filter": {"bogus"}}); rr.Header().Get("Location") != "/seed" {
		t.Errorf("POST /seed/lookup with a bogus filter goes to %q, want /seed", rr.Header().Get("Location"))
	}

	// A site key that isn't on the list.
	for _, key := range []string{"other.example", ""} {
		if rr := b.post(t, "/seed/lookup", url.Values{"site_key": {key}}); rr.Code != http.StatusNotFound {
			t.Errorf("POST /seed/lookup %q = %d, want 404", key, rr.Code)
		}
	}
	if len(feeds.calls) != 3 {
		t.Errorf("calls = %+v, want only the three seed list entries'", feeds.calls)
	}

	feeds.err = errors.New("db down")
	if rr := b.post(t, "/seed/lookup", url.Values{"site_key": {"found.example"}}); rr.Code != http.StatusInternalServerError {
		t.Errorf("POST /seed/lookup failing = %d, want 500", rr.Code)
	}
}

func TestSeedStatus(t *testing.T) {
	for _, e := range testSeedEntries() {
		got := seedStatus(e)
		want := map[string]string{
			rss.SeedFound: "Found", rss.SeedFailed: "Failed", rss.SeedNoFeeds: "No feeds",
			rss.SeedWaiting: "Waiting", rss.SeedRunning: "Running",
		}[e.State()]
		if !strings.HasPrefix(got.Text, want) {
			t.Errorf("seedStatus(%s, %s) = %q, want it to start %q", e.SiteKey, e.State(), got.Text, want)
		}
	}
}

// testCatalog is three sections: Science with two feeds, Food with none,
// and Film and TV with one never posted to.
func testCatalog() []rss.AdminCatalogSection {
	return []rss.AdminCatalogSection{
		{Section: testSections[0], Feeds: []rss.AdminCatalogFeed{
			{ID: feedID(1), Title: "Quanta", SiteHost: "quanta.example", PostsPerWeek: 4.25, LatestPost: testNow.Add(-3 * time.Hour), Subscribers: 1234},
			{ID: feedID(2), Title: `<b>Nautilus</b>`, SiteHost: "nautilus.example", GoneAt: testNow.Add(-24 * time.Hour)},
		}},
		{Section: testSections[1]},
		{Section: testSections[2], Feeds: []rss.AdminCatalogFeed{{ID: feedID(3), URL: "https://film.example/feed", SiteHost: "film.example"}}},
	}
}

func TestDiscoverPage(t *testing.T) {
	b := seedServer(t, &fakeFeeds{catalog: testCatalog()})

	rr := b.get(t, "/discover")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /discover = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/discover" aria-current="page">Discover</a>`,
		// A link per section, in their order, with its feeds' count.
		`<a href="/discover" aria-current="page">All <span class="seg-count">3</span></a>`,
		`<a href="/discover?section=science">Science <span class="seg-count">2</span></a>`,
		`<a href="/discover?section=food">Food <span class="seg-count">0</span></a>`,
		// Each feed links to its page, with its section when all are shown.
		`<a href="/feeds/` + feedID(1) + `">Quanta</a><small>quanta.example</small>`,
		`<th scope="col">Section</th>`,
		`<td>Film and TV</td>`,
		`<td class="num">4.2</td>`, `3 h ago`, `<td class="num">1,234</td>`,
		// Untitled, it's its URL; titles are escaped.
		`<a href="/feeds/` + feedID(3) + `">https://film.example/feed</a>`,
		`&lt;b&gt;Nautilus&lt;/b&gt;`,
		// A gone feed says Discover hides it.
		`nautilus.example · <span class="problem problem-bad"><strong>Gone: hidden from Discover</strong></span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("discover page lacks %s", want)
		}
	}
	if n := strings.Count(body, "Gone: hidden from Discover"); n != 1 {
		t.Errorf("discover page marks %d feeds gone, want 1", n)
	}
	if strings.Index(body, "Quanta") > strings.Index(body, "film.example/feed") {
		t.Error("discover page doesn't list the sections in their order")
	}

	body = b.get(t, "/discover?section=science").Body.String()
	if !strings.Contains(body, `<a href="/discover?section=science" aria-current="page">Science`) || !strings.Contains(body, "Quanta") {
		t.Error("?section=science doesn't show Science")
	}
	if strings.Contains(body, "film.example/feed") || strings.Contains(body, `<th scope="col">Section</th>`) {
		t.Error("?section=science shows other sections' feeds or the Section column")
	}

	if body := b.get(t, "/discover?section=food").Body.String(); !strings.Contains(body, "No feeds in Food yet.") {
		t.Error("an empty section doesn't say so")
	}
	// A section that doesn't exist is All.
	if body := b.get(t, "/discover?section=bogus").Body.String(); !strings.Contains(body, `<a href="/discover" aria-current="page">All`) {
		t.Error("an unknown section isn't All")
	}

	b = seedServer(t, &fakeFeeds{catalog: []rss.AdminCatalogSection{{Section: testSections[0]}}})
	if body := b.get(t, "/discover").Body.String(); !strings.Contains(body, "Discover’s catalog is empty") {
		t.Error("an empty catalog doesn't say so")
	}
}

// TestSeedAndDiscoverPagesFail checks both pages answer 500 when their
// store fails.
func TestSeedAndDiscoverPagesFail(t *testing.T) {
	b := seedServer(t, &fakeFeeds{err: errors.New("db down")})
	for _, path := range []string{"/seed", "/discover"} {
		if rr := b.get(t, path); rr.Code != http.StatusInternalServerError {
			t.Errorf("GET %s failing = %d, want 500", path, rr.Code)
		}
	}
}
