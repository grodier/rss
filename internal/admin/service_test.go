package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// fakeAvailability is an AvailabilityReader returning its fields, and
// remembering what it was asked.
type fakeAvailability struct {
	uptime    rss.Uptime
	incidents []rss.Incident
	latest    rss.AvailabilityCheck
	latestErr error // rss.ErrNoRecord: no version reported yet
	err       error // every method's

	calls int
	since time.Time
	limit int
}

func (f *fakeAvailability) Uptime(context.Context, time.Time) (rss.Uptime, error) {
	f.calls++
	return f.uptime, f.err
}

func (f *fakeAvailability) Incidents(_ context.Context, since time.Time, limit int) ([]rss.Incident, error) {
	f.calls++
	f.since, f.limit = since, limit
	return f.incidents, f.err
}

func (f *fakeAvailability) LatestVersion(context.Context) (rss.AvailabilityCheck, error) {
	f.calls++
	if f.err != nil {
		return rss.AvailabilityCheck{}, f.err
	}
	return f.latest, f.latestErr
}

// fakeRuntime is a RuntimeReader returning rows, or err, and remembering
// the range it was asked for.
type fakeRuntime struct {
	rows     []metrics.RuntimeRow
	err      error
	from, to time.Time
}

func (f *fakeRuntime) ListRuntime(_ context.Context, from, to time.Time) ([]metrics.RuntimeRow, error) {
	f.from, f.to = from, to
	return f.rows, f.err
}

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// sampleAvailability is a day of checks with one ended incident and one
// ongoing.
func sampleAvailability() *fakeAvailability {
	return &fakeAvailability{
		uptime: rss.Uptime{
			Day:     rss.UptimeWindow{Checks: 1440, OK: 1438},
			Week:    rss.UptimeWindow{Checks: 10080, OK: 10080},
			Month:   rss.UptimeWindow{Checks: 43200, OK: 43100},
			Quarter: rss.UptimeWindow{Checks: 43200, OK: 43100},
		},
		incidents: []rss.Incident{
			{StartedAt: testNow.Add(-3 * time.Minute), LastFailedAt: testNow.Add(-time.Minute), Checks: 2, Statuses: []string{"error", "timeout"}},
			{StartedAt: testNow.Add(-5 * time.Hour), LastFailedAt: testNow.Add(-5 * time.Hour), EndedAt: testNow.Add(-5*time.Hour + 72*time.Minute), Checks: 1, Statuses: []string{"degraded"}},
		},
		latest: rss.AvailabilityCheck{CheckedAt: testNow.Add(-time.Minute), OK: true, Status: "available", Version: testSHA},
	}
}

// sampleRuntime is an hour of one instance's samples.
func sampleRuntime() *fakeRuntime {
	var rows []metrics.RuntimeRow
	for i := range 60 {
		rows = append(rows, metrics.RuntimeRow{
			PeriodStart: testNow.Add(-time.Hour + time.Duration(i)*time.Minute), Instance: "web-1",
			HeapBytes: 40 << 20, TotalBytes: 60 << 20, Goroutines: 30, GCPauseP99Ms: 0.5, DBOpen: 3, DBInUse: 1,
		})
	}
	return &fakeRuntime{rows: rows}
}

// serviceServer is a logged-in browser on a server whose pinger runs when
// pinging is set.
func serviceServer(t *testing.T, a *fakeAvailability, rt *fakeRuntime, pinging bool) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}
	if pinging {
		cfg.AppInternalURL = "http://rss-test:8080"
	}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Availability: a, Runtime: rt})
	return loggedIn(t, s)
}

func TestServicePage(t *testing.T) {
	b := serviceServer(t, sampleAvailability(), sampleRuntime(), true)
	rr := b.get(t, "/service")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /service = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/service" aria-current="page">Service</a>`,
		`<a href="/service" aria-current="page">24h</a>`,
		`<a href="/service?period=7d">7d</a>`,
		// The four windows, rounded down, with their counts.
		`<dd>99.86%</dd>`, `<dd class="stat-note">1,438 of 1,440 checks</dd>`,
		`<dd>100%</dd>`, `<dd class="stat-note">43,100 of 43,200 checks</dd>`, `Last 90 days`,
		// The ongoing incident, then the ended one.
		`<span class="status status-danger">Ongoing</span>`, `<td class="num">3 min</td>`, `<td>error, timeout</td>`,
		`2026-11-09 08:12 UTC`, `<td class="num">1 h 12 min</td>`, `<td>degraded</td>`,
		// A commit is shortened and linked.
		`www is running <a href="https://github.com/grodier/rss/commit/` + testSHA + `"><code>0123456</code></a>, as of`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if n := strings.Count(body, `<figure class="service-chart">`); n != 5 {
		t.Errorf("%d runtime charts, want 5", n)
	}
	for _, unwanted := range []string{"Not configured", "No checks yet", "Showing the 50 most recent", "kept for"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page has %q", unwanted)
		}
	}
}

func TestServicePageNotConfigured(t *testing.T) {
	a := sampleAvailability()
	b := serviceServer(t, a, sampleRuntime(), false)
	body := b.get(t, "/service").Body.String()
	// Availability and Version both say so; the runtime charts still show.
	if n := strings.Count(body, "Not configured: set -app-internal-url (RSS_APP_INTERNAL_URL) to check www every minute."); n != 2 {
		t.Errorf("not-configured line %d times, want 2", n)
	}
	if n := strings.Count(body, `<figure class="service-chart">`); n != 5 {
		t.Errorf("%d runtime charts, want 5", n)
	}
	if a.calls != 0 {
		t.Errorf("availability asked %d times, want none", a.calls)
	}
	if strings.Contains(body, `class="stats"`) || strings.Contains(body, "www is running") {
		t.Error("page shows availability or a version without the pinger")
	}
}

func TestServicePageEmpty(t *testing.T) {
	a := &fakeAvailability{latestErr: rss.ErrNoRecord}
	b := serviceServer(t, a, &fakeRuntime{}, true)
	rr := b.get(t, "/service")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /service = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"No checks yet: the admin checks www every minute.",
		"No runtime metrics yet: www writes them once a minute.",
		"No version reported yet.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestServicePageNoIncidents(t *testing.T) {
	a := sampleAvailability()
	a.incidents = nil
	body := serviceServer(t, a, sampleRuntime(), true).get(t, "/service?period=7d").Body.String()
	if !strings.Contains(body, "No incidents in the last 7 days.") {
		t.Error("page lacks the no-incidents line")
	}

	a.incidents = make([]rss.Incident, maxIncidents)
	for i := range a.incidents {
		a.incidents[i] = rss.Incident{StartedAt: testNow.Add(-time.Duration(i+1) * time.Hour), EndedAt: testNow.Add(-time.Duration(i+1)*time.Hour + time.Minute), Checks: 1, Statuses: []string{"error"}}
	}
	body = serviceServer(t, a, sampleRuntime(), true).get(t, "/service").Body.String()
	if !strings.Contains(body, "Showing the 50 most recent.") {
		t.Error("page with 50 incidents lacks its note")
	}
}

func TestServicePagePeriods(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		query           string
		current         string
		since, from     time.Time
		note            bool
		noIncidentsText string
	}{
		{"", "24h", testNow.Add(-day), testNow.Add(-day), false, ""},
		{"?period=bogus", "24h", testNow.Add(-day), testNow.Add(-day), false, ""},
		{"?period=7d", "7d", testNow.Add(-7 * day), testNow.Add(-7 * day), false, ""},
		{"?period=30d", "30d", testNow.Add(-30 * day), testNow.Add(-30 * day).Truncate(4 * time.Hour), false, ""},
		// Runtime metrics are kept 30 days: 90 asks for those, and says so.
		{"?period=90d", "90d", testNow.Add(-90 * day), testNow.Add(-30 * day).Truncate(4 * time.Hour), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.current+tt.query, func(t *testing.T) {
			a, rt := sampleAvailability(), sampleRuntime()
			body := serviceServer(t, a, rt, true).get(t, "/service"+tt.query).Body.String()
			href := "/service"
			if tt.current != "24h" {
				href += "?period=" + tt.current
			}
			if want := `<a href="` + href + `" aria-current="page">` + tt.current + `</a>`; !strings.Contains(body, want) {
				t.Errorf("page lacks %q", want)
			}
			if !a.since.Equal(tt.since) || a.limit != maxIncidents {
				t.Errorf("incidents since %v, limit %d; want %v, %d", a.since, a.limit, tt.since, maxIncidents)
			}
			if !rt.from.Equal(tt.from) || !rt.to.Equal(testNow) {
				t.Errorf("runtime from %v to %v, want %v to %v", rt.from, rt.to, tt.from, testNow)
			}
			if got := strings.Contains(body, "Runtime metrics are kept for 30 days."); got != tt.note {
				t.Errorf("kept-for-30-days line shown: %v, want %v", got, tt.note)
			}
		})
	}
}

func TestServicePageStoreErrors(t *testing.T) {
	boom := errors.New("database down")
	for name, setup := range map[string]func(a *fakeAvailability, rt *fakeRuntime){
		"availability": func(a *fakeAvailability, _ *fakeRuntime) { a.err = boom },
		"version":      func(a *fakeAvailability, _ *fakeRuntime) { a.latestErr = boom },
		"runtime":      func(_ *fakeAvailability, rt *fakeRuntime) { rt.err = boom },
	} {
		t.Run(name, func(t *testing.T) {
			a, rt := sampleAvailability(), sampleRuntime()
			setup(a, rt)
			if rr := serviceServer(t, a, rt, true).get(t, "/service"); rr.Code != http.StatusInternalServerError {
				t.Errorf("GET /service = %d, want 500", rr.Code)
			}
		})
	}
}

func TestServicePageVersionNotACommit(t *testing.T) {
	a := sampleAvailability()
	a.latest.Version = "dev"
	body := serviceServer(t, a, sampleRuntime(), true).get(t, "/service").Body.String()
	if !strings.Contains(body, "www is running <code>dev</code>, as of") || strings.Contains(body, "github.com/grodier/rss/commit") {
		t.Error("a version that isn't a commit should show as text, not linked")
	}
}

func TestRuntimeCharts(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	m := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }
	mb := int64(1 << 20)
	rows := []metrics.RuntimeRow{
		// Step 1 (12:00–12:10): two instances in the same minute.
		{PeriodStart: m(0), Instance: "a", HeapBytes: 10 * mb, TotalBytes: 20 * mb, Goroutines: 5, GCPauseP99Ms: 1, DBOpen: 2, DBInUse: 1, DBWaitCount: 100},
		{PeriodStart: m(0), Instance: "b", HeapBytes: 30 * mb, TotalBytes: 40 * mb, Goroutines: 7, GCPauseP99Ms: 3, DBOpen: 4, DBInUse: 2, DBWaitCount: 7},
		// The highest of the step's minutes; a waits 6 in a minute.
		{PeriodStart: m(1), Instance: "a", HeapBytes: 50 * mb, TotalBytes: 60 * mb, Goroutines: 9, GCPauseP99Ms: 2, DBOpen: 3, DBInUse: 3, DBWaitCount: 106},
		// Step 2 (12:10–12:20) has no samples: a gap.
		// Step 3 (12:20–12:30): a restarted (counter down, no waits).
		{PeriodStart: m(20), Instance: "a", HeapBytes: mb, TotalBytes: mb, Goroutines: 1, DBOpen: 1, DBWaitCount: 2},
		// and a new instance's first sample (no waits), then b waits 4.
		{PeriodStart: m(21), Instance: "c", HeapBytes: mb, TotalBytes: mb, Goroutines: 1, DBOpen: 1, DBWaitCount: 50},
		{PeriodStart: m(22), Instance: "b", HeapBytes: mb, TotalBytes: mb, Goroutines: 1, DBOpen: 1, DBWaitCount: 11},
	}
	charts := runtimeCharts(rows, t0, t0.Add(30*time.Minute), 10*time.Minute)

	titles := []string{"Memory", "Goroutines", "GC pause p99", "Database connections", "Connection waits"}
	if len(charts) != len(titles) {
		t.Fatalf("%d charts, want %d", len(charts), len(titles))
	}
	for i, c := range charts {
		if c.Title != titles[i] {
			t.Errorf("chart %d = %q, want %q", i, c.Title, titles[i])
		}
	}
	// Each series' values at the three steps; NaN is a gap.
	gap := math.NaN()
	want := map[string][]float64{
		"Memory/Heap":                 {50, gap, 1},
		"Memory/Total":                {60, gap, 1},
		"Goroutines/Goroutines":       {9, gap, 1},
		"GC pause p99/p99":            {3, gap, 0},
		"Database connections/Open":   {6, gap, 1},       // 2 + 4 at 12:00
		"Database connections/In use": {3, gap, 0},       // 1 + 2 = 3 at 12:00, 3 at 12:01
		"Connection waits/Waits":      {3, gap, 4.0 / 3}, // 6 over 2 minutes; 4 over 3
	}
	for _, c := range charts {
		for _, s := range c.Series {
			key := c.Title + "/" + s.Name
			w, ok := want[key]
			if !ok {
				t.Errorf("unexpected series %s", key)
				continue
			}
			if !samePoints(s.Points, w, t0, 10*time.Minute) {
				t.Errorf("%s = %v, want %v", key, s.Points, w)
			}
		}
	}
}

// samePoints is whether points are want's values at from, from+step, …,
// with NaN for a gap.
func samePoints(points []chart.Point, want []float64, from time.Time, step time.Duration) bool {
	if len(points) != len(want) {
		return false
	}
	for i, p := range points {
		if !p.T.Equal(from.Add(time.Duration(i) * step)) {
			return false
		}
		if math.IsNaN(want[i]) != math.IsNaN(p.V) || (!math.IsNaN(p.V) && math.Abs(p.V-want[i]) > 1e-9) {
			return false
		}
	}
	return true
}

func TestUptimePercent(t *testing.T) {
	for _, tt := range []struct {
		w    rss.UptimeWindow
		want string
	}{
		{rss.UptimeWindow{}, "No checks"},
		{rss.UptimeWindow{Checks: 1440, OK: 1440}, "100%"},
		{rss.UptimeWindow{Checks: 1440, OK: 1438}, "99.86%"},
		// Rounded down: one failed check in 43,200 isn't 100%.
		{rss.UptimeWindow{Checks: 43200, OK: 43199}, "99.99%"},
		{rss.UptimeWindow{Checks: 10, OK: 0}, "0.00%"},
	} {
		if got := uptimePercent(tt.w); got != tt.want {
			t.Errorf("uptimePercent(%+v) = %q, want %q", tt.w, got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "under 1 min"},
		{3*time.Minute + 59*time.Second, "3 min"},
		{time.Hour, "1 h"},
		{72 * time.Minute, "1 h 12 min"},
		{48 * time.Hour, "2 d"},
		{52*time.Hour + 30*time.Minute, "2 d 4 h"},
	} {
		if got := duration(tt.d); got != tt.want {
			t.Errorf("duration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
