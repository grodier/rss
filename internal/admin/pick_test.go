package admin

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// testPickSites are the picking page's sites: one whose seed entry's hint
// places its one candidate (pre-ticked), one with two feeds hinted from
// its feed in the catalog (one a silent comments feed), one with more than
// the page shows and no hint, and one whose seed hint covers two feeds
// (not pre-ticked). Titles and headlines are markup.
func testPickSites() []rss.AdminPickSite {
	food, science := testSections[1], testSections[0]
	return []rss.AdminPickSite{
		{
			Site: rss.Site{ID: siteID(1), Host: "single.example", Title: "Single"}, Source: "waitlist", Hint: food, HintSeed: true,
			Feeds: []rss.AdminPickFeed{{ID: feedID(1), Title: "<b>Single</b>", URL: "https://single.example/feed", PostsPerWeek: 2.5,
				LatestPost: testNow.Add(-3 * time.Hour), Latest: []string{"Hello <i>world</i>", ""}}},
		},
		{
			Site: rss.Site{ID: siteID(2), Host: "several.example"}, Hint: science,
			Feeds: []rss.AdminPickFeed{
				{ID: feedID(2), Title: "Posts", URL: "https://several.example/feed", LatestPost: testNow.Add(-24 * time.Hour)},
				{ID: feedID(3), URL: "https://several.example/comments/feed", Comments: true},
			},
		},
		{
			Site:  rss.Site{ID: siteID(3), Host: "many.example"},
			Feeds: []rss.AdminPickFeed{{ID: feedID(4), Title: "Many", URL: "https://many.example/feed", LatestPost: testNow.Add(-100 * 24 * time.Hour)}},
			More:  5,
		},
		{
			Site: rss.Site{ID: siteID(4), Host: "pair.example"}, Hint: food, HintSeed: true,
			Feeds: []rss.AdminPickFeed{{ID: feedID(5), Title: "One"}, {ID: feedID(6), Title: "Two"}},
		},
	}
}

var testPickCounts = map[string]int{rss.AdminPickSeed: 4, rss.AdminPickAll: 1234, rss.AdminPickSkipped: 2}

// pickSelect is the section select of the feed at index i in body.
func pickSelect(t *testing.T, body string, i int) string {
	t.Helper()
	start := strings.Index(body, fmt.Sprintf(`name="feeds[%d].section"`, i))
	if start < 0 {
		t.Fatalf("no section select for feed %d", i)
	}
	end := strings.Index(body[start:], "</select>")
	return body[start : start+end]
}

// pickTicked reports whether the feed at index i is ticked in body.
func pickTicked(body string, i int) bool {
	return strings.Contains(body, fmt.Sprintf(`name="feeds[%d].pick" value="true" checked`, i))
}

func TestPickPage(t *testing.T) {
	feeds := &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts}
	b := seedServer(t, feeds)

	rr := b.get(t, "/discover/pick")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /discover/pick = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if want := []rss.AdminPickQuery{{Filter: rss.AdminPickSeed, Limit: pickPageSize + 1}}; !slices.Equal(feeds.pickQueries, want) {
		t.Errorf("queries = %+v, want %+v", feeds.pickQueries, want)
	}
	for _, want := range []string{
		`<a href="/discover" aria-current="page">Discover</a>`,
		`<a href="/discover/pick" aria-current="page">From the seed list <span class="seg-count">4</span></a>`,
		`<a href="/discover/pick?filter=all">All sites <span class="seg-count">1,234</span></a>`,
		`<a href="/discover/pick?filter=skipped">Skipped <span class="seg-count">2</span></a>`,
		`<form action="/discover/pick" method="post" data-busy>`,
		`<input type="hidden" name="filter" value="seed" />`,
		// Each site links to its page, with its source and hint.
		`<a href="/sites/` + siteID(1) + `">single.example</a> <small>Single</small>`,
		`From waitlist · For Food`, `Its other feeds are in Science`,
		`<input type="hidden" name="sites[0].id" value="` + siteID(1) + `" />`,
		// Each feed posts its ID and site, links to its page, and says what
		// helps choose; titles and headlines are escaped.
		`<input type="hidden" name="feeds[2].id" value="` + feedID(3) + `" />`,
		`<input type="hidden" name="feeds[2].site" value="` + siteID(2) + `" />`,
		`<a href="/feeds/` + feedID(1) + `">&lt;b&gt;Single&lt;/b&gt;</a>`,
		`About 2.5 posts a week<small>Latest <time`, `3 h ago`,
		`<li>Hello &lt;i&gt;world&lt;/i&gt;</li><li>Untitled</li>`,
		// Untitled, it's its address; notes for comments and silent feeds.
		`<a href="/feeds/` + feedID(3) + `">https://several.example/comments/feed</a>`,
		`Comments feed · No posts in 90 days`,
		`No posts yet`,
		`<a href="/sites/` + siteID(3) + `">5 more feeds: pick them on the site’s page</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pick page lacks %s", want)
		}
	}
	if n := strings.Count(body, "No posts in 90 days"); n != 4 {
		t.Errorf("%d feeds noted silent, want 4 (three never posted, one 100 days ago)", n)
	}
	if strings.Contains(body, "<b>Single</b>") || strings.Contains(body, "<i>world</i>") {
		t.Error("a title or headline isn't escaped")
	}

	// Only a single candidate placed by its seed entry is ticked.
	for i, want := range []bool{true, false, false, false, false, false} {
		if got := pickTicked(body, i); got != want {
			t.Errorf("feed %d ticked = %v, want %v", i, got, want)
		}
	}
	// The hint, from the seed entry or the catalog, is pre-selected.
	for i, want := range []string{testSections[1].ID, testSections[0].ID, testSections[0].ID, "", testSections[1].ID, testSections[1].ID} {
		sel := pickSelect(t, body, i)
		if strings.Count(sel, " selected") > 1 || (want == "" && strings.Contains(sel, " selected")) ||
			(want != "" && !strings.Contains(sel, `value="`+want+`" selected`)) {
			t.Errorf("feed %d's select = %s, want %q selected", i, sel, want)
		}
	}
	// Skip the rest starts ticked on every site.
	for j := range 4 {
		if !strings.Contains(body, fmt.Sprintf(`name="sites[%d].skip" value="true" checked`, j)) {
			t.Errorf("site %d's Skip the rest isn't ticked", j)
		}
	}
}

func TestPickPageQuery(t *testing.T) {
	sites := testPickSites()
	feeds := &fakeFeeds{pick: sites, pickCounts: testPickCounts}
	b := seedServer(t, feeds)

	for _, tt := range []struct {
		path string
		want rss.AdminPickQuery
	}{
		{"/discover/pick?filter=all&after=" + siteID(1), rss.AdminPickQuery{Filter: rss.AdminPickAll, After: siteID(1), Limit: pickPageSize + 1}},
		{"/discover/pick?filter=skipped", rss.AdminPickQuery{Filter: rss.AdminPickSkipped, Limit: pickPageSize + 1}},
		// An unknown filter is the default.
		{"/discover/pick?filter=bogus", rss.AdminPickQuery{Filter: rss.AdminPickSeed, Limit: pickPageSize + 1}},
	} {
		feeds.pickQueries = nil
		if rr := b.get(t, tt.path); rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tt.path, rr.Code)
		}
		if len(feeds.pickQueries) != 1 || feeds.pickQueries[0] != tt.want {
			t.Errorf("GET %s asked %+v, want %+v", tt.path, feeds.pickQueries, tt.want)
		}
	}

	// A malformed cursor, one that isn't listed and the last are 404.
	for _, after := range []string{"nope", siteID(99), siteID(4)} {
		if rr := b.get(t, "/discover/pick?after="+after); rr.Code != http.StatusNotFound {
			t.Errorf("?after=%s = %d, want 404", after, rr.Code)
		}
	}

	// A full page links to the next, after its last site.
	var many []rss.AdminPickSite
	for i := range pickPageSize + 1 {
		many = append(many, rss.AdminPickSite{Site: rss.Site{ID: siteID(100 + i), Host: fmt.Sprintf("s%d.example", i)}})
	}
	feeds.pick = many
	body := b.get(t, "/discover/pick?filter=all").Body.String()
	if want := `href="/discover/pick?after=` + siteID(100+pickPageSize-1) + `&amp;filter=all">Next</a>`; !strings.Contains(body, want) {
		t.Errorf("a full page lacks %s", want)
	}
	if strings.Contains(body, fmt.Sprintf("s%d.example", pickPageSize)) {
		t.Error("the page lists more than a page of sites")
	}
}

func TestPickPageEmptyAndSkipped(t *testing.T) {
	b := seedServer(t, &fakeFeeds{pickCounts: testPickCounts})
	if body := html.UnescapeString(b.get(t, "/discover/pick").Body.String()); !strings.Contains(body, "Nothing to pick: every found site’s feeds are in Discover or skipped.") {
		t.Error("an empty queue doesn't say so")
	}
	if body := b.get(t, "/discover/pick?filter=skipped").Body.String(); !strings.Contains(body, "No skipped feeds.") {
		t.Error("no skips doesn't say so")
	}

	b = seedServer(t, &fakeFeeds{pick: testPickSites()[:1], pickCounts: testPickCounts})
	body := b.get(t, "/discover/pick?filter=skipped").Body.String()
	for _, want := range []string{
		`<form action="/discover/pick/unskip" method="post">`,
		`<input type="hidden" name="feed" value="` + feedID(1) + `" />`,
		`>Unskip</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skipped page lacks %s", want)
		}
	}
	if strings.Contains(body, `name="feeds[0].pick"`) || strings.Contains(body, `name="sites[0].skip"`) {
		t.Error("the skipped page offers picking")
	}
}

func TestPickPageFails(t *testing.T) {
	b := seedServer(t, &fakeFeeds{err: errors.New("db down")})
	if rr := b.get(t, "/discover/pick"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /discover/pick failing = %d, want 500", rr.Code)
	}
}

// testPickForm is a Save of testPickSites' first three sites: the first's
// feed ticked under section, the second's left with Science, under Skip
// the rest, and the third's left without it, posted from filter.
func testPickForm(filter, section string) url.Values {
	return url.Values{
		"filter":      {filter},
		"feeds[0].id": {feedID(1)}, "feeds[0].site": {siteID(1)}, "feeds[0].pick": {"true"}, "feeds[0].section": {section},
		"feeds[1].id": {feedID(2)}, "feeds[1].site": {siteID(2)}, "feeds[1].section": {testSections[0].ID},
		"feeds[2].id": {feedID(3)}, "feeds[2].site": {siteID(2)}, "feeds[2].section": {""},
		"feeds[3].id": {feedID(4)}, "feeds[3].site": {siteID(3)}, "feeds[3].section": {""},
		"sites[0].id": {siteID(1)}, "sites[0].skip": {"true"},
		"sites[1].id": {siteID(2)}, "sites[1].skip": {"true"},
		"sites[2].id": {siteID(3)},
	}
}

func TestPickSave(t *testing.T) {
	for _, tt := range []struct {
		filter, to string
		picks      []rss.DiscoverPick
		form       func(url.Values)
		flash      string
	}{
		{
			filter: rss.AdminPickAll, to: "/discover/pick?filter=all",
			picks: []rss.DiscoverPick{{FeedID: feedID(1), SectionID: testSections[1].ID}},
			flash: "Added 1 feed to Discover and skipped 2.",
		},
		{
			// Ticking a feed of a Skip the rest site keeps it from the skips.
			filter: rss.AdminPickSeed, to: "/discover/pick",
			picks: []rss.DiscoverPick{{FeedID: feedID(1), SectionID: testSections[1].ID}, {FeedID: feedID(2), SectionID: testSections[0].ID}},
			form:  func(v url.Values) { v.Set("feeds[1].pick", "true") },
			flash: "Added 2 feeds to Discover and skipped 1.",
		},
	} {
		feeds := &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts}
		b := seedServer(t, feeds)
		form := testPickForm(tt.filter, testSections[1].ID)
		if tt.form != nil {
			tt.form(form)
		}
		rr := b.post(t, "/discover/pick", form)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.to {
			t.Fatalf("POST /discover/pick = %d to %q, want 303 to %s:\n%s", rr.Code, rr.Header().Get("Location"), tt.to, rr.Body)
		}
		if !slices.Equal(feeds.picks, tt.picks) {
			t.Errorf("picks = %+v, want %+v", feeds.picks, tt.picks)
		}
		// Only the unticked feeds of Skip the rest sites, never the third
		// site's.
		wantSkips := []string{feedID(2), feedID(3)}
		if len(tt.picks) == 2 {
			wantSkips = wantSkips[1:]
		}
		if !slices.Equal(feeds.skips, wantSkips) {
			t.Errorf("skips = %v, want %v", feeds.skips, wantSkips)
		}
		if len(feeds.calls) != 1 || feeds.calls[0].By.AdminEmail != testEmail {
			t.Errorf("calls = %+v, want one save by %s", feeds.calls, testEmail)
		}
		if body := b.get(t, tt.to).Body.String(); !strings.Contains(body, tt.flash) {
			t.Errorf("the page doesn't flash %q", tt.flash)
		}
	}
}

func TestPickSaveErrors(t *testing.T) {
	// A ticked feed without a section saves nothing and keeps the choices.
	feeds := &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts}
	b := seedServer(t, feeds)
	form := testPickForm(rss.AdminPickAll, "")
	form.Del("sites[1].skip")
	rr := b.post(t, "/discover/pick", form)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a ticked feed without a section = %d, want 422", rr.Code)
	}
	if len(feeds.calls) != 0 {
		t.Errorf("saved %+v despite the error", feeds.calls)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`aria-invalid="true" aria-describedby="feed-0-error"`,
		`<p class="error" id="feed-0-error">Choose a section.</p>`,
		`<input type="hidden" name="filter" value="all" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the 422 page lacks %s", want)
		}
	}
	if !pickTicked(body, 0) || pickTicked(body, 1) {
		t.Error("the ticks posted aren't kept")
	}
	if sel := pickSelect(t, body, 1); !strings.Contains(sel, `value="`+testSections[0].ID+`" selected`) {
		t.Errorf("feed 1's posted section isn't kept: %s", sel)
	}
	if sel := pickSelect(t, body, 0); strings.Contains(sel, " selected") {
		t.Errorf("feed 0's empty section isn't kept: %s", sel)
	}
	if !strings.Contains(body, `name="sites[0].skip" value="true" checked`) || strings.Contains(body, `name="sites[1].skip" value="true" checked`) {
		t.Error("the posted Skip the rest choices aren't kept")
	}

	// The store refusing a feed names it on its row.
	for _, tt := range []struct {
		err  error
		want string
	}{
		{rss.ErrFeedGone, "This feed is gone, so it can't be in Discover."},
		{rss.ErrUnknownSection, "Choose one of the sections."},
		{rss.ErrNoRecord, "This feed no longer exists."},
	} {
		feeds := &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts, pickErr: &rss.PickError{FeedID: feedID(1), Err: tt.err}}
		b := seedServer(t, feeds)
		rr := b.post(t, "/discover/pick", testPickForm(rss.AdminPickSeed, testSections[1].ID))
		body := html.UnescapeString(rr.Body.String())
		if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `id="feed-0-error">`+tt.want+`</p>`) ||
			!strings.Contains(body, `<p class="error" role="alert">Nothing was saved: `+tt.want+`</p>`) {
			t.Errorf("%v: %d, want 422 with %q on feed 0", tt.err, rr.Code, tt.want)
		}
	}

	b = seedServer(t, &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts, err: errors.New("db down")})
	if rr := b.post(t, "/discover/pick", testPickForm(rss.AdminPickSeed, testSections[1].ID)); rr.Code != http.StatusInternalServerError {
		t.Errorf("a failing save = %d, want 500", rr.Code)
	}

	// IDs that aren't UUIDs are refused before anything is saved.
	for _, field := range []string{"feeds[1].id", "sites[0].id"} {
		feeds := &fakeFeeds{pick: testPickSites(), pickCounts: testPickCounts}
		b := seedServer(t, feeds)
		form := testPickForm(rss.AdminPickSeed, testSections[1].ID)
		form.Set(field, "nope")
		if rr := b.post(t, "/discover/pick", form); rr.Code != http.StatusBadRequest || len(feeds.calls) != 0 {
			t.Errorf("%s not a UUID = %d with %d saves, want 400 and none", field, rr.Code, len(feeds.calls))
		}
	}
}

func TestPickUnskip(t *testing.T) {
	feeds := &fakeFeeds{skipped: []string{feedID(1)}, pickCounts: testPickCounts}
	b := seedServer(t, feeds)

	rr := b.post(t, "/discover/pick/unskip", url.Values{"feed": {feedID(1)}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/discover/pick?filter=skipped" {
		t.Fatalf("POST unskip = %d to %q, want 303 to the Skipped filter", rr.Code, rr.Header().Get("Location"))
	}
	if len(feeds.calls) != 1 || feeds.calls[0].Action != "unskip" || feeds.calls[0].ID != feedID(1) || feeds.calls[0].By.AdminEmail != testEmail {
		t.Errorf("calls = %+v, want one unskip of feed 1 by %s", feeds.calls, testEmail)
	}
	if body := b.get(t, "/discover/pick?filter=skipped").Body.String(); !strings.Contains(body, "Unskipped: the feed is back in the queue.") {
		t.Error("the page doesn't flash the unskip")
	}
	for _, id := range []string{feedID(2), "nope"} {
		if rr := b.post(t, "/discover/pick/unskip", url.Values{"feed": {id}}); rr.Code != http.StatusNotFound {
			t.Errorf("unskipping %q = %d, want 404", id, rr.Code)
		}
	}
}

func TestDiscoverPageLinksPicking(t *testing.T) {
	b := seedServer(t, &fakeFeeds{catalog: testCatalog(), pickCounts: testPickCounts})
	if body := b.get(t, "/discover").Body.String(); !strings.Contains(body, `<a class="btn btn-sm btn-primary" href="/discover/pick">Pick feeds (1,234 sites)</a>`) {
		t.Error("the catalog page doesn't link the picking page with its count")
	}
}
