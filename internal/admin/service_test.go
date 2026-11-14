package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/admin/render"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// fakeAvailability is an AvailabilityReader returning its fields, and
// remembering what it was asked.
type fakeAvailability struct {
	uptime    rss.Uptime
	incidents []rss.Incident
	latest    rss.AvailabilityCheck
	latestErr error    // rss.ErrNoRecord: no version reported yet
	versions  []string // VersionsIn's, by span
	err       error    // every method's

	calls int
	spans []rss.Span
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

func (f *fakeAvailability) VersionsIn(_ context.Context, spans []rss.Span) ([]string, error) {
	f.calls++
	f.spans = spans
	out := make([]string, len(spans))
	for i := range spans {
		if i < len(f.versions) {
			out[i] = f.versions[i]
		}
	}
	return out, f.err
}

// fakeRender is a RenderReader returning its fields, or its errors.
type fakeRender struct {
	instances    render.Instances
	deploys      []render.Deploy
	instancesErr error
	deploysErr   error
}

func (f *fakeRender) Instances(context.Context, time.Time, time.Time, time.Duration) (render.Instances, error) {
	return f.instances, f.instancesErr
}

func (f *fakeRender) Deploys(context.Context, int) ([]render.Deploy, error) {
	return f.deploys, f.deploysErr
}

const (
	newDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oldDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	badDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	oldSHA    = "fedcba9876543210fedcba9876543210fedcba98"
)

// sampleRender is an hour of one instance's figures and three deploys:
// the live one two hours ago, a failed one before it, and the one it
// replaced a day ago.
func sampleRender() *fakeRender {
	var cpu, mem, count []render.Point
	for i := range 6 {
		at := testNow.Add(-time.Hour + time.Duration(i)*10*time.Minute)
		cpu = append(cpu, render.Point{T: at, V: 0.02})
		mem = append(mem, render.Point{T: at, V: 120 << 20})
		count = append(count, render.Point{T: at, V: 1})
	}
	return &fakeRender{
		instances: render.Instances{CPU: cpu, Memory: mem, Count: count, CPULimit: 0.5, MemoryLimit: 512 << 20},
		deploys: []render.Deploy{
			{ID: "dep-new", Status: "live", Image: newDigest, CreatedAt: testNow.Add(-2*time.Hour - 2*time.Minute), FinishedAt: testNow.Add(-2 * time.Hour)},
			{ID: "dep-failed", Status: "update_failed", Image: badDigest, CreatedAt: testNow.Add(-3 * time.Hour), FinishedAt: testNow.Add(-3*time.Hour + time.Minute)},
			{ID: "dep-old", Status: "deactivated", Image: oldDigest, CreatedAt: testNow.Add(-26 * time.Hour), FinishedAt: testNow.Add(-26*time.Hour + 3*time.Minute)},
		},
	}
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
	return serviceServerWithRender(t, a, rt, pinging, nil)
}

// serviceServerWithRender is serviceServer reading Render's figures from
// rd, unless it's nil.
func serviceServerWithRender(t *testing.T, a *fakeAvailability, rt *fakeRuntime, pinging bool, rd *fakeRender) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}
	if pinging {
		cfg.AppInternalURL = "http://rss-test:8080"
	}
	services := Services{Database: pinger{}, Availability: a, Runtime: rt}
	if rd != nil {
		services.Render = rd
	}
	s, _ := newTestServerWithAdminConfig(t, cfg, services)
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
	for _, unwanted := range []string{"Not configured: set -app-internal-url", "No checks yet", "Showing the 50 most recent", "kept for"} {
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

	// The page asks for one more than it shows, to know if any are left out.
	incidents := func(n int) []rss.Incident {
		out := make([]rss.Incident, n)
		for i := range out {
			out[i] = rss.Incident{StartedAt: testNow.Add(-time.Duration(i+1) * time.Hour), EndedAt: testNow.Add(-time.Duration(i+1)*time.Hour + time.Minute), Checks: 1, Statuses: []string{"error"}}
		}
		return out
	}
	for _, tt := range []struct {
		n, rows int
		note    bool
	}{{maxIncidents, maxIncidents, false}, {maxIncidents + 1, maxIncidents, true}} {
		a.incidents = incidents(tt.n)
		body = serviceServer(t, a, sampleRuntime(), true).get(t, "/service").Body.String()
		if got := strings.Count(body, "<td class=\"num\">1 min</td>"); got != tt.rows {
			t.Errorf("%d incidents: %d rows, want %d", tt.n, got, tt.rows)
		}
		if got := strings.Contains(body, "Showing the 50 most recent."); got != tt.note {
			t.Errorf("%d incidents: note shown %v, want %v", tt.n, got, tt.note)
		}
	}
}

func TestServicePagePeriods(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		query       string
		current     string
		since, from time.Time // from is zero when runtime isn't read
		kept        bool
	}{
		{"", "24h", testNow.Add(-day), testNow.Add(-day), false},
		{"?period=bogus", "24h", testNow.Add(-day), testNow.Add(-day), false},
		{"?period=7d", "7d", testNow.Add(-7 * day), testNow.Add(-7 * day), false},
		{"?period=30d", "30d", testNow.Add(-30 * day), testNow.Add(-30 * day).Truncate(4 * time.Hour), false},
		// Runtime metrics are kept 30 days: 90 reads none, and says why.
		{"?period=90d", "90d", testNow.Add(-90 * day), time.Time{}, true},
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
			if !a.since.Equal(tt.since) || a.limit != maxIncidents+1 {
				t.Errorf("incidents since %v, limit %d; want %v, %d", a.since, a.limit, tt.since, maxIncidents+1)
			}
			if tt.from.IsZero() {
				if !rt.from.IsZero() {
					t.Errorf("runtime read from %v, want not read", rt.from)
				}
			} else if !rt.from.Equal(tt.from) || !rt.to.Equal(testNow) {
				t.Errorf("runtime from %v to %v, want %v to %v", rt.from, rt.to, tt.from, testNow)
			}
			kept := strings.Contains(body, "Runtime metrics are kept for 30 days: choose 24h, 7d or 30d.")
			charts := strings.Count(body, `<figure class="service-chart">`)
			if kept != tt.kept || (kept && charts != 0) || (!kept && charts != 5) {
				t.Errorf("kept line shown %v with %d charts; want shown %v, and 5 charts only without it", kept, charts, tt.kept)
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
		// and a new instance's first sample (no waits), then b's 4 more
		// since 12:00, 22 minutes back, spread over them.
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
		"Database connections/Open":   {6, gap, 1}, // 2 + 4 at 12:00
		"Database connections/In use": {3, gap, 0}, // 1 + 2 = 3 at 12:00, 3 at 12:01
		// Only the minutes that measured waits count: 12:01's 6, and 12:22's
		// 4 over 22 minutes.
		"Connection waits/Waits": {6, gap, 4.0 / 22},
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

func TestServicePageRender(t *testing.T) {
	a := sampleAvailability()
	// The live deploy's span, then the one it replaced: VersionsIn is asked
	// oldest first.
	a.versions = []string{oldSHA, testSHA}
	b := serviceServerWithRender(t, a, sampleRuntime(), true, sampleRender())
	rr := b.get(t, "/service")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /service = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	wantSpans := []rss.Span{
		{From: testNow.Add(-26*time.Hour + 3*time.Minute), To: testNow.Add(-2 * time.Hour)},
		{From: testNow.Add(-2 * time.Hour), To: testNow},
	}
	if !slices.Equal(a.spans, wantSpans) {
		t.Errorf("VersionsIn spans = %v, want %v", a.spans, wantSpans)
	}
	for _, want := range []string{
		`<h2 id="instances">Instances</h2>`,
		`<h3>CPU</h3> <span class="unit">CPU</span>`,
		`<h3>Memory</h3> <span class="unit">MB</span>`,
		`<h3>Instances</h3>`,
		// The limits are thresholds.
		`Limit at 0.5 CPU`, `Limit at 512 MB`,
		// The deploys, newest first: a version the pinger saw is linked,
		// a failed deploy shows its digest.
		`<a href="https://github.com/grodier/rss/commit/` + testSHA + `"><code>0123456</code></a></td>
                <td><span class="status">Live</span>`,
		`<code>sha256:ccccccc</code></td>
                <td><span class="status status-danger">Failed</span>`,
		`<a href="https://github.com/grodier/rss/commit/` + oldSHA + `"><code>fedcba9</code></a></td>
                <td><span class="status">Replaced</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Three Render charts and five runtime charts, each marking the deploy
	// that went live in the period, not the failed one or the older one.
	if n := strings.Count(body, `<figure class="service-chart">`); n != 8 {
		t.Errorf("%d charts, want 8", n)
	}
	if n := strings.Count(body, `<line class="chart-marker"`); n != 8 {
		t.Errorf("%d deploy markers, want 8 (one per chart)", n)
	}
	if n := strings.Count(body, "Deploy 0123456 at"); n != 8 {
		t.Errorf("charts describe the deploy %d times, want 8", n)
	}
	for _, unwanted := range []string{"Not configured", "Couldn’t reach Render", "ccccccc at", "fedcba9 at"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page has %q", unwanted)
		}
	}
}

func TestServicePageRenderWithoutPinger(t *testing.T) {
	a := sampleAvailability()
	body := serviceServerWithRender(t, a, sampleRuntime(), false, sampleRender()).get(t, "/service").Body.String()
	if a.calls != 0 {
		t.Errorf("availability asked %d times, want none", a.calls)
	}
	// Every deploy is labelled with its digest.
	for _, want := range []string{"<code>sha256:bbbbbbb</code>", "<code>sha256:ccccccc</code>", "<code>sha256:aaaaaaa</code>", "Deploy sha256:bbbbbbb at"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestServicePageRenderNotConfigured(t *testing.T) {
	body := serviceServer(t, sampleAvailability(), sampleRuntime(), true).get(t, "/service").Body.String()
	// Instances and Deploys both say so.
	if n := strings.Count(body, "Not configured: set -render-api-key (RSS_RENDER_API_KEY) and -render-service-id (RSS_RENDER_SERVICE_ID)"); n != 2 {
		t.Errorf("not-configured line %d times, want 2", n)
	}
	if strings.Contains(body, `<line class="chart-marker"`) {
		t.Error("charts mark deploys without Render")
	}
}

func TestServicePageRenderFails(t *testing.T) {
	boom := errors.New("render: status 503")
	for name, tt := range map[string]struct {
		setup             func(*fakeRender)
		failures, markers int
	}{
		"metrics": {setup: func(rd *fakeRender) { rd.instancesErr = boom }, failures: 1, markers: 5},
		"deploys": {setup: func(rd *fakeRender) { rd.deploysErr = boom }, failures: 1},
		"both":    {setup: func(rd *fakeRender) { rd.instancesErr, rd.deploysErr = boom, boom }, failures: 2},
	} {
		t.Run(name, func(t *testing.T) {
			rd := sampleRender()
			tt.setup(rd)
			rr := serviceServerWithRender(t, sampleAvailability(), sampleRuntime(), true, rd).get(t, "/service")
			if rr.Code != http.StatusOK {
				t.Fatalf("GET /service = %d, want 200", rr.Code)
			}
			body := rr.Body.String()
			if n := strings.Count(body, "Couldn’t reach Render."); n != tt.failures {
				t.Errorf("Couldn’t reach Render %d times, want %d", n, tt.failures)
			}
			if n := strings.Count(body, `<line class="chart-marker"`); n != tt.markers {
				t.Errorf("%d deploy markers, want %d", n, tt.markers)
			}
			// The rest of the page is there.
			if !strings.Contains(body, `<dd>99.86%</dd>`) || !strings.Contains(body, "www is running") {
				t.Error("page lacks the pinger's figures")
			}
		})
	}
}

func TestServicePageRenderNoFigures(t *testing.T) {
	rd := sampleRender()
	rd.instances, rd.deploys = render.Instances{}, nil
	body := serviceServerWithRender(t, sampleAvailability(), sampleRuntime(), true, rd).get(t, "/service").Body.String()
	for _, want := range []string{"No figures from Render for the last 24 hours.", "No deploys yet."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestServicePageRenderVersionsError(t *testing.T) {
	a := sampleAvailability()
	b := serviceServerWithRender(t, a, sampleRuntime(), true, sampleRender())
	a.err = errors.New("database down")
	if rr := b.get(t, "/service"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /service = %d, want 500", rr.Code)
	}
}

func TestDeployStatus(t *testing.T) {
	for status, want := range map[string]struct {
		label  string
		failed bool
	}{
		"live":               {"Live", false},
		"deactivated":        {"Replaced", false},
		"canceled":           {"Canceled", false},
		"build_failed":       {"Failed", true},
		"update_failed":      {"Failed", true},
		"pre_deploy_failed":  {"Failed", true},
		"update_in_progress": {"In progress", false},
		"queued":             {"In progress", false},
	} {
		if label, failed := deployStatus(status); label != want.label || failed != want.failed {
			t.Errorf("deployStatus(%q) = %q, %v; want %q, %v", status, label, failed, want.label, want.failed)
		}
	}
}
