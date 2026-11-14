package admin

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

// feedID and siteID are the UUIDs of the nth test feed and site.
func feedID(n int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", n) }
func siteID(n int) string { return fmt.Sprintf("30000000-0000-4000-8000-%012d", n) }

// fakeFeeds is a FeedStore: ListFeeds returns rows, keeping the query it
// was asked; Feed and Site return feed and site when their IDs match; the
// actions record their calls and return err.
type fakeFeeds struct {
	mu      sync.Mutex
	rows    []rss.AdminFeedRow
	queries []rss.AdminFeedQuery
	feed    rss.AdminFeed
	site    rss.AdminSite
	err     error
	calls   []feedCall
}

// feedCall is one action the store was asked to make.
type feedCall struct {
	Action, ID string
	By         rss.AuditActor
}

func (f *fakeFeeds) ListFeeds(_ context.Context, q rss.AdminFeedQuery) ([]rss.AdminFeedRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.rows, nil
}

func (f *fakeFeeds) Feed(_ context.Context, id string) (rss.AdminFeed, error) {
	if id != f.feed.ID {
		return rss.AdminFeed{}, rss.ErrNoRecord
	}
	return f.feed, nil
}

func (f *fakeFeeds) Site(_ context.Context, id string) (rss.AdminSite, error) {
	if id != f.site.Site.ID {
		return rss.AdminSite{}, rss.ErrNoRecord
	}
	return f.site, nil
}

func (f *fakeFeeds) call(action, id string, by rss.AuditActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, feedCall{action, id, by})
	return f.err
}

func (f *fakeFeeds) ResetBackoff(_ context.Context, id string, by rss.AuditActor) error {
	return f.call("reset", id, by)
}

func (f *fakeFeeds) ClearGone(_ context.Context, id string, by rss.AuditActor) error {
	return f.call("clear", id, by)
}

func (f *fakeFeeds) CheckIcon(_ context.Context, id string, by rss.AuditActor) error {
	return f.call("icon", id, by)
}

func (f *fakeFeeds) CheckForFeeds(_ context.Context, id string, by rss.AuditActor) (rss.Lookup, error) {
	return rss.Lookup{Status: rss.LookupPending}, f.call("lookup", id, by)
}

// fakeRefresher returns res and err, keeping the feeds it refreshed and
// whether their context had a deadline.
type fakeRefresher struct {
	res         rss.FetchResult
	err         error
	refreshed   []string
	hadDeadline bool
}

func (f *fakeRefresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	f.refreshed = append(f.refreshed, feed.ID)
	_, f.hadDeadline = ctx.Deadline()
	return f.res, f.err
}

// testFeed is feed 1 of site 1, failing with an error that's markup.
func testFeed() rss.AdminFeed {
	return rss.AdminFeed{
		AdminFeedRow: rss.AdminFeedRow{
			Feed: rss.Feed{
				ID: feedID(1), SiteID: siteID(1), Url: "https://example.com/feed.xml", Title: "Example feed",
				LastError: `<script>alert("feed")</script>`, ConsecutiveFailures: 4, PostsPerWeek: 2.25,
				LastFetched: testNow.Add(-50 * time.Hour), NextFetch: testNow.Add(time.Hour),
			},
			SiteHost: "example.com", Subscribers: 1234,
		},
		Site:     rss.Site{ID: siteID(1), Host: "example.com", URL: "https://example.com/"},
		Articles: 42, EnrichJobs: 3,
	}
}

// testSite is site 1, with testFeed and a failed lookup whose error is
// markup.
func testSite() rss.AdminSite {
	return rss.AdminSite{
		Site: rss.Site{ID: siteID(1), Host: "example.com", URL: "https://example.com/"},
		Icon: rss.SiteDebug{IconNextCheckAt: testNow.Add(24 * time.Hour)},
		Lookup: rss.Lookup{ID: "l1", SiteKey: "example.com", Status: rss.LookupFailed,
			Error: `<img src=x onerror="lookup">`, Attempts: 2, RequestedAt: testNow},
		Feeds: []rss.AdminFeedRow{testFeed().AdminFeedRow},
	}
}

// feedsServer returns a logged-in browser on a server with feeds, refresher
// and audit.
func feedsServer(t *testing.T, feeds *fakeFeeds, refresher *fakeRefresher, audit *fakeAudit) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001", AppURL: testAppURL}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Feeds: feeds, Refresher: refresher, Audit: audit})
	return loggedIn(t, s)
}

func TestFeedsPageQuery(t *testing.T) {
	tests := []struct {
		query string
		want  rss.AdminFeedQuery
	}{
		{"", rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Sort: rss.AdminFeedsBySubscribers}},
		{"?filter=gone", rss.AdminFeedQuery{Filter: rss.AdminFeedsGone, Sort: rss.AdminFeedsBySubscribers}},
		{"?filter=never-fetched", rss.AdminFeedQuery{Filter: rss.AdminFeedsNeverFetched, Sort: rss.AdminFeedsBySubscribers}},
		{"?filter=stale", rss.AdminFeedQuery{Filter: rss.AdminFeedsStale, Sort: rss.AdminFeedsBySubscribers}},
		{"?filter=no-subscribers", rss.AdminFeedQuery{Filter: rss.AdminFeedsNoSubscribers, Sort: rss.AdminFeedsBySubscribers}},
		{"?filter=all&sort=posts", rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Sort: rss.AdminFeedsByPosts}},
		{"?sort=failures", rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Sort: rss.AdminFeedsByFailures}},
		{"?sort=last-success", rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Sort: rss.AdminFeedsByLastSuccess}},
		// What the page doesn't offer is its default.
		{"?filter=bogus&sort=title", rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Sort: rss.AdminFeedsBySubscribers}},
		{"?q=+lwn.net+", rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Q: "lwn.net", Sort: rss.AdminFeedsBySubscribers}},
		{"?after=" + feedID(9), rss.AdminFeedQuery{Filter: rss.AdminFeedsFailing, Sort: rss.AdminFeedsBySubscribers, After: feedID(9)}},
	}
	feeds := &fakeFeeds{rows: []rss.AdminFeedRow{testFeed().AdminFeedRow}}
	b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})
	for _, tt := range tests {
		rr := b.get(t, "/feeds"+tt.query)
		if rr.Code != http.StatusOK {
			t.Errorf("GET /feeds%s = %d, want 200", tt.query, rr.Code)
			continue
		}
		tt.want.Limit = feedsPageSize + 1
		if got := feeds.queries[len(feeds.queries)-1]; got != tt.want {
			t.Errorf("GET /feeds%s asked %+v, want %+v", tt.query, got, tt.want)
		}
	}

	if rr := b.get(t, "/feeds?after=nope"); rr.Code != http.StatusNotFound {
		t.Errorf("GET /feeds?after=nope = %d, want 404", rr.Code)
	}
	// A page after the last is missing.
	feeds.rows = nil
	if rr := b.get(t, "/feeds?after="+feedID(9)); rr.Code != http.StatusNotFound {
		t.Errorf("GET /feeds?after= past the end = %d, want 404", rr.Code)
	}
}

func TestFeedsPage(t *testing.T) {
	gone := testFeed().AdminFeedRow
	gone.ID, gone.Title, gone.ConsecutiveFailures, gone.GoneAt = feedID(2), "Gone feed", 0, testNow.Add(-24*time.Hour)
	feeds := &fakeFeeds{rows: []rss.AdminFeedRow{testFeed().AdminFeedRow, gone}}
	b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})

	rr := b.get(t, "/feeds?filter=all&q=example&sort=failures")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /feeds = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/feeds" aria-current="page">Feeds</a>`,
		// The filters keep the search and order; All is current.
		`<a href="/feeds?filter=gone&amp;q=example&amp;sort=failures">Gone</a>`,
		`<a href="/feeds?filter=all&amp;q=example&amp;sort=failures" aria-current="page">All</a>`,
		// Sortable headers keep the filter and search; Failures is current.
		`<th scope="col" class="num" aria-sort="descending"><a href="/feeds?filter=all&amp;q=example&amp;sort=failures">Failures <span aria-hidden="true">↓</span></a></th>`,
		`<a href="/feeds?filter=all&amp;q=example&amp;sort=last-success">Last success</a>`,
		`<a href="/feeds?filter=all&amp;q=example">Subscribers</a>`,
		`<th scope="col">Last attempt</th>`,
		// The rows.
		`<a href="/feeds/` + feedID(1) + `">Example feed</a>`, `<a href="/sites/` + siteID(1) + `">example.com</a>`,
		`1,234`, `2.2`, `<span class="status status-danger">4</span>`,
		`<span class="status status-danger">2026-11-08</span>`,
		`value="example"`, `<a class="btn btn-sm" href="/feeds?filter=all&amp;sort=failures">Clear</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /feeds lacks %s", want)
		}
	}
	if strings.Contains(body, ">Next<") {
		t.Error("GET /feeds with one page offers Next")
	}

	// Last success sorts oldest first.
	body = b.get(t, "/feeds?sort=last-success").Body.String()
	if !strings.Contains(body, `aria-sort="ascending"><a href="/feeds?sort=last-success">Last success <span aria-hidden="true">↑</span>`) {
		t.Error("Last success isn't marked as sorting ascending")
	}

	// A full page links to the next, after its last row.
	feeds.rows = nil
	for i := range feedsPageSize + 1 {
		row := testFeed().AdminFeedRow
		row.ID = feedID(100 + i)
		feeds.rows = append(feeds.rows, row)
	}
	body = b.get(t, "/feeds?filter=stale").Body.String()
	if want := `<a class="btn btn-sm" href="/feeds?after=` + feedID(100+feedsPageSize-1) + `&amp;filter=stale">Next</a>`; !strings.Contains(body, want) {
		t.Errorf("a full page lacks %s", want)
	}
	if strings.Contains(body, feedID(100+feedsPageSize)) {
		t.Error("a full page shows the row past it")
	}

	feeds.rows = nil
	for query, want := range map[string]string{"": "No feeds here.", "?q=nothing": "No feeds here match “nothing”."} {
		if body := b.get(t, "/feeds"+query).Body.String(); !strings.Contains(body, want) {
			t.Errorf("GET /feeds%s lacks %q", query, want)
		}
	}
}

func TestFeedAndSitePagesEscape(t *testing.T) {
	b := feedsServer(t, &fakeFeeds{feed: testFeed(), site: testSite()}, &fakeRefresher{}, &fakeAudit{})

	rr := b.get(t, "/feeds/"+feedID(1))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /feeds/{id} = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<h1>Example feed</h1>`,
		`<code>&lt;script&gt;alert(&#34;feed&#34;)&lt;/script&gt;</code>`,
		`href="` + testAppURL + `/feeds/` + feedID(1) + `">In the reader</a>`,
		`href="/audit?target_id=` + feedID(1) + `&amp;target_type=feed">History</a>`,
		`/feeds/` + feedID(1) + `/refresh`, `/feeds/` + feedID(1) + `/reset-backoff`,
		`<td>1,234</td>`, `<td>42</td>`, `<td>2.2</td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed page lacks %s", want)
		}
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("feed page renders last_error as markup")
	}
	// Clear gone only for a gone feed.
	if strings.Contains(body, "/clear-gone") {
		t.Error("feed page offers Clear gone for a feed that isn't gone")
	}

	rr = b.get(t, "/sites/"+siteID(1))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/{id} = %d, want 200", rr.Code)
	}
	body = rr.Body.String()
	for _, want := range []string{
		`<h1>example.com</h1>`,
		`<code>&lt;img src=x onerror=&#34;lookup&#34;&gt;</code>`,
		`<span class="status status-danger">failed</span>`,
		`<a href="/feeds/` + feedID(1) + `">Example feed</a>`,
		`<td>None</td>`, `2026-11-10 12:00`,
		`/sites/` + siteID(1) + `/lookup`, `/sites/` + siteID(1) + `/check-icon`,
		`href="` + testAppURL + `/sites/` + siteID(1) + `">In the reader</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("site page lacks %s", want)
		}
	}
	if strings.Contains(body, "<img src=x") {
		t.Error("site page renders the lookup's error as markup")
	}

	for _, path := range []string{"/feeds/nope", "/feeds/" + feedID(2), "/sites/nope", "/sites/" + siteID(2)} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
}

func TestFeedAndSiteActions(t *testing.T) {
	goneFeed := testFeed()
	goneFeed.GoneAt = testNow
	tests := []struct {
		name  string
		feed  rss.AdminFeed
		path  string
		back  string
		call  string // the store's action, "" for none
		flash string
	}{
		{"reset backoff", testFeed(), "/feeds/" + feedID(1) + "/reset-backoff", "/feeds/" + feedID(1), "reset", "Reset its backoff"},
		{"clear gone", goneFeed, "/feeds/" + feedID(1) + "/clear-gone", "/feeds/" + feedID(1), "clear", "Cleared gone"},
		{"clear gone, not gone", testFeed(), "/feeds/" + feedID(1) + "/clear-gone", "/feeds/" + feedID(1), "", "This feed isn't marked gone."},
		{"check for new feeds", testFeed(), "/sites/" + siteID(1) + "/lookup", "/sites/" + siteID(1), "lookup", "Queued a lookup of example.com"},
		{"check the icon", testFeed(), "/sites/" + siteID(1) + "/check-icon", "/sites/" + siteID(1), "icon", "The icon is checked again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feeds := &fakeFeeds{feed: tt.feed, site: testSite()}
			b := feedsServer(t, feeds, &fakeRefresher{}, &fakeAudit{})

			rr := b.post(t, tt.path, nil)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.back {
				t.Fatalf("POST %s = %d to %q, want 303 to %s", tt.path, rr.Code, rr.Header().Get("Location"), tt.back)
			}
			if tt.call == "" {
				if len(feeds.calls) != 0 {
					t.Errorf("store asked %+v, want nothing", feeds.calls)
				}
			} else {
				id := feedID(1)
				if strings.HasPrefix(tt.path, "/sites/") {
					id = siteID(1)
				}
				// The store records the action with who asked.
				if len(feeds.calls) != 1 || feeds.calls[0].Action != tt.call || feeds.calls[0].ID != id ||
					feeds.calls[0].By.AdminEmail != testEmail || feeds.calls[0].By.Via != rss.AuditViaWeb {
					t.Errorf("store asked %+v, want %s of %s by the test admin from the web", feeds.calls, tt.call, id)
				}
			}
			if body := b.get(t, tt.back).Body.String(); !strings.Contains(body, html.EscapeString(tt.flash)) {
				t.Errorf("%s lacks the flash %q", tt.back, tt.flash)
			}
		})
	}

	// An action on what doesn't exist is a 404, and on a store's error a
	// 500.
	b := feedsServer(t, &fakeFeeds{feed: testFeed(), site: testSite(), err: errors.New("boom")}, &fakeRefresher{}, &fakeAudit{})
	for _, path := range []string{"/feeds/" + feedID(2) + "/reset-backoff", "/sites/" + siteID(2) + "/lookup", "/feeds/nope/clear-gone"} {
		if rr := b.post(t, path, nil); rr.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rr.Code)
		}
	}
	for _, path := range []string{"/feeds/" + feedID(1) + "/reset-backoff", "/sites/" + siteID(1) + "/check-icon"} {
		if rr := b.post(t, path, nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("POST %s with a failing store = %d, want 500", path, rr.Code)
		}
	}
}

func TestFeedRefresh(t *testing.T) {
	tests := []struct {
		name    string
		res     rss.FetchResult
		err     error
		flash   string
		outcome map[string]any
	}{
		{"saved", rss.FetchResult{New: 2, Updated: 1}, nil, "Refreshed: 2 new articles, 1 updated.",
			map[string]any{"outcome": "saved", "new": 2, "updated": 1}},
		{"not modified", rss.FetchResult{NotModified: true}, nil, "Refreshed: not modified since the last fetch.",
			map[string]any{"outcome": "not_modified"}},
		{"gone", rss.FetchResult{}, fmt.Errorf("%w: status 410", ingest.ErrGone), "The feed answered 410 Gone, so it's marked gone.",
			map[string]any{"outcome": "gone"}},
		{"failed", rss.FetchResult{}, fmt.Errorf("%w: status 500", ingest.ErrUnreachable), "The refresh failed: ingest: feed unreachable: status 500",
			map[string]any{"outcome": "failed", "error": "ingest: feed unreachable: status 500"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refresher := &fakeRefresher{res: tt.res, err: tt.err}
			audit := &fakeAudit{}
			b := feedsServer(t, &fakeFeeds{feed: testFeed()}, refresher, audit)

			rr := b.post(t, "/feeds/"+feedID(1)+"/refresh", nil)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/feeds/"+feedID(1) {
				t.Fatalf("POST refresh = %d to %q, want 303 to the feed", rr.Code, rr.Header().Get("Location"))
			}
			if len(refresher.refreshed) != 1 || refresher.refreshed[0] != feedID(1) || !refresher.hadDeadline {
				t.Errorf("refreshed %v (deadline %t), want the feed, with a deadline", refresher.refreshed, refresher.hadDeadline)
			}
			if body := b.get(t, "/feeds/"+feedID(1)).Body.String(); !strings.Contains(body, html.EscapeString(tt.flash)) {
				t.Errorf("feed page lacks the flash %q", tt.flash)
			}

			var entry *rss.AuditEvent
			for i, e := range audit.events {
				if e.Action == "feed.refresh" {
					entry = &audit.events[i]
				}
			}
			if entry == nil {
				t.Fatal("no feed.refresh in the audit log")
			}
			if entry.TargetType != "feed" || entry.TargetID != feedID(1) || entry.AdminEmail != testEmail {
				t.Errorf("audit entry = %+v, want the feed, by the test admin", entry)
			}
			if fmt.Sprint(entry.After) != fmt.Sprint(tt.outcome) {
				t.Errorf("audit entry's outcome = %v, want %v", entry.After, tt.outcome)
			}
		})
	}

	t.Run("gone feed", func(t *testing.T) {
		feed := testFeed()
		feed.GoneAt = testNow
		refresher := &fakeRefresher{}
		b := feedsServer(t, &fakeFeeds{feed: feed}, refresher, &fakeAudit{})
		if rr := b.post(t, "/feeds/"+feedID(1)+"/refresh", nil); rr.Code != http.StatusSeeOther {
			t.Fatalf("POST refresh of a gone feed = %d, want 303", rr.Code)
		}
		if len(refresher.refreshed) != 0 {
			t.Error("a gone feed was refreshed")
		}
		if body := b.get(t, "/feeds/"+feedID(1)).Body.String(); !strings.Contains(body, "This feed is marked gone: clear it first.") {
			t.Error("feed page lacks the gone flash")
		}
	})

	t.Run("store error", func(t *testing.T) {
		b := feedsServer(t, &fakeFeeds{feed: testFeed()}, &fakeRefresher{err: errors.New("database down")}, &fakeAudit{})
		if rr := b.post(t, "/feeds/"+feedID(1)+"/refresh", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("POST refresh with a failing store = %d, want 500", rr.Code)
		}
	})

	t.Run("audit fails", func(t *testing.T) {
		audit := &fakeAudit{}
		b := feedsServer(t, &fakeFeeds{feed: testFeed()}, &fakeRefresher{}, audit)
		audit.err = errors.New("audit down")
		if rr := b.post(t, "/feeds/"+feedID(1)+"/refresh", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("POST refresh that can't be recorded = %d, want 500", rr.Code)
		}
	})
}
