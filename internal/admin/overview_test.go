package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeOverview is an OverviewStore returning its fields, or err, and
// remembering the times it was asked for.
type fakeOverview struct {
	o        rss.AdminOverview
	queues   []rss.QueueStatus
	jobs     []rss.JobOutcomes
	recorded bool
	err      error
	now      time.Time
	since    time.Time
}

func (f *fakeOverview) Overview(_ context.Context, now time.Time) (rss.AdminOverview, error) {
	f.now = now
	return f.o, f.err
}

func (f *fakeOverview) Queues(context.Context) ([]rss.QueueStatus, error) {
	return f.queues, f.err
}

func (f *fakeOverview) Jobs(_ context.Context, since time.Time) ([]rss.JobOutcomes, bool, error) {
	f.since = since
	return f.jobs, f.recorded, f.err
}

// overviewServer returns a logged-in browser to a server whose Overview is
// ov's.
func overviewServer(t *testing.T, ov *fakeOverview) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "abc1234", BaseURL: "http://localhost:4001"}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Overview: ov})
	return loggedIn(t, s)
}

func TestOverview(t *testing.T) {
	ov := &fakeOverview{o: rss.AdminOverview{
		People: rss.OverviewPeople{
			Accounts: 1284, ConfirmedAccounts: 1201, Signups7: 12, Signups30: 57,
			Active7: 640, Active30: 902, Waiting: 3412, InvitesPending: 25,
		},
		Content: rss.OverviewContent{
			Sites: 2210, Feeds: 2604, SubscribedFeeds: 1890, Subscriptions: 15032,
			Articles: 1520400, Reads: 830120, TimelineEntries: 4200500,
		},
	}}
	b := overviewServer(t, ov)

	rr := b.get(t, "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rr.Code)
	}
	if !ov.now.Equal(testNow) {
		t.Errorf("Overview asked at %v, want the server's now, %v", ov.now, testNow)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<h2 id="people">People</h2>`,
		`<dt>Confirmed accounts</dt>`, `<dd>1,201</dd>`, `of 1,284`,
		`<dt>Signups in 7 days</dt>`, `<dd>12</dd>`, `57 in 30 days`,
		`<dt>Active in 7 days, about</dt>`, `<dd>640</dd>`, `902 in 30 days`,
		`<dt><a href="/waitlist">Waiting</a></dt>`, `<dd>3,412</dd>`,
		`<dt><a href="/invites">Invites pending</a></dt>`, `<dd>25</dd>`,
		`<h2 id="content">Content</h2>`,
		`<dt>Sites</dt>`, `<dd>2,210</dd>`,
		`<dt><a href="/feeds?filter=all">Feeds</a></dt>`, `<dd>2,604</dd>`, `1,890 with subscribers`,
		`<dt>Subscriptions</dt>`, `<dd>15,032</dd>`,
		`<dt>Articles, about</dt>`, `<dd>1,520,400</dd>`,
		`<dt>Reads, about</dt>`, `<dd>830,120</dd>`,
		`<dt>Timeline entries, about</dt>`, `<dd>4,200,500</dd>`,
		`Admin abc1234 · rendered <time datetime="` + testNow.UTC().Format("2006-01-02T15:04:05Z") + `">` + testNow.UTC().Format("2006-01-02 15:04") + ` UTC</time>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %s", want)
		}
	}
	// Only figures with a page are links.
	if n := strings.Count(body, `class="stat stat-link"`); n != 5 {
		t.Errorf("GET / has %d linked figures, want 5 (the service's two, Waiting, Invites pending and Feeds)", n)
	}
}

func TestOverviewEmpty(t *testing.T) {
	b := overviewServer(t, &fakeOverview{})

	rr := b.get(t, "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if n := strings.Count(body, `<dd>0</dd>`); n != 11 {
		t.Errorf("GET / shows %d zeros, want 11 (every figure)", n)
	}
	for _, want := range []string{"of 0", "0 in 30 days", "0 with subscribers"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %q", want)
		}
	}
}

func TestOverviewStoreError(t *testing.T) {
	b := overviewServer(t, &fakeOverview{err: errors.New("database down")})

	if rr := b.get(t, "/"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET / = %d, want 500", rr.Code)
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{
		0: "0", 7: "7", 999: "999", 1000: "1,000", 1284: "1,284", 999999: "999,999",
		1000000: "1,000,000", -1284: "-1,284",
	} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q, want %q", n, got, want)
		}
	}
}

// testQueues are the six queues at testNow: refresh 15 minutes and a second
// overdue and enrichment 5 hours (behind), email exactly 15 minutes (not),
// and the rest under that.
func testQueues() []rss.QueueStatus {
	return []rss.QueueStatus{
		{Name: "lookup", Jobs: 2, Oldest: testNow.Add(-30 * time.Second)},
		{Name: "import", Jobs: 0},
		{Name: "refresh", Jobs: 1284, Oldest: testNow.Add(-rss.QueueBehindAfter - time.Second)},
		{Name: "icon", Jobs: 7, Oldest: testNow.Add(-3 * time.Minute)},
		{Name: "enrich", Jobs: 40, Oldest: testNow.Add(-5 * time.Hour)},
		{Name: "mail", Jobs: 1, Oldest: testNow.Add(-rss.QueueBehindAfter)},
	}
}

func TestOverviewQueues(t *testing.T) {
	b := overviewServer(t, &fakeOverview{queues: testQueues()})

	rr := b.get(t, "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<h2 id="queues">Queues</h2>`,
		"<td>Lookups</td>\n                <td class=\"num\">2</td>\n                <td>under 1 min</td>",
		"<td>Imports</td>\n                <td class=\"num\">0</td>\n                <td>—</td>",
		"<td>Refresh</td>\n                <td class=\"num\">1,284</td>\n                <td>15 min</td>\n                <td><span class=\"status status-danger\">Behind</span></td>",
		"<td>Icons</td>\n                <td class=\"num\">7</td>\n                <td>3 min</td>",
		"<td>Enrichment</td>\n                <td class=\"num\">40</td>\n                <td>5 h</td>\n                <td><span class=\"status status-danger\">Behind</span></td>",
		"<td>Email</td>\n                <td class=\"num\">1</td>\n                <td>15 min</td>\n                <td><span class=\"status\">OK</span></td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %q", want)
		}
	}
	if n := strings.Count(body, "status-danger"); n != 2 {
		t.Errorf("GET / marks %d queues behind, want 2 (Refresh and Enrichment)", n)
	}
}

func TestOverviewQueuesEmpty(t *testing.T) {
	queues := testQueues()
	for i := range queues {
		queues[i].Jobs, queues[i].Oldest = 0, time.Time{}
	}
	body := overviewServer(t, &fakeOverview{queues: queues}).get(t, "/").Body.String()
	if n := strings.Count(body, "<td>—</td>"); n != 6 {
		t.Errorf("GET / shows %d queues with no wait, want 6", n)
	}
	if strings.Contains(body, "status-danger") {
		t.Error("GET / marks an empty queue behind")
	}
}

func TestWait(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Minute:     "under 1 min",
		59 * time.Second: "under 1 min",
		time.Minute:      "1 min",
		59 * time.Minute: "59 min",
		time.Hour:        "1 h",
		47 * time.Hour:   "47 h",
		48 * time.Hour:   "2 d",
		100 * time.Hour:  "4 d",
	} {
		if got := wait(d); got != want {
			t.Errorf("wait(%v) = %q, want %q", d, got, want)
		}
	}
}

// testJobs are a day's outcomes: refresh with every outcome, lookups with
// none, enrichment with retries and a few given up, email all sent, and
// icons, which the section leaves out.
func testJobs() []rss.JobOutcomes {
	return []rss.JobOutcomes{
		{Runner: "enrich", Total: 1000, Outcomes: []rss.JobOutcome{{Name: "gave_up", Jobs: 4}, {Name: "retry", Jobs: 96}, {Name: "saved", Jobs: 900}}},
		{Runner: "mail", Total: 12, Outcomes: []rss.JobOutcome{{Name: "sent", Jobs: 12}}},
		{Runner: "refresh", Total: 2000, Outcomes: []rss.JobOutcome{{Name: "failed", Jobs: 40}, {Name: "gone", Jobs: 2}, {Name: "not_modified", Jobs: 1158}, {Name: "saved", Jobs: 800}}},
		{Runner: "icon", Total: 5, Outcomes: []rss.JobOutcome{{Name: "saved", Jobs: 5}}},
	}
}

func TestOverviewJobs(t *testing.T) {
	ov := &fakeOverview{jobs: testJobs(), recorded: true}
	b := overviewServer(t, ov)

	body := b.get(t, "/").Body.String()
	if want := testNow.Add(-24 * time.Hour); !ov.since.Equal(want) {
		t.Errorf("Jobs asked since %v, want %v", ov.since, want)
	}
	for _, want := range []string{
		`<h2 id="jobs">Jobs, last 24 hours</h2>`,
		`<td>Refresh</td>`,
		`<td class="num">2,000</td>`,
		`aria-label="Refresh: saved 40%, 304 58%, failed 2%, gone &lt;1%"`,
		`<rect class="bar-failed" x="0" y="0" width="3" height="8" /><rect class="bar-done" x="3" y="0" width="96.9" height="8" /><rect class="bar-other" x="99.9" y="0" width="0.1" height="8" />`,
		`<td class="num"><span class="job-failed">2%</span></td>`,
		"<td>Lookups</td>\n                <td class=\"num\">0</td>\n                <td class=\"job-bar-col\">No jobs</td>\n                <td class=\"num\">—</td>",
		`<td class="num"><span class="job-failed">&lt;1%</span></td>`,
		`<td class="num"><span class="job-none">none</span></td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %s", want)
		}
	}
	if strings.Contains(body, "<td>Icons</td>\n                <td class=\"num\">5</td>") {
		t.Error("GET / shows the icon runner's jobs")
	}
}

func TestOverviewJobsNotRecorded(t *testing.T) {
	body := overviewServer(t, &fakeOverview{}).get(t, "/").Body.String()
	if !strings.Contains(body, `<p class="empty">No job metrics yet. <code>www</code> writes them once a minute.</p>`) {
		t.Error("GET / lacks the empty state for no job metrics")
	}
	if strings.Contains(body, `class="job-key"`) {
		t.Error("GET / shows the jobs' key with no job metrics")
	}
}

func TestJobRows(t *testing.T) {
	rows := jobRows(testJobs())
	if len(rows) != 4 {
		t.Fatalf("jobRows = %d rows, want 4 (refresh, lookups, enrichment, email)", len(rows))
	}
	for _, tt := range []struct {
		label     string
		total     int
		failed    int
		failedPct string
		breakdown string
		bar       []barSegment
	}{
		{"Refresh", 2000, 40, "2%", "saved 40%, 304 58%, failed 2%, gone <1%",
			[]barSegment{{"failed", 0, 3}, {"done", 3, 96.9}, {"other", 99.9, 0.1}}},
		{"Lookups", 0, 0, "", "", nil},
		{"Enrichment", 1000, 4, "<1%", "saved 90%, gave up <1%, retry 10%",
			[]barSegment{{"failed", 0, 3}, {"done", 3, 87.7}, {"other", 90.7, 9.3}}},
		{"Email", 12, 0, "0%", "sent 100%",
			[]barSegment{{"done", 0, 100}}},
	} {
		var row jobRow
		for _, r := range rows {
			if r.Label == tt.label {
				row = r
			}
		}
		if row.Total != tt.total || row.Failed != tt.failed || row.FailedPct != tt.failedPct {
			t.Errorf("%s: total %d, failed %d (%q); want %d, %d (%q)", tt.label, row.Total, row.Failed, row.FailedPct, tt.total, tt.failed, tt.failedPct)
		}
		if row.Breakdown != tt.breakdown {
			t.Errorf("%s: breakdown %q, want %q", tt.label, row.Breakdown, tt.breakdown)
		}
		if fmt.Sprint(row.Bar) != fmt.Sprint(tt.bar) {
			t.Errorf("%s: bar %v, want %v", tt.label, row.Bar, tt.bar)
		}
	}
}

func TestBucketBar(t *testing.T) {
	for _, tt := range []struct {
		name                string
		done, other, failed float64
		want                []barSegment
	}{
		{"failures over the minimum keep their width", 40, 55, 5,
			[]barSegment{{"failed", 0, 5}, {"done", 5, 40}, {"other", 45, 55}}},
		{"a few failures widened to the minimum, the rest shrunk", 90, 9.6, 0.4,
			[]barSegment{{"failed", 0, 3}, {"done", 3, 87.7}, {"other", 90.7, 9.3}}},
		{"no failures", 100, 0, 0,
			[]barSegment{{"done", 0, 100}}},
		{"only failures", 0, 0, 100,
			[]barSegment{{"failed", 0, 100}}},
	} {
		if got := bucketBar(tt.done, tt.other, tt.failed); fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("%s: bucketBar = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestShare(t *testing.T) {
	for _, tt := range []struct {
		part, whole int
		want        string
	}{
		{0, 0, ""}, {0, 10, "0%"}, {1, 1000, "<1%"}, {5, 1000, "1%"}, {1, 3, "33%"}, {2, 3, "67%"}, {10, 10, "100%"},
	} {
		if got := share(tt.part, tt.whole); got != tt.want {
			t.Errorf("share(%d, %d) = %q, want %q", tt.part, tt.whole, got, tt.want)
		}
	}
}

// overviewServerPinging is overviewServer with the pinger's checks in a.
func overviewServerPinging(t *testing.T, a *fakeAvailability) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "abc1234", BaseURL: "http://localhost:4001", AppInternalURL: "http://rss-test:8080"}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Overview: &fakeOverview{}, Availability: a})
	return loggedIn(t, s)
}

func TestOverviewService(t *testing.T) {
	body := overviewServerPinging(t, sampleAvailability()).get(t, "/").Body.String()
	for _, want := range []string{
		`<h2 id="service">Service</h2>`,
		`<dt><a href="/service">Uptime, 24 hours</a></dt>`, `<dd>99.86%</dd>`, `1,438 of 1,440 checks`,
		`<dt><a href="/service">www’s version</a></dt>`, `<dd>0123456</dd>`, `as of 11:59 UTC`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %s", want)
		}
	}
}

func TestOverviewServiceNotConfigured(t *testing.T) {
	body := overviewServer(t, &fakeOverview{}).get(t, "/").Body.String()
	if n := strings.Count(body, `<dd>Not configured</dd>`); n != 2 {
		t.Errorf("GET / says Not configured %d times, want 2", n)
	}
}

func TestOverviewServiceNoChecks(t *testing.T) {
	body := overviewServerPinging(t, &fakeAvailability{latestErr: rss.ErrNoRecord}).get(t, "/").Body.String()
	for _, want := range []string{`<dd>No checks</dd>`, `<dd>None reported</dd>`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %s", want)
		}
	}
}

func TestOverviewServiceError(t *testing.T) {
	for name, a := range map[string]*fakeAvailability{
		"uptime":  {err: errors.New("database down")},
		"version": {latestErr: errors.New("database down")},
	} {
		t.Run(name, func(t *testing.T) {
			if rr := overviewServerPinging(t, a).get(t, "/"); rr.Code != http.StatusInternalServerError {
				t.Errorf("GET / = %d, want 500", rr.Code)
			}
		})
	}
}
