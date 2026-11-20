package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// testSiteRow is site 1 in the Sites list: its title, a failed lookup whose
// error is markup, a PNG icon, two feeds and three subscribers.
func testSiteRow() rss.AdminSiteRow {
	return rss.AdminSiteRow{
		Site:  rss.Site{ID: siteID(1), Host: "example.com", URL: "https://example.com/", Title: "Example", CreatedAt: testNow.Add(-50 * time.Hour)},
		Feeds: 2, Subscribers: 1234,
		Icon: rss.SiteIcon{SiteID: siteID(1), ContentType: "image/png", Width: 64, Height: 64, Hash: "abc123"},
		Lookup: rss.Lookup{ID: "l1", SiteKey: "example.com", Status: rss.LookupFailed,
			Error: `<script>alert("lookup")</script>`, Attempts: 2, FinishedAt: testNow.Add(-time.Hour)},
	}
}

func TestSitesPageQuery(t *testing.T) {
	tests := []struct {
		query string
		want  rss.AdminSiteQuery
	}{
		{"", rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesBySubscribers}},
		{"?filter=no-icon", rss.AdminSiteQuery{Filter: rss.AdminSitesNoIcon, Sort: rss.AdminSitesBySubscribers}},
		{"?filter=lookup-failed", rss.AdminSiteQuery{Filter: rss.AdminSitesLookupFailed, Sort: rss.AdminSitesBySubscribers}},
		{"?filter=no-subscribers&sort=feeds", rss.AdminSiteQuery{Filter: rss.AdminSitesNoSubscribers, Sort: rss.AdminSitesByFeeds}},
		{"?sort=host", rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesByHost}},
		{"?sort=found", rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesByFound}},
		// What the page doesn't offer is its default.
		{"?filter=bogus&sort=title", rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesBySubscribers}},
		{"?q=+lwn.net+", rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Q: "lwn.net", Sort: rss.AdminSitesBySubscribers}},
		{"?after=" + siteID(9), rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesBySubscribers, After: siteID(9)}},
	}
	feeds := &fakeFeeds{sites: []rss.AdminSiteRow{testSiteRow()}}
	b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})
	for _, tt := range tests {
		rr := b.get(t, "/sites"+tt.query)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /sites%s = %d, want 200", tt.query, rr.Code)
			continue
		}
		tt.want.Limit = sitesPageSize + 1
		if got := feeds.siteQueries[len(feeds.siteQueries)-1]; got != tt.want {
			t.Errorf("GET /sites%s asked %+v, want %+v", tt.query, got, tt.want)
		}
	}

	if rr := b.get(t, "/sites?after=nope"); rr.Code != http.StatusNotFound {
		t.Errorf("GET /sites?after=nope = %d, want 404", rr.Code)
	}
	// A page after the last is missing.
	feeds.sites = nil
	if rr := b.get(t, "/sites?after="+siteID(9)); rr.Code != http.StatusNotFound {
		t.Errorf("GET /sites?after= past the end = %d, want 404", rr.Code)
	}

	feeds.err = errors.New("boom")
	if rr := b.get(t, "/sites"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /sites with the store failing = %d, want 500", rr.Code)
	}
}

func TestSitesPage(t *testing.T) {
	noIcon := testSiteRow()
	noIcon.ID, noIcon.Host, noIcon.Title, noIcon.Icon = siteID(2), "<b>bold.example</b>", "", rss.SiteIcon{}
	noIcon.Lookup = rss.Lookup{}
	feeds := &fakeFeeds{sites: []rss.AdminSiteRow{testSiteRow(), noIcon},
		siteCounts: map[string]int{rss.AdminSitesAll: 1234, rss.AdminSitesNoIcon: 1}}
	b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})

	rr := b.get(t, "/sites?filter=no-icon&q=example&sort=host")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/sites" aria-current="page">Sites</a>`,
		// The filters keep the search and order, with their counts (0 for
		// one the store didn't count); No icon is current.
		`<a href="/sites?q=example&amp;sort=host">All <span class="seg-count">1,234</span></a>`,
		`<a href="/sites?filter=no-icon&amp;q=example&amp;sort=host" aria-current="page">No icon <span class="seg-count">1</span></a>`,
		`<a href="/sites?filter=lookup-failed&amp;q=example&amp;sort=host">Lookup failed <span class="seg-count">0</span></a>`,
		// Sortable headers keep the filter and search; Site, by host, is
		// current and ascending.
		`<th scope="col" aria-sort="ascending"><a href="/sites?filter=no-icon&amp;q=example&amp;sort=host">Site <span aria-hidden="true">↑</span></a></th>`,
		`<a href="/sites?filter=no-icon&amp;q=example">Subscribers</a>`,
		`<a href="/sites?filter=no-icon&amp;q=example&amp;sort=feeds">Feeds</a>`,
		`<a href="/sites?filter=no-icon&amp;q=example&amp;sort=found">Found</a>`,
		`<th scope="col">Lookup</th>`, `<th scope="col">Icon</th>`,
		// The rows: the host linking to its page with the title under it;
		// a failed lookup in the danger tone, its error escaped; the icon.
		`<a href="/sites/` + siteID(1) + `">example.com</a><small>Example</small>`,
		`<span class="problem problem-bad" title="&lt;script&gt;alert(&#34;lookup&#34;)&lt;/script&gt;"><strong>Failed 1 h ago after 2 attempts.</strong>`,
		// The icon, from the admin's own route versioned by its hash,
		// beside its size; an empty tile and None without one.
		`<span class="site-icon"><img src="/sites/` + siteID(1) + `/icon?v=abc123" alt="" width="32" height="32" loading="lazy"></span>64×64 PNG</span>`,
		`<span class="site-icon"></span>None</span>`, `No lookup of this site yet.`,
		`<td class="num">2</td>`, `<td class="num">1,234</td>`,
		`title="2026-11-07 10:00 UTC">2 days ago</time>`,
		// Markup in a host is text.
		`<a href="/sites/` + siteID(2) + `">&lt;b&gt;bold.example&lt;/b&gt;</a></td>`,
		`value="example"`, `<a class="btn btn-sm" href="/sites?filter=no-icon&amp;sort=host">Clear</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /sites lacks %s", want)
		}
	}
	for _, bad := range []string{"<script>alert", "<b>bold"} {
		if strings.Contains(body, bad) {
			t.Errorf("GET /sites renders %q as markup", bad)
		}
	}
	if strings.Contains(body, ">Next<") {
		t.Error("GET /sites with one page offers Next")
	}
	// The counts are of the sites the search matches.
	if got := feeds.siteCounted[len(feeds.siteCounted)-1]; got != "example" {
		t.Errorf("counted sites matching %q, want %q", got, "example")
	}

	// A full page links to the next, after its last row.
	feeds.sites = nil
	for i := range sitesPageSize + 1 {
		row := testSiteRow()
		row.ID = siteID(100 + i)
		feeds.sites = append(feeds.sites, row)
	}
	body = b.get(t, "/sites?filter=lookup-failed").Body.String()
	if want := `<a class="btn btn-sm" href="/sites?after=` + siteID(100+sitesPageSize-1) + `&amp;filter=lookup-failed">Next</a>`; !strings.Contains(body, want) {
		t.Errorf("a full page lacks %s", want)
	}
	if strings.Contains(body, siteID(100+sitesPageSize)) {
		t.Error("a full page shows the row past it")
	}

	feeds.sites = nil
	for query, want := range map[string]string{"": "No sites.", "?q=%3Ci%3Enothing": "No sites match “&lt;i&gt;nothing”."} {
		if body := b.get(t, "/sites"+query).Body.String(); !strings.Contains(body, want) {
			t.Errorf("GET /sites%s lacks %q", query, want)
		}
	}
}

func TestIconLabel(t *testing.T) {
	tests := []struct {
		icon rss.SiteIcon
		want string
	}{
		{rss.SiteIcon{}, "None"},
		{rss.SiteIcon{SiteID: "s", ContentType: "image/png", Width: 64, Height: 64}, "64×64 PNG"},
		{rss.SiteIcon{SiteID: "s", ContentType: "image/jpeg", Width: 180, Height: 120}, "180×120 JPEG"},
		{rss.SiteIcon{SiteID: "s", ContentType: "image/gif", Width: 16, Height: 16}, "16×16 GIF"},
		{rss.SiteIcon{SiteID: "s", ContentType: "image/x-icon", Width: 32, Height: 32}, "32×32 ICO"},
	}
	for _, tt := range tests {
		if got := iconLabel(tt.icon); got != tt.want {
			t.Errorf("iconLabel(%+v) = %q, want %q", tt.icon, got, tt.want)
		}
	}
}

func TestSiteIconImage(t *testing.T) {
	icon := rss.SiteIcon{SiteID: siteID(1), ContentType: "image/png", Data: []byte("\x89PNG fake"), Hash: "abc123"}
	site := testSite()
	site.Icon.Icon = icon
	feeds := &fakeFeeds{icon: icon, site: site}
	b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})

	// Its versioned URL is cached for good, privately.
	rr := b.get(t, "/sites/"+siteID(1)+"/icon?v=abc123")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET icon = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "\x89PNG fake" {
		t.Errorf("body = %q, want the icon's data", got)
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/png",
		"Content-Length":          "9",
		"Cache-Control":           "private, max-age=31536000, immutable",
		"Content-Security-Policy": "default-src 'none'; sandbox",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := rr.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// Another version isn't kept: the icon may have changed.
	if got := b.get(t, "/sites/"+siteID(1)+"/icon?v=old").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("an old version's Cache-Control = %q, want no-store", got)
	}

	// The site's page shows it, larger.
	body := b.get(t, "/sites/"+siteID(1)).Body.String()
	if want := `<span class="site-icon site-icon-lg"><img src="/sites/` + siteID(1) + `/icon?v=abc123" alt="" width="64" height="64"></span>`; !strings.Contains(body, want) {
		t.Errorf("site page lacks %s", want)
	}

	for _, path := range []string{"/sites/nope/icon", "/sites/" + siteID(2) + "/icon"} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
	feeds.err = errors.New("boom")
	if rr := b.get(t, "/sites/"+siteID(1)+"/icon"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET icon with the store failing = %d, want 500", rr.Code)
	}
}
