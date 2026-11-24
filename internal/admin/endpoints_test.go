package admin

import (
	"context"
	"errors"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/admin/render"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// fakeEndpoints is an EndpointsStore returning its fields, or err, and
// remembering what it was asked.
type fakeEndpoints struct {
	ep     rss.Endpoints
	errs   []rss.ServerError
	err    error
	q      rss.EndpointQuery
	asked  [2]string // RouteErrors' route and method
	limit  int
	errErr error
}

func (f *fakeEndpoints) Endpoints(_ context.Context, q rss.EndpointQuery) (rss.Endpoints, error) {
	f.q = q
	return f.ep, f.err
}

func (f *fakeEndpoints) RouteErrors(_ context.Context, route, method string, limit int) ([]rss.ServerError, error) {
	f.asked, f.limit = [2]string{route, method}, limit
	return f.errs, f.errErr
}

func endpointsServer(t *testing.T, f *fakeEndpoints, r RenderReader) *browser {
	t.Helper()
	return endpointsServerWith(t, f, r, &fakeWebPerf{})
}

// endpointsServerWith is endpointsServer with wp for the real-user views.
func endpointsServerWith(t *testing.T, f *fakeEndpoints, r RenderReader, wp *fakeWebPerf) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Endpoints: f, Render: r, WebPerf: wp})
	return loggedIn(t, s)
}

// requests returns stats of n requests all in latency bucket i, with
// c4 4xx and c5 5xx among them, of size bytes each.
func requests(n, i, c4, c5 int, bytes int64) rss.RequestStats {
	b := make([]int, len(metrics.DurationBounds)+1)
	b[i] = n
	return rss.RequestStats{
		Requests: n, Classes: [5]int{0, n - c4 - c5, 0, c4, c5},
		BytesSum: bytes * int64(n), Buckets: b,
	}
}

// sampleEndpoints are three routes whose percentiles are known: GET /
// has 100 requests between 0 and 5 ms (p50 2.5, p95 4.75), GET /feeds/{id}
// 10 between 100 and 250 ms (p50 175), and POST /login 4 between 250 and
// 500 ms (p50 375).
func sampleEndpoints() rss.Endpoints {
	root := requests(100, 0, 2, 0, 2048)
	feed := requests(10, 5, 1, 1, 1024)
	login := requests(4, 6, 0, 0, 0)
	total := requests(114, 0, 3, 1, 0)
	total.Buckets[0], total.Buckets[5], total.Buckets[6] = 100, 10, 4
	return rss.Endpoints{
		Total: total,
		Series: []rss.EndpointPoint{
			{T: testNow.Add(-24 * time.Hour).Truncate(time.Minute), RequestStats: requests(30, 0, 0, 0, 0)},
		},
		Routes: []rss.EndpointRoute{
			{Route: "/", Method: "GET", RequestStats: root},
			{Route: "/feeds/{id}", Method: "GET", RequestStats: feed},
			{Route: "/login", Method: "POST", RequestStats: login},
		},
	}
}

// routeOrder returns the routes table's rows' routes, in order.
var routeCell = regexp.MustCompile(`<th scope="row"><a href="[^"]*"><span class="method">(\w+)</span> <code>(.*?)</code>`)

func routeOrder(body string) []string {
	var out []string
	for _, m := range routeCell.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1]+" "+strings.ReplaceAll(m[2], "<wbr>", ""))
	}
	return out
}

func TestEndpointsSortsRoutes(t *testing.T) {
	f := &fakeEndpoints{ep: sampleEndpoints()}
	b := endpointsServer(t, f, nil)
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"", []string{"GET /", "GET /feeds/{id}", "POST /login"}},
		{"?sort=p95", []string{"POST /login", "GET /feeds/{id}", "GET /"}},
		{"?sort=route", []string{"GET /", "GET /feeds/{id}", "POST /login"}},
		{"?sort=5xx", []string{"GET /feeds/{id}", "GET /", "POST /login"}},
		{"?sort=4xx", []string{"GET /feeds/{id}", "GET /", "POST /login"}},
		{"?sort=size", []string{"GET /", "GET /feeds/{id}", "POST /login"}},
		// A sort the page doesn't offer is the default.
		{"?sort=nope", []string{"GET /", "GET /feeds/{id}", "POST /login"}},
	} {
		rr := b.get(t, "/endpoints"+tt.query)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /endpoints%s = %d", tt.query, rr.Code)
		}
		body := rr.Body.String()
		if got := routeOrder(body); !slices.Equal(got, tt.want) {
			t.Errorf("GET /endpoints%s: routes %v, want %v", tt.query, got, tt.want)
		}
	}

	body := b.get(t, "/endpoints?sort=p95").Body.String()
	for _, want := range []string{
		`aria-sort="descending"><a href="/endpoints?sort=p95">p95 ms <span aria-hidden="true">↓</span>`,
		`<a href="/endpoints?sort=route">Route</a>`,
		// The period keeps the sort.
		`href="/endpoints?period=7d&amp;sort=p95"`,
		// GET /: 2.5 ms at p50 and 4.75 at p95, 2% 4xx, 2 KB.
		`<td class="num">100</td>
                <td class="num">2.0%</td>
                <td class="num">0%</td>
                <td class="num">2.5</td>
                <td class="num">4.8</td>`,
		`<td class="num">2.0</td>`,
		// GET /feeds/{id}: 10% 4xx and 5xx, 175 ms at p50.
		`<td class="num">10%</td>
                <td class="num">10%</td>
                <td class="num">175</td>`,
		// A route's link keeps the sort for its way back.
		`href="/endpoints/route?method=GET&amp;route=%2Ffeeds%2F%7Bid%7D&amp;sort=p95"`,
		`114 requests to www in the last 24 hours`,
		// Each chart's figure for the period, labelled.
		`<p class="endpoint-figure"><span>Total</span> 114</p>`,
		`<p class="endpoint-figure"><span>Overall</span> 0.9%</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /endpoints?sort=p95: missing %q", want)
		}
	}
}

func TestEndpointsQuery(t *testing.T) {
	f := &fakeEndpoints{ep: sampleEndpoints()}
	b := endpointsServer(t, f, nil)
	for _, tt := range []struct {
		period   string
		from, to time.Time
		hourly   bool
		step     time.Duration
	}{
		{"", testNow.Add(-24 * time.Hour), testNow, false, 10 * time.Minute},
		{"1h", testNow.Add(-time.Hour), testNow, false, time.Minute},
		{"7d", testNow.Add(time.Hour - 7*24*time.Hour), testNow.Add(time.Hour), true, time.Hour},
		{"30d", testNow.Add(time.Hour - 30*24*time.Hour), testNow.Add(time.Hour), true, 4 * time.Hour},
		{"13m", testNow.Add(-24 * time.Hour), testNow, false, 10 * time.Minute},
	} {
		if rr := b.get(t, "/endpoints?period="+tt.period); rr.Code != http.StatusOK {
			t.Fatalf("period %q: %d", tt.period, rr.Code)
		}
		want := rss.EndpointQuery{From: tt.from, To: tt.to, Hourly: tt.hourly, Step: tt.step}
		if f.q != want {
			t.Errorf("period %q: query %+v, want %+v", tt.period, f.q, want)
		}
	}
}

func TestEndpointsEmpty(t *testing.T) {
	b := endpointsServer(t, &fakeEndpoints{}, nil)
	rr := b.get(t, "/endpoints?period=1h")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /endpoints = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `<p class="empty">No requests in the last hour.`) {
		t.Errorf("no empty state:\n%s", body)
	}
	if strings.Contains(body, "<table") || strings.Contains(body, `class="chart-svg"`) {
		t.Error("an empty period shows a table or chart")
	}
}

func TestEndpointsStoreError(t *testing.T) {
	b := endpointsServer(t, &fakeEndpoints{err: errors.New("boom")}, nil)
	if rr := b.get(t, "/endpoints"); rr.Code != http.StatusInternalServerError {
		t.Errorf("GET /endpoints = %d, want 500", rr.Code)
	}
}

func TestEndpointsDeployMarkers(t *testing.T) {
	ep := sampleEndpoints()
	live := testNow.Add(-2 * time.Hour)
	deploys := []render.Deploy{{ID: "dep-1", Status: "live", Image: newDigest, CreatedAt: live.Add(-5 * time.Minute), FinishedAt: live}}

	body := endpointsServer(t, &fakeEndpoints{ep: ep}, nil).get(t, "/endpoints").Body.String()
	if !strings.Contains(body, "Deploys aren’t marked: set -render-api-key") {
		t.Error("without Render: no note on the markers")
	}
	if strings.Contains(body, "Dashed lines") {
		t.Error("without Render: the head says dashed lines mark deploys")
	}

	body = endpointsServer(t, &fakeEndpoints{ep: ep}, &fakeRender{deploys: deploys}).get(t, "/endpoints").Body.String()
	if n := strings.Count(body, `class="chart-marker"`); n != 3 {
		t.Errorf("with Render: %d markers, want one on each of 3 charts", n)
	}
	if strings.Contains(body, "aren’t marked") || !strings.Contains(body, "Dashed lines on the charts mark deploys") {
		t.Error("with Render: says deploys aren't marked, or not that they are")
	}

	body = endpointsServer(t, &fakeEndpoints{ep: ep}, &fakeRender{deploysErr: errors.New("down")}).get(t, "/endpoints").Body.String()
	if !strings.Contains(body, "Couldn’t reach Render, so deploys aren’t marked.") || strings.Contains(body, "Dashed lines") {
		t.Error("Render failing: not said, or the head says dashed lines mark deploys")
	}
}

func TestRoutePage(t *testing.T) {
	ep := rss.Endpoints{
		Total: requests(20, 3, 0, 2, 100),
		Instances: []rss.EndpointInstance{
			{Instance: "web-1", RequestStats: requests(15, 3, 0, 0, 100)},
			{Instance: "web-2", RequestStats: requests(5, 3, 0, 2, 100)},
		},
	}
	f := &fakeEndpoints{ep: ep, errs: []rss.ServerError{
		{OccurredAt: testNow.Add(-time.Hour), Instance: "web-2", Route: "/feeds/{id}", Method: "GET", Message: "panic: <script>alert(1)</script>\ngoroutine 1 [running]:", Panic: true},
		{OccurredAt: testNow.Add(-2 * time.Hour), Instance: "web-2", Route: "/feeds/{id}", Method: "GET", Message: "load feed: timeout"},
	}}
	b := endpointsServer(t, f, nil)
	rr := b.get(t, "/endpoints/route?route=/feeds/{id}&method=GET&period=1h")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET route = %d", rr.Code)
	}
	if want := (rss.EndpointQuery{From: testNow.Add(-time.Hour), To: testNow, Step: time.Minute, Route: "/feeds/{id}", Method: "GET"}); f.q != want {
		t.Errorf("query %+v, want %+v", f.q, want)
	}
	if f.asked != [2]string{"/feeds/{id}", "GET"} || f.limit != maxRouteErrors+1 {
		t.Errorf("errors asked for %v, %d", f.asked, f.limit)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<h1 class="endpoint-title"><span class="method">GET</span> <code>/feeds<wbr>/{id}</code></h1>`,
		`<a class="btn btn-sm" href="/endpoints?period=1h">All endpoints</a>`,
		`20 requests in the last hour.`,
		// The status mix and the instances.
		`<td>2xx</td><td class="num">18</td><td class="num">90%</td>`,
		`<td>5xx</td><td class="num">2</td><td class="num">10%</td>`,
		`<td><code>web-2</code></td><td class="num">5</td><td class="num">25%</td><td class="num">40%</td>`,
		`class="chart chart-histogram"`,
		// The errors: newest first, the first line, escaped, and all of it.
		`<span class="status status-danger">Panic</span>`,
		`<summary><code>panic: &lt;script&gt;alert(1)&lt;/script&gt;</code></summary><pre><code>panic: &lt;script&gt;alert(1)&lt;/script&gt;
goroutine 1 [running]:</code></pre>`,
		`<summary><code>load feed: timeout</code></summary>`,
		`2026-11-09 11:00:00`,
		// The period links keep the route.
		`href="/endpoints/route?method=GET&amp;period=7d&amp;route=%2Ffeeds%2F%7Bid%7D"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("route page: missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert") {
		t.Error("route page: the error message isn't escaped")
	}
}

func TestRoutePageWithoutErrors(t *testing.T) {
	f := &fakeEndpoints{ep: rss.Endpoints{Total: requests(20, 3, 0, 0, 100), Instances: []rss.EndpointInstance{{Instance: "web-1", RequestStats: requests(20, 3, 0, 0, 100)}}}}
	body := endpointsServer(t, f, nil).get(t, "/endpoints/route?route=/&method=GET").Body.String()
	if !strings.Contains(body, "No server errors kept for this route.") {
		t.Error("no line for no errors")
	}
	if strings.Contains(body, "<h3 class=\"sub\">Instances</h3>") {
		t.Error("one instance: shows the instances")
	}

	// From hourly rows, which don't keep instances, it says so.
	body = endpointsServer(t, f, nil).get(t, "/endpoints/route?route=/&method=GET&period=7d").Body.String()
	if !strings.Contains(body, "Hourly metrics don’t say which instance") {
		t.Error("7d: no note on instances")
	}
}

// TestRoutePageUnknownRoute: a route with no requests and no errors, such
// as a hand-edited URL's, shows an empty state, not a 404 or a 500.
func TestRoutePageUnknownRoute(t *testing.T) {
	b := endpointsServer(t, &fakeEndpoints{ep: rss.Endpoints{Total: rss.RequestStats{Buckets: make([]int, 12)}}}, nil)
	rr := b.get(t, "/endpoints/route?route=/nowhere&method=GET")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET unknown route = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<p class="empty">No requests to <code>GET /nowhere</code> in the last 24 hours. Choose a longer period to look further back.</p>`,
		"No server errors kept for this route.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("unknown route: missing %q", want)
		}
	}
	if strings.Contains(body, "<svg class=\"chart-svg\"") {
		t.Error("unknown route: draws a chart")
	}
}

func TestRoutePageNotFound(t *testing.T) {
	b := endpointsServer(t, &fakeEndpoints{}, nil)
	for _, path := range []string{
		"/endpoints/route",
		"/endpoints/route?method=GET",
		"/endpoints/route?route=/&method=get",
		"/endpoints/route?route=/&method=BREW",
		"/endpoints/route?route=/",
	} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
}

func TestRoutePageStoreErrors(t *testing.T) {
	for name, f := range map[string]*fakeEndpoints{
		"metrics": {err: errors.New("boom")},
		"errors":  {errErr: errors.New("boom")},
	} {
		if rr := endpointsServer(t, f, nil).get(t, "/endpoints/route?route=/&method=GET"); rr.Code != http.StatusInternalServerError {
			t.Errorf("%s failing: %d, want 500", name, rr.Code)
		}
	}
}

func TestEndpointSteps(t *testing.T) {
	p := endpointPeriods[2] // 7d, hourly
	now := testNow.Add(20 * time.Minute)
	from, to := p.window(now)
	steps := endpointSteps([]rss.EndpointPoint{{T: testNow, RequestStats: requests(10, 0, 0, 0, 0)}}, from, to, now, p)
	if len(steps) != 7*24 {
		t.Fatalf("%d steps, want %d", len(steps), 7*24)
	}
	last := steps[len(steps)-1]
	if !last.T.Equal(testNow) || last.Stats.Requests != 10 || last.Covered != 20*time.Minute {
		t.Errorf("last step = %+v, want 10 requests at %v over 20 minutes", last, testNow)
	}
	if steps[0].Stats.Requests != 0 || steps[0].Covered != time.Hour {
		t.Errorf("first step = %+v, want none over an hour", steps[0])
	}
	// The current hour's 10 requests in 20 minutes are 30 an hour.
	c := requestsChart(steps, p)
	if v := c.Series[0].Points[len(steps)-1].V; v != 30 {
		t.Errorf("last rate = %v, want 30 an hour", v)
	}
}

func TestEndpointFormats(t *testing.T) {
	for _, tt := range []struct {
		part, whole int
		want        string
	}{
		{0, 0, "—"}, {0, 10, "0%"}, {1, 10000, "<0.1%"}, {24, 1000, "2.4%"}, {12, 100, "12%"}, {90, 100, "90%"},
		{14137, 14161, "99.8%"}, {9999, 10000, ">99.9%"}, {10, 10, "100%"},
	} {
		if got := percentOf(tt.part, tt.whole); got != tt.want {
			t.Errorf("percentOf(%d, %d) = %q, want %q", tt.part, tt.whole, got, tt.want)
		}
	}
	for _, tt := range []struct {
		v    float64
		want string
	}{
		{math.NaN(), "—"}, {2.5, "2.5"}, {9.96, "10"}, {175, "175"}, {2375.4, "2,375"}, {10000, ">10,000"},
	} {
		if got := ms(tt.v); got != tt.want {
			t.Errorf("ms(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
	if got := kb(2048*3, 3); got != "2.0" {
		t.Errorf("kb = %q, want 2.0", got)
	}
	if got := kb(0, 0); got != "—" {
		t.Errorf("kb with no requests = %q, want —", got)
	}
}

func TestSlashBreaks(t *testing.T) {
	breaks := templateFuncs["slashBreaks"].(func(string) template.HTML)
	for in, want := range map[string]template.HTML{
		"/":                     "/",
		"/discover/{slug}":      "/discover<wbr>/{slug}",
		"(unmatched)":           "(unmatched)",
		"/a/<script>&</script>": "/a<wbr>/&lt;script&gt;&amp;&lt;<wbr>/script&gt;",
	} {
		if got := breaks(in); got != want {
			t.Errorf("slashBreaks(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEndpointsMarksLastStepsDeploy: a deploy after the last step's start,
// within the period, is marked, as the time axis runs to the period's end.
// At 30d the last 4-hour step starts at 09:00 and the window ends at 13:00.
func TestEndpointsMarksLastStepsDeploy(t *testing.T) {
	live := testNow.Add(-2 * time.Hour)
	deploys := []render.Deploy{{ID: "dep-1", Status: "live", Image: newDigest, CreatedAt: live.Add(-5 * time.Minute), FinishedAt: live}}
	for _, path := range []string{"/endpoints?period=30d", "/endpoints/route?route=/&method=GET&period=30d"} {
		f := &fakeEndpoints{ep: sampleEndpoints()}
		body := endpointsServer(t, f, &fakeRender{deploys: deploys}).get(t, path).Body.String()
		if n := strings.Count(body, `class="chart-marker"`); n != 3 {
			t.Errorf("%s: %d markers, want one on each of 3 charts", path, n)
		}
	}
}

// TestRoutePageKeepsSort: a route's page keeps the overview's sort in its
// period links and its way back.
func TestRoutePageKeepsSort(t *testing.T) {
	f := &fakeEndpoints{ep: rss.Endpoints{Total: requests(20, 3, 0, 0, 100)}}
	body := endpointsServer(t, f, nil).get(t, "/endpoints/route?route=/&method=GET&period=7d&sort=p95").Body.String()
	for _, want := range []string{
		`<a class="btn btn-sm" href="/endpoints?period=7d&amp;sort=p95">All endpoints</a>`,
		`href="/endpoints/route?method=GET&amp;route=%2F&amp;sort=p95"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("route page: missing %q", want)
		}
	}
}

// A GET route with page views links to its web performance page, for the
// nearest period; one without (an API, a file), or any other method,
// doesn't.
func TestRoutePageLinksWebPerf(t *testing.T) {
	f := &fakeEndpoints{ep: rss.Endpoints{Total: requests(20, 3, 0, 0, 100)}}
	wp := &fakeWebPerf{views: true}
	body := endpointsServerWith(t, f, nil, wp).get(t, "/endpoints/route?route=/feeds/{id}&method=GET&period=1h").Body.String()
	if want := `<a class="btn btn-sm" href="/web-performance/route?period=24h&amp;route=%2Ffeeds%2F%7Bid%7D">Web performance</a>`; !strings.Contains(body, want) {
		t.Errorf("a page's route is missing %q", want)
	}
	// At 1h it opens the route's 24 hours, so it asks about them.
	if wp.route != "/feeds/{id}" || !wp.since.Equal(testNow.Add(-24*time.Hour)) {
		t.Errorf("HasViews asked for %s since %v, want /feeds/{id} since the 24 hours' start", wp.route, wp.since)
	}
	body = endpointsServerWith(t, f, nil, wp).get(t, "/endpoints/route?route=/feeds/{id}&method=GET&period=7d").Body.String()
	if want := `href="/web-performance/route?route=%2Ffeeds%2F%7Bid%7D">Web performance</a>`; !strings.Contains(body, want) {
		t.Errorf("7d, the web performance page's default, is missing %q", want)
	}

	for name, path := range map[string]string{
		"no views": "/endpoints/route?route=/healthcheck&method=GET",
		"a POST":   "/endpoints/route?route=/feeds/{id}&method=POST",
	} {
		wp := &fakeWebPerf{views: name != "no views"}
		body := endpointsServerWith(t, f, nil, wp).get(t, path).Body.String()
		if strings.Contains(body, `class="btn btn-sm" href="/web-performance/route`) {
			t.Errorf("%s: links to web performance", name)
		}
	}
	if rr := endpointsServerWith(t, f, nil, &fakeWebPerf{err: errors.New("boom")}).get(t, "/endpoints/route?route=/&method=GET"); rr.Code != http.StatusInternalServerError {
		t.Errorf("HasViews failing: %d, want 500", rr.Code)
	}
}
