package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeWebPerf is a WebPerfStore returning wp, or err, and remembering the
// query it was asked.
type fakeWebPerf struct {
	wp  rss.WebPerf
	err error
	q   rss.WebPerfQuery
}

func (f *fakeWebPerf) WebPerf(_ context.Context, q rss.WebPerfQuery) (rss.WebPerf, error) {
	f.q = q
	return f.wp, f.err
}

func webPerfServer(t *testing.T, f *fakeWebPerf) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, WebPerf: f})
	return loggedIn(t, s)
}

// stat returns a RUMStat of n views with percentiles p75, p90 and p95, and
// n split 70/20/10 by rating.
func stat(n int, p75, p90, p95 float64) rss.RUMStat {
	return rss.RUMStat{Views: n, P75: p75, P90: p90, P95: p95, Good: n * 7 / 10, NeedsImprovement: n * 2 / 10, Poor: n - n*7/10 - n*2/10}
}

// vitals returns a group's LCP, INP and CLS.
func vitals(lcp, inp, cls rss.RUMStat) map[string]rss.RUMStat {
	return map[string]rss.RUMStat{"lcp": lcp, "inp": inp, "cls": cls}
}

func sampleWebPerf() rss.WebPerf {
	t0 := testNow.Add(-7 * 24 * time.Hour)
	total := vitals(stat(4800, 2460, 3750, 5310), stat(4300, 234, 439, 662), stat(4790, 0.04, 0.09, 0.14))
	total["fcp"] = stat(4810, 1480, 2270, 2840)
	total["ttfb"] = stat(4810, 522, 1220, 1800)
	return rss.WebPerf{
		Total: rss.RUMGroup{Views: 4812, Metrics: total},
		Devices: []rss.RUMGroup{
			{Key: "desktop", Views: 1688, Metrics: vitals(stat(1688, 1870, 2500, 3000), stat(1500, 117, 200, 250), stat(1688, 0.02, 0.05, 0.07))},
			{Key: "phone", Views: 2904, Metrics: vitals(stat(2904, 2780, 4000, 6000), stat(2700, 280, 500, 794), stat(2904, 0.05, 0.1, 0.17))},
		},
		Browsers: []rss.RUMGroup{
			{Key: "safari", Views: 1906, Metrics: vitals(stat(1906, 2660, 3000, 5740), stat(1900, 374, 600, 1060), stat(1906, 0.05, 0.1, 0.17))},
			{Key: "samsung", Views: 43, Metrics: map[string]rss.RUMStat{"cls": stat(43, 0.12, 0.3, 0.42)}},
		},
		Installed: []rss.RUMGroup{
			{Key: "false", Views: 3510, Metrics: vitals(stat(3510, 2630, 3000, 5680), stat(3000, 257, 400, 728), stat(3510, 0.05, 0.1, 0.17))},
			{Key: "true", Views: 1302, Metrics: vitals(stat(1302, 1970, 3000, 4250), stat(1300, 175, 300, 496), stat(1302, 0.02, 0.05, 0.07))},
		},
		Routes: []rss.RUMGroup{
			{Key: "/", Views: 1912, Metrics: vitals(stat(1912, 2530, 3000, 5470), stat(1900, 245, 400, 695), stat(1912, 0.04, 0.1, 0.14))},
			{Key: "/articles/{id}", Views: 1140, Metrics: vitals(stat(1140, 3000, 4000, 6480), stat(1100, 164, 300, 463), stat(1140, 0.09, 0.2, 0.31))},
		},
		Series: []rss.RUMPoint{
			{T: t0, Metrics: total},
			{T: t0.Add(6 * time.Hour), Metrics: total},
		},
		RouteViews: []rss.RUMRouteViews{
			{T: t0, Route: "/", Views: 900}, {T: t0.Add(6 * time.Hour), Route: "/", Views: 1012},
			{T: t0, Route: "/articles/{id}", Views: 540},
		},
		Referrers:   []rss.RUMCount{{Key: "news.example", Views: 412}, {Key: `<script>alert(1)</script>.example`, Views: 3}},
		Protocols:   []rss.RUMCount{{Key: "h2", Views: 3000}, {Key: "h3", Views: 1800}, {Key: "", Views: 12}},
		Connections: []rss.RUMCount{{Key: "4g", Views: 3600}, {Key: "", Views: 1212}},
	}
}

func TestWebPerfPage(t *testing.T) {
	f := &fakeWebPerf{wp: sampleWebPerf()}
	b := webPerfServer(t, f)

	rr := b.get(t, "/web-performance")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /web-performance = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/web-performance" aria-current="page">Web performance</a>`,
		`4,812 page views from real visitors in the last 7 days`,
		// The filters keep each other's values; the current one is marked.
		`<a href="/web-performance" aria-current="page">7d</a>`,
		`<a href="/web-performance?period=30d">30d</a>`,
		`<a href="/web-performance?device=phone">Phone</a>`,
		`<a href="/web-performance?p=p90">p90</a>`,
		// INP needs improvement at p75, so the assessment doesn't pass.
		`<strong>Not passed</strong>`, `at p75, as Google assesses it: INP needs improvement`,
		// Each metric's value at p75, rated in words, with the others.
		`<strong>2.46 s</strong>`, `<strong>234 ms</strong>`, `<strong>0.04</strong>`,
		`Largest Contentful Paint`, `Time to First Byte`,
		`Needs improvement</span>`,
		`<span class="vital-q">p95</span> <span class="rated r-poor">`,
		`70% good · 20% needs improvement · 10% poor`,
		`4,800 views reported LCP`,
		// The breakdowns at p75, with Safari's note and a browser that
		// reports no LCP or INP, with few views.
		`<th scope="col" class="num">p75 LCP</th>`,
		`<td>Safari</td>`, `Safari’s INP is known to over-report`,
		`<td>Samsung Internet <span class="tag">few views</span></td>`, `No LCP or INP reported`,
		`<td>Installed</td>`, `<td>In the browser</td>`,
		// Routes, sorted by views, most first, sortable by each column.
		`<th scope="col" class="num" aria-sort="descending"><a href="/web-performance">Views`,
		`<a href="/web-performance?sort=lcp">p75 LCP</a>`,
		`Page views per 6 hours, top routes`,
		// Sources, escaped.
		`<td>news.example</td>`, `&lt;script&gt;alert(1)&lt;/script&gt;.example`,
		`<td>h2</td><td class="num">62%</td>`, `<td>Not reported</td><td class="num">&lt;1%</td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(body, `<script>alert(1)`) {
		t.Error("a referrer host isn't escaped")
	}
	if strings.Index(body, "<td>/</td>") > strings.Index(body, "<td>/articles/{id}</td>") {
		t.Error("routes aren't sorted by views")
	}
}

func TestWebPerfPercentile(t *testing.T) {
	b := webPerfServer(t, &fakeWebPerf{wp: sampleWebPerf()})
	body := b.get(t, "/web-performance?p=p95&device=phone").Body.String()
	for _, want := range []string{
		`<a href="/web-performance?device=phone&amp;p=p95" aria-current="page">p95</a>`,
		`<a href="/web-performance?device=phone&amp;p=p95" aria-current="page">Phone</a>`,
		`<strong>5.31 s</strong>`, `<span class="vital-q">p95</span>`,
		`<th scope="col" class="num">p95 LCP</th>`,
		// The assessment stays at p75, as Google assesses it.
		`at p75, as Google assesses it: INP needs improvement`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page at p95 is missing %q", want)
		}
	}
}

func TestWebPerfPeriods(t *testing.T) {
	midnight := testNow.Truncate(24 * time.Hour)
	for _, tt := range []struct {
		path     string
		from, to time.Time
		rollups  bool
		step     time.Duration
		device   string
	}{
		{"/web-performance", testNow.Add(-7 * 24 * time.Hour), testNow, false, 6 * time.Hour, ""},
		{"/web-performance?period=24h", testNow.Add(-24 * time.Hour), testNow, false, time.Hour, ""},
		// 30 days are the most rum_views keeps; beyond, rollups' whole days.
		{"/web-performance?period=30d&device=tablet", testNow.Add(-30 * 24 * time.Hour), testNow, false, 24 * time.Hour, "tablet"},
		{"/web-performance?period=90d", midnight.AddDate(0, 0, -90), midnight, true, 24 * time.Hour, ""},
		// 13 months are 56 whole weeks, so no point is part of a week.
		{"/web-performance?period=13m", midnight.AddDate(0, 0, -56*7), midnight, true, 7 * 24 * time.Hour, ""},
		// A hand-edited value is its default.
		{"/web-performance?period=1y&device=watch&p=p99&sort=x", testNow.Add(-7 * 24 * time.Hour), testNow, false, 6 * time.Hour, ""},
	} {
		t.Run(tt.path, func(t *testing.T) {
			f := &fakeWebPerf{}
			b := webPerfServer(t, f)
			if rr := b.get(t, tt.path); rr.Code != http.StatusOK {
				t.Fatalf("GET = %d, want 200", rr.Code)
			}
			want := rss.WebPerfQuery{From: tt.from, To: tt.to, Device: tt.device, Rollups: tt.rollups, Step: tt.step}
			if !f.q.From.Equal(want.From) || !f.q.To.Equal(want.To) || f.q.Device != want.Device || f.q.Rollups != want.Rollups || f.q.Step != want.Step {
				t.Errorf("query = %+v, want %+v", f.q, want)
			}
		})
	}
}

func TestWebPerfRollups(t *testing.T) {
	wp := sampleWebPerf()
	wp.Installed, wp.Referrers, wp.Protocols, wp.Connections = nil, nil, nil, nil
	b := webPerfServer(t, &fakeWebPerf{wp: wp})
	body := b.get(t, "/web-performance?period=13m").Body.String()
	for _, want := range []string{
		`in the 13 months before today (daily rollups)`,
		`Only the last 30 days’ views say whether the app was installed`,
		`Only the last 30 days’ views say where they came from`,
		`Page views per week, top routes`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("13m page is missing %q", want)
		}
	}
}

func TestWebPerfEmpty(t *testing.T) {
	b := webPerfServer(t, &fakeWebPerf{})
	rr := b.get(t, "/web-performance?period=24h")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `No page views in the last 24 hours. Each page the reader shows sends one beacon`) {
		t.Error("the empty page doesn't say there are no views")
	}
	if strings.Contains(body, "Core Web Vitals") || strings.Contains(body, "<table") {
		t.Error("the empty page shows sections")
	}
	// The filters still show, to choose a longer period.
	if !strings.Contains(body, `<a href="/web-performance?period=30d">30d</a>`) {
		t.Error("the empty page has no period filter")
	}
}

func TestWebPerfRoutes(t *testing.T) {
	var groups []rss.RUMGroup
	for i := range 25 {
		groups = append(groups, rss.RUMGroup{
			Key:     fmt.Sprintf("/r%02d", i),
			Views:   1000 - i,
			Metrics: vitals(stat(500, float64(1000+i*100), 0, 0), stat(500, 100, 0, 0), stat(500, 0.01, 0, 0)),
		})
	}
	groups[3].Metrics = map[string]rss.RUMStat{} // reported nothing

	for _, tt := range []struct {
		name  string
		p     webPerfParams
		first string
		rows  int
		more  int
	}{
		{"by views", webPerfParams{period: defaultWebPeriod, sort: "views", percentile: "p75"}, "/r00", 20, 5},
		{"by LCP, the slowest first", webPerfParams{period: defaultWebPeriod, sort: "lcp", percentile: "p75"}, "/r24", 20, 5},
		{"by route", webPerfParams{period: defaultWebPeriod, sort: "route", percentile: "p75"}, "/r00", 20, 5},
		{"all", webPerfParams{period: defaultWebPeriod, sort: "views", percentile: "p75", allRoutes: true}, "/r00", 25, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			table := routes(groups, tt.p)
			if len(table.Rows) != tt.rows || table.More != tt.more || table.Rows[0].Label != tt.first {
				t.Errorf("%d rows from %s, %d more; want %d from %s, %d more", len(table.Rows), table.Rows[0].Label, table.More, tt.rows, tt.first, tt.more)
			}
			if tt.p.sort == "lcp" && tt.p.allRoutes == false {
				for _, r := range table.Rows {
					if r.Label == "/r03" {
						t.Error("a route with no LCP is among the slowest")
					}
				}
			}
		})
	}
	if got := routes(groups, webPerfParams{period: defaultWebPeriod, sort: "views", percentile: "p75"}).MoreHref; got != "/web-performance?routes=all" {
		t.Errorf("Show all links to %q", got)
	}
}

func TestRating(t *testing.T) {
	for _, tt := range []struct {
		metric string
		v      float64
		want   string
	}{
		{"lcp", 2500, "good"}, {"lcp", 2500.1, "needs-improvement"}, {"lcp", 4000, "needs-improvement"}, {"lcp", 4000.1, "poor"},
		{"inp", 200, "good"}, {"inp", 201, "needs-improvement"}, {"inp", 500, "needs-improvement"}, {"inp", 501, "poor"},
		{"cls", 0.1, "good"}, {"cls", 0.11, "needs-improvement"}, {"cls", 0.25, "needs-improvement"}, {"cls", 0.26, "poor"},
		{"fcp", 1800, "good"}, {"fcp", 3001, "poor"},
		{"ttfb", 800, "good"}, {"ttfb", 1801, "poor"},
		{"lcp", math.NaN(), ""},
		{"server", 10, ""},
	} {
		if got := rating(tt.metric, tt.v); got != tt.want {
			t.Errorf("rating(%s, %v) = %q, want %q", tt.metric, tt.v, got, tt.want)
		}
	}
}

func TestAssess(t *testing.T) {
	good := vitals(stat(10, 2000, 0, 0), stat(10, 150, 0, 0), stat(10, 0.05, 0, 0))
	if a := assess(good); a.Rating != "good" {
		t.Errorf("all good = %+v, want passed", a)
	}
	poor := vitals(stat(10, 4500, 0, 0), stat(10, 300, 0, 0), stat(10, 0.05, 0, 0))
	if a := assess(poor); a.Rating != "poor" || !strings.Contains(a.Text, "LCP is poor, INP needs improvement") {
		t.Errorf("LCP poor and INP needing improvement = %+v", a)
	}
	missing := map[string]rss.RUMStat{"cls": stat(10, 0.05, 0, 0)}
	if a := assess(missing); a.Rating != "" || a.Text != "No LCP or INP reported" {
		t.Errorf("no LCP or INP = %+v, want not assessed", a)
	}
}

func TestFormatMetric(t *testing.T) {
	for _, tt := range []struct {
		metric string
		v      float64
		want   string
	}{
		{"lcp", 234.4, "234 ms"}, {"lcp", 999.4, "999 ms"}, {"lcp", 999.6, "1.00 s"}, {"lcp", 2460, "2.46 s"},
		{"cls", 0.042, "0.04"}, {"cls", 0, "0.00"},
	} {
		if got := formatMetric(tt.metric, tt.v); got != tt.want {
			t.Errorf("formatMetric(%s, %v) = %q, want %q", tt.metric, tt.v, got, tt.want)
		}
	}
}

func TestWebPerfStoreError(t *testing.T) {
	b := webPerfServer(t, &fakeWebPerf{err: errors.New("db down")})
	if rr := b.get(t, "/web-performance"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET = %d, want 500", rr.Code)
	}
}
