package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeQueues is a QueueStore: Counts, JobsPerHour and Failures return its
// fields; Retry and Delete record their calls and return changed.
type fakeQueues struct {
	mu       sync.Mutex
	counts   []rss.AdminQueueCounts
	hours    []rss.JobsHour
	failures map[string][]rss.QueueFailure
	changed  int
	calls    []queueCall
}

// queueCall is one Retry or Delete the store was asked to make.
type queueCall struct {
	Action, Queue, Group string
	IDs                  []string
	By                   rss.AuditActor
}

func (f *fakeQueues) Counts(context.Context) ([]rss.AdminQueueCounts, error) { return f.counts, nil }

func (f *fakeQueues) JobsPerHour(context.Context, time.Time, time.Time) ([]rss.JobsHour, error) {
	return f.hours, nil
}

func (f *fakeQueues) Failures(_ context.Context, queue string) ([]rss.QueueFailure, error) {
	return f.failures[queue], nil
}

func (f *fakeQueues) Retry(_ context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error) {
	return f.call("retry", queue, ids, group, by)
}

func (f *fakeQueues) Delete(_ context.Context, queue string, ids []string, group string, by rss.AuditActor) (int, error) {
	return f.call("delete", queue, ids, group, by)
}

func (f *fakeQueues) call(action, queue string, ids []string, group string, by rss.AuditActor) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, queueCall{Action: action, Queue: queue, Group: group, IDs: ids, By: by})
	return f.changed, nil
}

// jobID is the UUID of the nth test job.
func jobID(n int) string { return fmt.Sprintf("40000000-0000-4000-8000-%012d", n) }

// queuesServer returns a browser logged in to a server with queues.
func queuesServer(t *testing.T, queues *fakeQueues) *browser {
	t.Helper()
	s, _ := newTestServerWithAdminConfig(t, Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"},
		Services{Database: pinger{}, Queues: queues})
	return loggedIn(t, s)
}

// testFailures are a failed lookup of each of two unreachable sites (one
// cause), one that answered 404, two failed import feeds, and an
// enrichment job whose error is markup.
func testFailures() map[string][]rss.QueueFailure {
	return map[string][]rss.QueueFailure{
		rss.QueueLookup: {
			{ID: jobID(1), Error: `discovery: fetching https://a.example/: fetch: Get "https://a.example/": dial tcp: lookup a.example: no such host`, At: testNow.Add(-time.Hour), URL: "https://a.example/"},
			{ID: jobID(2), Error: "discovery: site returned an error status: https://b.example/: status 404", At: testNow.Add(-2 * time.Hour), URL: "https://b.example/", SiteID: siteID(2)},
			{ID: jobID(3), Error: `discovery: fetching https://c.example/: fetch: Get "https://c.example/": dial tcp: lookup c.example on 127.0.0.11:53: no such host`, At: testNow.Add(-3 * 24 * time.Hour), URL: "https://c.example/"},
		},
		rss.QueueImport: {
			{ID: jobID(4) + ".0", Failure: "unreachable", Error: "get feed: status 500", At: testNow.Add(-time.Hour), URL: "https://d.example/feed", ImportedAt: testNow.Add(-2 * time.Hour)},
			{ID: jobID(4) + ".3", Failure: "not_feed", At: testNow.Add(-time.Hour), URL: "https://e.example/", ImportedAt: testNow.Add(-2 * time.Hour), FeedID: feedID(5)},
		},
		rss.QueueEnrich: {
			{ID: jobID(6), Error: `<script>alert(1)</script>`, At: testNow.Add(-time.Hour), URL: "https://f.example/post", FeedID: feedID(6)},
		},
	}
}

func TestQueuesPage(t *testing.T) {
	queues := &fakeQueues{
		counts: []rss.AdminQueueCounts{
			{Name: "lookup", Pending: 2, Running: 1, Failed: 3, Oldest: testNow.Add(-5 * time.Minute)},
			{Name: "import", Failed: 2},
			{Name: "enrich", Pending: 40, Failed: 1, Oldest: testNow.Add(-5 * time.Hour)},
			{Name: "icon", Pending: 7, Overdue: 3, Oldest: testNow.Add(-time.Hour)},
			{Name: "mail", Failed: 4},
		},
		hours:    []rss.JobsHour{{Runner: "lookup", End: testNow, Jobs: 12}},
		failures: testFailures(),
	}
	b := queuesServer(t, queues)

	rr := b.get(t, "/queues")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /queues = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/queues" aria-current="page">Queues</a>`,
		`<section class="admin-section" id="lookup" aria-labelledby="lookup-heading">`,
		`<h2 id="import-heading">Imports</h2>`,
		`<h2 id="enrich-heading">Enrichment</h2>`,
		`<h2 id="icon-heading">Icons</h2>`,
		`<h2 id="mail-heading">Email</h2>`,
		"Icon checks store no error",
		// The two unreachable sites are one group, the largest, first.
		`<code>discovery: fetching &lt;url&gt;: fetch: Get &#34;&lt;url&gt;&#34;: dial tcp: lookup &lt;host&gt;: no such host</code>`,
		`<strong>2</strong> lookups`,
		`<code>discovery: site returned an error status: &lt;url&gt;: status &lt;n&gt;</code>`,
		`<input type="hidden" name="ids" value="` + jobID(1) + ` ` + jobID(3) + `" />`,
		`<a href="/sites/` + siteID(2) + `">Site</a>`,
		// Imports: by failure, then error; no Delete.
		`<code>unreachable: get feed: status &lt;n&gt;</code>`,
		`<code>not_feed</code>`,
		`Imported 2026-11-09 · <a href="/feeds/` + feedID(5) + `">Feed</a>`,
		// Enrichment: the error escaped.
		`<code>&lt;script&gt;alert(&lt;n&gt;)&lt;/script&gt;</code>`,
		`<code>` + jobID(6) + `</code>`,
		`<form action="/queues/enrich/delete" method="post">`,
		`Type 2 to act on all 2`,
		`Type 1 to act on it`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /queues lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("GET /queues renders an error as markup")
	}
	if strings.Contains(body, "/queues/import/delete") {
		t.Error("GET /queues offers to delete import feeds")
	}
	if i, j := strings.Index(body, "dial tcp"), strings.Index(body, "status &lt;n&gt;</code>"); i > j {
		t.Error("GET /queues lists the smaller lookup group first")
	}
}

func TestQueuesPageEmpty(t *testing.T) {
	body := queuesServer(t, &fakeQueues{}).get(t, "/queues").Body.String()
	for _, want := range []string{
		`<p class="empty">No failed lookups.</p>`,
		`<p class="empty">No failed import feeds.</p>`,
		`<p class="empty">No failed enrichment jobs.</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /queues lacks %q", want)
		}
	}
}

func TestQueuesPageLimits(t *testing.T) {
	var failures []rss.QueueFailure
	// 1,001 jobs of one cause, then 21 causes of one job each.
	for i := range 1001 {
		failures = append(failures, rss.QueueFailure{ID: jobID(i), Error: "status 500", At: testNow.Add(-time.Minute)})
	}
	for i := range 21 {
		failures = append(failures, rss.QueueFailure{ID: jobID(2000 + i), Error: fmt.Sprintf("cause %c", 'a'+i), At: testNow.Add(-time.Hour)})
	}
	body := queuesServer(t, &fakeQueues{failures: map[string][]rss.QueueFailure{rss.QueueLookup: failures}}).get(t, "/queues").Body.String()

	for _, want := range []string{
		`<strong>1,001</strong> lookups`,
		`Type 1,000 to act on the newest 1,000`,
		`A group action takes at most 1,000 jobs: run it again for the rest.`,
		`<button class="btn btn-sm" type="submit">Retry 1,000</button>`,
		`And 2 smaller groups, not listed.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /queues lacks %q", want)
		}
	}
	if strings.Contains(body, jobID(1000)) {
		t.Error("GET /queues lists the 1,001st job's ID for its group's action")
	}
	if n := strings.Count(body, `class="queue-group"`); n != maxFailureGroups {
		t.Errorf("GET /queues lists %d groups, want %d", n, maxFailureGroups)
	}
}

func TestQueueActionOne(t *testing.T) {
	for _, tt := range []struct {
		path, queue, id, action, flash string
		changed                        int
	}{
		{"/queues/lookup/retry", rss.QueueLookup, jobID(1), "retry", "Retried 1 lookup: the runner takes them in the next minute or so.", 1},
		{"/queues/import/retry", rss.QueueImport, jobID(1) + ".12", "retry", "Retried 1 import feed: the runner takes them in the next minute or so.", 1},
		{"/queues/enrich/retry", rss.QueueEnrich, jobID(1), "retry", "Nothing to retry: those jobs aren’t failed any more.", 0},
		{"/queues/lookup/delete", rss.QueueLookup, jobID(1), "delete", "Deleted 1 lookup.", 1},
		{"/queues/enrich/delete", rss.QueueEnrich, jobID(1), "delete", "Nothing to delete: those jobs aren’t failed any more.", 0},
	} {
		t.Run(tt.path, func(t *testing.T) {
			queues := &fakeQueues{changed: tt.changed}
			b := queuesServer(t, queues)
			rr := b.post(t, tt.path, url.Values{"id": {tt.id}})
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/queues#"+tt.queue {
				t.Fatalf("POST %s = %d to %q, want 303 to /queues#%s", tt.path, rr.Code, rr.Header().Get("Location"), tt.queue)
			}
			if len(queues.calls) != 1 {
				t.Fatalf("calls = %+v, want one", queues.calls)
			}
			c := queues.calls[0]
			if c.Action != tt.action || c.Queue != tt.queue || c.Group != "" || len(c.IDs) != 1 || c.IDs[0] != tt.id || c.By.AdminEmail != testEmail || c.By.Via != rss.AuditViaWeb {
				t.Errorf("call = %+v, want %s of %s by %s", c, tt.action, tt.id, testEmail)
			}
			if body := b.get(t, "/queues").Body.String(); !strings.Contains(body, tt.flash) {
				t.Errorf("GET /queues after lacks the flash %q", tt.flash)
			}
		})
	}
}

func TestQueueActionGroup(t *testing.T) {
	ids := []string{jobID(1), jobID(2), jobID(3)}
	for _, tt := range []struct {
		name, path, action, typed, flash string
	}{
		{"retry", "/queues/lookup/retry", "retry", "3", "Retried 3 lookups: the runner takes them in the next minute or so."},
		{"delete, typed with spaces", "/queues/lookup/delete", "delete", " 3 ", "Deleted 3 lookups."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			queues := &fakeQueues{changed: 3}
			b := queuesServer(t, queues)
			rr := b.post(t, tt.path, url.Values{"ids": {strings.Join(ids, " ")}, "group": {"status <n>"}, "confirm": {tt.typed}})
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/queues#lookup" {
				t.Fatalf("POST %s = %d to %q, want 303 to /queues#lookup:\n%s", tt.path, rr.Code, rr.Header().Get("Location"), rr.Body)
			}
			if len(queues.calls) != 1 {
				t.Fatalf("calls = %+v, want one", queues.calls)
			}
			c := queues.calls[0]
			if c.Action != tt.action || c.Group != "status <n>" || strings.Join(c.IDs, " ") != strings.Join(ids, " ") || c.By.AdminEmail != testEmail {
				t.Errorf("call = %+v, want %s of the 3 IDs in group %q by %s", c, tt.action, "status <n>", testEmail)
			}
			if body := b.get(t, "/queues").Body.String(); !strings.Contains(body, tt.flash) {
				t.Errorf("GET /queues after lacks the flash %q", tt.flash)
			}
		})
	}

	t.Run("the count with a comma", func(t *testing.T) {
		var many []string
		for i := range 1000 {
			many = append(many, jobID(i))
		}
		queues := &fakeQueues{changed: 1000}
		b := queuesServer(t, queues)
		rr := b.post(t, "/queues/enrich/retry", url.Values{"ids": {strings.Join(many, " ")}, "group": {"g"}, "confirm": {"1,000"}})
		if rr.Code != http.StatusSeeOther || len(queues.calls) != 1 || len(queues.calls[0].IDs) != 1000 {
			t.Errorf("POST 1,000 IDs typed 1,000 = %d with calls %d, want 303 and one call of 1,000", rr.Code, len(queues.calls))
		}
	})
}

func TestQueueActionGroupWrongCount(t *testing.T) {
	failures := testFailures()
	queues := &fakeQueues{failures: failures}
	b := queuesServer(t, queues)
	key := "discovery: fetching <url>: fetch: Get \"<url>\": dial tcp: lookup <host>: no such host"
	for _, typed := range []string{"", "3", "two"} {
		rr := b.post(t, "/queues/lookup/retry", url.Values{"ids": {jobID(1) + " " + jobID(3)}, "group": {key}, "confirm": {typed}})
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("POST typed %q = %d, want 422", typed, rr.Code)
		}
		body := rr.Body.String()
		for _, want := range []string{
			`<p class="error" id="confirm-lookup-1-error">Type 2 to confirm.</p>`,
			`aria-describedby="confirm-lookup-1-error"`,
			`aria-invalid="true" autofocus`,
			`value="` + typed + `" inputmode="numeric"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("POST typed %q lacks %q", typed, want)
			}
		}
		if n := strings.Count(body, `class="error"`); n != 1 {
			t.Errorf("POST typed %q shows %d errors, want 1", typed, n)
		}
	}
	if len(queues.calls) != 0 {
		t.Errorf("calls = %+v, want none", queues.calls)
	}
}

func TestQueueActionRefused(t *testing.T) {
	var tooMany []string
	for i := range maxGroupIDs + 1 {
		tooMany = append(tooMany, jobID(i))
	}
	for _, tt := range []struct {
		name, path string
		form       url.Values
		want       int
	}{
		{"over 1,000 IDs", "/queues/lookup/retry", url.Values{"ids": {strings.Join(tooMany, " ")}, "confirm": {"1001"}}, http.StatusBadRequest},
		{"no IDs", "/queues/lookup/retry", url.Values{"confirm": {"0"}}, http.StatusBadRequest},
		{"an ID that isn't a UUID", "/queues/lookup/retry", url.Values{"id": {"1"}}, http.StatusBadRequest},
		{"an import feed ID without its position", "/queues/import/retry", url.Values{"id": {jobID(1)}}, http.StatusBadRequest},
		{"one bad ID in a group", "/queues/enrich/retry", url.Values{"ids": {jobID(1) + " x"}, "confirm": {"2"}}, http.StatusBadRequest},
		{"deleting import feeds", "/queues/import/delete", url.Values{"id": {jobID(1) + ".0"}}, http.StatusNotFound},
		{"a queue without failures", "/queues/icon/retry", url.Values{"id": {jobID(1)}}, http.StatusNotFound},
		{"an unknown queue", "/queues/refresh/retry", url.Values{"id": {jobID(1)}}, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			queues := &fakeQueues{changed: 1}
			b := queuesServer(t, queues)
			if rr := b.post(t, tt.path, tt.form); rr.Code != tt.want {
				t.Errorf("POST %s = %d, want %d", tt.path, rr.Code, tt.want)
			}
			if len(queues.calls) != 0 {
				t.Errorf("calls = %+v, want none", queues.calls)
			}
		})
	}
}

func TestGroupFailures(t *testing.T) {
	failures := []rss.QueueFailure{
		{ID: "1", Error: "status 500", At: testNow.Add(-time.Minute)},
		{ID: "2", Error: "timeout", At: testNow.Add(-2 * time.Minute)},
		{ID: "3", Error: "status 502", At: testNow.Add(-3 * time.Minute)},
		{ID: "4", Error: "timeout", At: testNow.Add(-4 * time.Minute)},
		{ID: "5", Error: "refused", At: testNow},
		{ID: "6", Error: "status 503", At: time.Time{}},
	}
	groups := groupFailures(rss.QueueLookup, failures)
	var got []string
	for _, g := range groups {
		got = append(got, fmt.Sprintf("%d %s %d [%s]", g.Index, g.Key, g.Count, g.IDs))
	}
	// Largest first, then the one that failed most recently.
	want := []string{"1 status <n> 3 [1 3 6]", "2 timeout 2 [2 4]", "3 refused 1 [5]"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("groups = %q, want %q", got, want)
	}
	if g := groups[0]; !g.Oldest.Equal(testNow.Add(-3*time.Minute)) || !g.Newest.Equal(testNow.Add(-time.Minute)) {
		t.Errorf("status group spans %v to %v, want the oldest and newest known times", g.Oldest, g.Newest)
	}

	imports := groupFailures(rss.QueueImport, []rss.QueueFailure{
		{ID: "a.0", Failure: "unreachable", Error: "status 500"},
		{ID: "a.1", Failure: "not_feed"},
		{ID: "a.2", Failure: "unreachable", Error: "status 404"},
	})
	if len(imports) != 2 || imports[0].Key != "unreachable: status <n>" || imports[1].Key != "not_feed" {
		t.Errorf("import groups = %+v, want unreachable: status <n>, then not_feed", imports)
	}

	long := groupFailures(rss.QueueEnrich, []rss.QueueFailure{{ID: "x", Error: strings.Repeat("é", 300)}})
	if n := len([]rune(long[0].Error)); n != maxErrorChars || !strings.HasSuffix(long[0].Error, "…") || long[0].Key != strings.Repeat("é", 300) {
		t.Errorf("a 300-character error shows %d characters, want %d ending in …, with the whole as its key", n, maxErrorChars)
	}
}

func TestNormalizeError(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		// discovery.Discover's, from the fetch package and net/http.
		{`discovery: fetching https://example.com/: fetch: Get "https://example.com/": dial tcp: lookup example.com: no such host`,
			`discovery: fetching <url>: fetch: Get "<url>": dial tcp: lookup <host>: no such host`},
		{`discovery: fetching https://example.org/blog: fetch: Get "https://example.org/blog": dial tcp: lookup example.org on 127.0.0.11:53: no such host`,
			`discovery: fetching <url>: fetch: Get "<url>": dial tcp: lookup <host>: no such host`},
		{`discovery: fetching https://example.net/: fetch: Get "https://example.net/": dial tcp 93.184.216.34:443: connect: connection refused`,
			`discovery: fetching <url>: fetch: Get "<url>": dial tcp <n>.<n>.<n>.<n>:<n>: connect: connection refused`},
		{`discovery: fetching https://example.net/: fetch: Get "https://example.net/": dial tcp [2606:2800:220:1:248:1893:25c8:1946]:443: connect: network is unreachable`,
			`discovery: fetching <url>: fetch: Get "<url>": dial tcp <ip>:<n>: connect: network is unreachable`},
		{`discovery: site returned an error status: https://example.com/: status 403`,
			`discovery: site returned an error status: <url>: status <n>`},
		{`discovery: fetching https://example.com/: fetch: Get "https://example.com/": tls: failed to verify certificate: x509: certificate signed by unknown authority`,
			`discovery: fetching <url>: fetch: Get "<url>": tls: failed to verify certificate: x509: certificate signed by unknown authority`},
		{`discovery: fetching https://example.com/: fetch: Get "https://example.com/": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
			`discovery: fetching <url>: fetch: Get "<url>": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`},
		{`fetch: address not allowed: 10.0.0.1`, `fetch: address not allowed: <n>.<n>.<n>.<n>`},
		{`discovery: link to the app itself: https://paperround.app/sites/x led to https://paperround.app/`,
			`discovery: link to the app itself: <url> led to <url>`},
		// The enrichment runner's.
		{`status 429`, `status <n>`},
		{`not HTML: "application/pdf"`, `not HTML: "application/pdf"`},
		// The import runner's, and the stale reset's.
		{`get feed: fetch: response too large`, `get feed: fetch: response too large`},
		{`stale: gave up after too many attempts`, `stale: gave up after too many attempts`},
		{``, ``},
	} {
		if got := normalizeError(tt.in); got != tt.want {
			t.Errorf("normalizeError(%q)\n = %q,\nwant %q", tt.in, got, tt.want)
		}
	}
}
