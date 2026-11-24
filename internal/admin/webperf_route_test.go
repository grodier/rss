package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/admin/render"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// buckets returns views spread over metric's buckets, n in each of the
// first few.
func buckets(metric string, n int) []int {
	b := make([]int, len(metrics.RUMBounds[metric])+1)
	for i := range 4 {
		b[i+2] = n
	}
	return b
}

func sampleWebPerfRoute() rss.WebPerfRoute {
	t0 := testNow.Add(-7 * 24 * time.Hour)
	total := vitals(stat(1900, 2530, 3000, 5470), stat(1800, 245, 400, 695), stat(1900, 0.04, 0.1, 0.14))
	total["fcp"] = stat(1910, 1480, 2270, 2840)
	total["ttfb"] = stat(1910, 522, 1220, 1800)
	total["server"] = rss.RUMStat{Views: 1890, P75: 310, P90: 480, P95: 700}
	bs := map[string][]int{}
	for m := range total {
		bs[m] = buckets(m, 100)
	}
	return rss.WebPerfRoute{
		Total:   rss.RUMGroup{Views: 1912, Metrics: total},
		Buckets: bs,
		Devices: []rss.RUMGroup{
			{Key: "phone", Views: 1200, Metrics: vitals(stat(1200, 2780, 4000, 6000), stat(1100, 280, 500, 794), stat(1200, 0.05, 0.1, 0.17))},
		},
		Browsers: []rss.RUMGroup{
			{Key: "safari", Views: 700, Metrics: vitals(stat(700, 2660, 3000, 5740), stat(690, 374, 600, 1060), stat(700, 0.05, 0.1, 0.17))},
		},
		Series: []rss.RUMPoint{{T: t0, Metrics: total}, {T: t0.Add(6 * time.Hour), Metrics: total}},
		LCPElements: []rss.RUMGroup{
			{Key: "img", Views: 1500, Metrics: map[string]rss.RUMStat{"lcp": stat(1500, 2900, 3400, 6000)}},
			{Key: "<script>alert(1)</script>", Views: 3, Metrics: map[string]rss.RUMStat{"lcp": stat(3, 900, 1000, 1100)}},
			{Key: "", Views: 397, Metrics: map[string]rss.RUMStat{"lcp": stat(397, 1200, 1500, 1900)}},
		},
		INPTargets: []rss.RUMGroup{
			{Key: "data-save", Views: 400, Metrics: map[string]rss.RUMStat{"inp": stat(400, 520, 700, 900)}},
			{Key: `"><img src=x>`, Views: 2, Metrics: map[string]rss.RUMStat{"inp": stat(2, 100, 110, 120)}},
		},
		NavTypes: []rss.RUMCount{{Key: "navigate", Views: 1500}, {Key: "reload", Views: 300}, {Key: "restore", Views: 112}},
		Transfer: rss.RUMTransfer{Views: 1800, Cached: 360, P50: 18 * 1024, P75: 22 * 1024, P95: 40 * 1024},
	}
}

func webPerfRouteServer(t *testing.T, f *fakeWebPerf, r RenderReader) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, WebPerf: f, Render: r})
	return loggedIn(t, s)
}

func TestWebPerfRoutePage(t *testing.T) {
	f := &fakeWebPerf{wr: sampleWebPerfRoute()}
	rr := webPerfRouteServer(t, f, nil).get(t, "/web-performance/route?route=/feeds/{id}&device=phone")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET a route = %d, want 200", rr.Code)
	}
	if f.route != "/feeds/{id}" || f.q.Device != "phone" || f.q.Rollups || !f.q.To.Equal(testNow) || !f.q.From.Equal(testNow.Add(-7*24*time.Hour)) {
		t.Errorf("asked for %s with %+v, want /feeds/{id} on phones over the last 7 days", f.route, f.q)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/web-performance" aria-current="page">Web performance</a>`,
		`<h1 class="endpoint-title"><code>/feeds<wbr>/{id}</code></h1>`,
		// The way back keeps the filters, and the endpoint is for a GET.
		`<a class="btn btn-sm" href="/web-performance?device=phone">All routes</a>`,
		`<a class="btn btn-sm" href="/endpoints/route?method=GET&amp;period=7d&amp;route=%2Ffeeds%2F%7Bid%7D">Endpoint</a>`,
		`1,912 page views from real visitors in the last 7 days.`,
		// The filters keep the route and each other.
		`<a href="/web-performance/route?device=phone&amp;period=30d&amp;route=%2Ffeeds%2F%7Bid%7D">30d</a>`,
		`<a href="/web-performance/route?route=%2Ffeeds%2F%7Bid%7D">All</a>`,
		// The vitals, as on the web performance page.
		`<strong>Not passed</strong>`, `<strong>2.53 s</strong>`, `1,900 views reported LCP`,
		// A distribution per metric, the server's time last.
		`<h3>LCP <span class="vital-full">Largest Contentful Paint</span></h3> <span class="unit">Good up to 2.50 s, poor above 4.00 s · 1,900 views</span>`,
		`<h3>CLS <span class="vital-full">Cumulative Layout Shift</span></h3> <span class="unit">Good up to 0.10, poor above 0.25 · 1,900 views</span>`,
		`<h3>Server <span class="vital-full">Server time</span></h3> <span class="unit">1,890 views</span>`,
		// Server and network.
		`<dt>p75 TTFB</dt>`, `<dd>522 ms</dd>`, `<dt>p75 server time</dt>`, `<dd>310 ms</dd>`, `<dd>212 ms</dd>`,
		`1,890 of 1,912 views reported the server’s time.`,
		`TTFB and server time, p75 over time`,
		// The breakdowns, with Safari's note.
		`<td>Phone</td>`, `<td>Safari</td>`, `Safari’s INP is known to over-report`,
		// What's slow, escaped.
		`<td><code>img</code></td><td class="num">1,500</td><td class="num">79%</td>`,
		`<td><code>&lt;script&gt;alert(1)&lt;/script&gt;</code></td>`,
		`<td>Not reported</td><td class="num">397</td>`,
		`<td><code>data-save</code></td><td class="num">400</td><td class="num">30%</td>`,
		`<td><code>&#34;&gt;&lt;img src=x&gt;</code></td>`,
		`<td>Navigation</td><td class="num">1,500</td><td class="num">78%</td>`,
		`<td>Back or Forward, restored from the cache</td>`,
		`<dt>Median size</dt>`, `<dd>18.0 KB</dd>`, `p75 22.0 KB · p95 40.0 KB`,
		`<dd>20%</dd>`, `360 of 1,800 views transferred nothing`,
		`Deploys aren’t marked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("route page is missing %q", want)
		}
	}
	if n := strings.Count(body, `class="chart chart-histogram"`); n != 6 {
		t.Errorf("%d histograms, want 6", n)
	}
	for _, bad := range []string{"<script>alert(1)", `"><img src=x>`} {
		if strings.Contains(body, bad) {
			t.Errorf("%q isn't escaped", bad)
		}
	}
	if strings.Contains(body, "few-note") {
		t.Error("1,912 views: says there are few")
	}
}

// The way the web performance page's routes link here and back keeps its
// filters.
func TestWebPerfRouteHref(t *testing.T) {
	p := webPerfParams{period: "30d", device: "", percentile: "p90", sort: "lcp"}
	if got, want := webPerfRouteHref("/feeds/{id}", p), "/web-performance/route?p=p90&period=30d&route=%2Ffeeds%2F%7Bid%7D&sort=lcp"; got != want {
		t.Errorf("webPerfRouteHref = %q, want %q", got, want)
	}
	for _, tt := range []struct{ period, want string }{
		{"24h", "/endpoints/route?method=GET&route=%2F"},
		{"7d", "/endpoints/route?method=GET&period=7d&route=%2F"},
		{"30d", "/endpoints/route?method=GET&period=30d&route=%2F"},
		{"90d", "/endpoints/route?method=GET&period=30d&route=%2F"},
		{"13m", "/endpoints/route?method=GET&period=30d&route=%2F"},
	} {
		for _, p := range webPeriods {
			if p.Key == tt.period {
				if got := endpointHref("/", p); got != tt.want {
					t.Errorf("endpointHref at %s = %q, want %q", tt.period, got, tt.want)
				}
			}
		}
	}
}

// TestWebPerfRouteUnknown: a route with no views, such as a hand-edited
// URL's, shows an empty state, not a 404 or a 500.
func TestWebPerfRouteUnknown(t *testing.T) {
	rr := webPerfRouteServer(t, &fakeWebPerf{}, nil).get(t, "/web-performance/route?route=/nowhere&period=24h")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET an unknown route = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if want := `<p class="empty">No page views of <code>/nowhere</code> in the last 24 hours. Choose a longer period to look further back.`; !strings.Contains(body, want) {
		t.Errorf("unknown route: missing %q", want)
	}
	if strings.Contains(body, `<svg class="chart-svg"`) {
		t.Error("unknown route: draws a chart")
	}
	body = webPerfRouteServer(t, &fakeWebPerf{}, nil).get(t, "/web-performance/route?route=/nowhere&period=13m").Body.String()
	if strings.Contains(body, "Choose a longer period") {
		t.Error("13m: asks for a longer period")
	}
}

// A route with no LCP reported, and few views, says so where LCP would be.
func TestWebPerfRouteWithoutLCP(t *testing.T) {
	wr := sampleWebPerfRoute()
	delete(wr.Total.Metrics, "lcp")
	delete(wr.Buckets, "lcp")
	wr.Total.Views = 42
	wr.LCPElements = nil
	body := webPerfRouteServer(t, &fakeWebPerf{wr: wr}, nil).get(t, "/web-performance/route?route=/settings").Body.String()
	for _, want := range []string{
		`<p class="vital-note">No view reported LCP.</p>`,
		`<span class="unit">No view reported LCP.</span>`,
		`<p class="missing">No view reported its LCP.</p>`,
		`<p class="few-note">Only 42 page views:`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no LCP: missing %q", want)
		}
	}
	if n := strings.Count(body, `class="chart chart-histogram"`); n != 5 {
		t.Errorf("%d histograms, want 5 without LCP's", n)
	}
}

func TestWebPerfRouteWithoutServerTime(t *testing.T) {
	wr := sampleWebPerfRoute()
	delete(wr.Total.Metrics, "server")
	body := webPerfRouteServer(t, &fakeWebPerf{wr: wr}, nil).get(t, "/web-performance/route?route=/").Body.String()
	for _, want := range []string{`<dt>p75 server time</dt>`, `<dd>—</dd>`, `No view reported the server’s time`} {
		if !strings.Contains(body, want) {
			t.Errorf("no server time: missing %q", want)
		}
	}
	if strings.Contains(body, "TTFB and server time") {
		t.Error("no server time: draws its chart")
	}
}

// Up to 30 days read the raw views; beyond, the rollups, which don't say
// what was slow.
func TestWebPerfRouteRollups(t *testing.T) {
	for _, tt := range []struct {
		period  string
		rollups bool
	}{{"24h", false}, {"30d", false}, {"90d", true}, {"13m", true}} {
		f := &fakeWebPerf{wr: sampleWebPerfRoute()}
		body := webPerfRouteServer(t, f, nil).get(t, "/web-performance/route?route=/&period="+tt.period).Body.String()
		if f.q.Rollups != tt.rollups {
			t.Errorf("%s: rollups %v, want %v", tt.period, f.q.Rollups, tt.rollups)
		}
		said := strings.Contains(body, "Only the last 30 days’ views say what their LCP element")
		if said != tt.rollups || strings.Contains(body, "<code>img</code>") == tt.rollups {
			t.Errorf("%s: says the raw views are needed %v, want %v", tt.period, said, tt.rollups)
		}
	}
}

func TestWebPerfRouteDeployMarkers(t *testing.T) {
	live := testNow.Add(-2 * time.Hour)
	deploys := []render.Deploy{{ID: "dep-1", Status: "live", Image: newDigest, CreatedAt: live.Add(-5 * time.Minute), FinishedAt: live}}
	body := webPerfRouteServer(t, &fakeWebPerf{wr: sampleWebPerfRoute()}, &fakeRender{deploys: deploys}).get(t, "/web-performance/route?route=/").Body.String()
	// The five vitals' charts and the server's.
	if n := strings.Count(body, `class="chart-marker"`); n != 6 {
		t.Errorf("%d markers, want one on each of 6 charts", n)
	}
	if !strings.Contains(body, "Dashed lines on the charts mark deploys") || strings.Contains(body, "aren’t marked") {
		t.Error("with Render: doesn't say deploys are marked")
	}
	body = webPerfRouteServer(t, &fakeWebPerf{wr: sampleWebPerfRoute()}, &fakeRender{deploysErr: errors.New("down")}).get(t, "/web-performance/route?route=/").Body.String()
	if !strings.Contains(body, "Couldn’t reach Render, so deploys aren’t marked.") {
		t.Error("Render failing: not said")
	}
}

func TestWebPerfRouteErrors(t *testing.T) {
	if rr := webPerfRouteServer(t, &fakeWebPerf{}, nil).get(t, "/web-performance/route"); rr.Code != http.StatusNotFound {
		t.Errorf("no route: %d, want 404", rr.Code)
	}
	if rr := webPerfRouteServer(t, &fakeWebPerf{err: errors.New("boom")}, nil).get(t, "/web-performance/route?route=/"); rr.Code != http.StatusInternalServerError {
		t.Errorf("the store failing: %d, want 500", rr.Code)
	}
}
