package admin

import (
	"cmp"
	"context"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// The endpoints pages show www's requests from its request metrics (#563):
// the service as a whole and each route, with its latency percentiles
// estimated from the summed histogram buckets (metrics.Percentile), and
// each route's recent server errors (#564).

// endpointPeriod is one of the endpoints pages' periods.
type endpointPeriod struct {
	Key   string
	Label string // "in the last 24 hours"
	Span  time.Duration
	Step  time.Duration // between the charts' points
	// Hourly reads the hourly rows, past the minute rows' 24 hours here.
	Hourly bool
	// Per is the requests chart's rate: requests per minute, or per hour
	// from hourly rows; PerLabel says it.
	Per      time.Duration
	PerLabel string
}

// endpointPeriods are the pages' periods, in the filter's order.
var endpointPeriods = []endpointPeriod{
	{Key: "1h", Label: "in the last hour", Span: time.Hour, Step: time.Minute, Per: time.Minute, PerLabel: "per minute"},
	{Key: "24h", Label: "in the last 24 hours", Span: 24 * time.Hour, Step: 10 * time.Minute, Per: time.Minute, PerLabel: "per minute"},
	{Key: "7d", Label: "in the last 7 days", Span: 7 * 24 * time.Hour, Step: time.Hour, Hourly: true, Per: time.Hour, PerLabel: "per hour"},
	{Key: "30d", Label: "in the last 30 days", Span: 30 * 24 * time.Hour, Step: 4 * time.Hour, Hourly: true, Per: time.Hour, PerLabel: "per hour"},
}

const defaultEndpointPeriod = "24h"

// maxRouteErrors is how many of a route's server errors its page lists.
const maxRouteErrors = 50

// window is the period's query at now: minute rows up to the current
// minute, which isn't written yet, or hourly rows up to the end of the
// current hour, whose minutes so far are read from minute rows. The window
// is whole steps, so no point is part of one but the current hour's.
func (p endpointPeriod) window(now time.Time) (from, to time.Time) {
	to = now.UTC().Truncate(time.Minute)
	if p.Hourly {
		to = now.UTC().Truncate(time.Hour).Add(time.Hour)
	}
	return to.Add(-p.Span), to
}

// endpointsParams are the overview's query parameters, defaults filled in.
type endpointsParams struct {
	period endpointPeriod
	sort   string
}

// routeSortColumns are the routes table's columns: their sort key, their
// header, and whether they're numbers (most first) rather than names.
var routeSortColumns = []struct {
	key, label string
	numeric    bool
}{
	{"route", "Route", false},
	{"requests", "Requests", true},
	{"4xx", "4xx %", true},
	{"5xx", "5xx %", true},
	{"p50", "p50 ms", true},
	{"p95", "p95 ms", true},
	{"p99", "p99 ms", true},
	{"size", "Avg KB", true},
}

const defaultRouteSort = "requests"

// readPeriod is r's period, or the default: every link the pages make is
// valid, so an unknown one is a hand-edited URL.
func readPeriod(r *http.Request) endpointPeriod {
	for _, p := range endpointPeriods {
		if r.URL.Query().Get("period") == p.Key {
			return p
		}
	}
	for _, p := range endpointPeriods {
		if p.Key == defaultEndpointPeriod {
			return p
		}
	}
	panic("no default endpoint period")
}

// readEndpointsParams reads the overview's parameters; an unknown sort is
// the default, as an unknown period is.
func readEndpointsParams(r *http.Request) endpointsParams {
	p := endpointsParams{period: readPeriod(r), sort: defaultRouteSort}
	for _, c := range routeSortColumns {
		if r.URL.Query().Get("sort") == c.key {
			p.sort = c.key
		}
	}
	return p
}

// href is the overview's URL with p's parameters, leaving defaults out.
func (p endpointsParams) href() string {
	v := url.Values{}
	if p.period.Key != defaultEndpointPeriod {
		v.Set("period", p.period.Key)
	}
	if p.sort != defaultRouteSort {
		v.Set("sort", p.sort)
	}
	if len(v) == 0 {
		return "/endpoints"
	}
	return "/endpoints?" + v.Encode()
}

// routeHref is a route's page's URL, with p's period, and its sort for
// the way back to the overview, leaving defaults out.
func routeHref(route, method string, p endpointsParams) string {
	v := url.Values{"route": {route}, "method": {method}}
	if p.period.Key != defaultEndpointPeriod {
		v.Set("period", p.period.Key)
	}
	if p.sort != defaultRouteSort {
		v.Set("sort", p.sort)
	}
	return "/endpoints/route?" + v.Encode()
}

// endpointsPage is the overview's data.
type endpointsPage struct {
	Nav     navData
	Periods []filterLink
	Period  endpointPeriod
	Total   rss.RequestStats
	Charts  []endpointChart
	Deploys deployState
	Routes  []routeRow
	Columns []sortColumn
}

// endpointChart is a chart with the period's figure for its caption, so
// it isn't taken for the line's latest value: "Total" and "21,887", or
// "Overall" and "146 ms".
type endpointChart struct {
	chart.LineChart
	FigureLabel, Figure string
}

// deployState says whether the charts mark deploys, and why not.
type deployState struct {
	Rendering, Failed bool
}

// Marked is whether the charts mark the deploys.
func (d deployState) Marked() bool { return d.Rendering && !d.Failed }

// routeRow is a route and method's row in the routes table.
type routeRow struct {
	Route, Method, Href string
	Requests            int
	ClientErrors        string // the share of 4xx
	ServerErrors        string // the share of 5xx
	P50, P95, P99       string
	Size                string // the average response, in KB
}

// endpointsHandler shows every route's requests.
func (s *Server) endpointsHandler(w http.ResponseWriter, r *http.Request) {
	params := readEndpointsParams(r)
	now := s.now().UTC().Truncate(time.Second)
	from, to := params.period.window(now)
	ep, err := s.services.Endpoints.Endpoints(r.Context(), rss.EndpointQuery{From: from, To: to, Hourly: params.period.Hourly, Step: params.period.Step})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := endpointsPage{Nav: s.nav(r, "endpoints"), Period: params.period, Total: ep.Total}
	for _, p := range endpointPeriods {
		next := params
		next.period = p
		page.Periods = append(page.Periods, filterLink{Label: p.Key, Href: next.href(), Current: p.Key == params.period.Key})
	}
	if ep.Total.Requests > 0 {
		page.Charts = endpointCharts(ep, from, to, now, params.period, false)
		page.Deploys, err = s.markDeploys(r, page.Charts, from, now)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		page.Routes = routeRows(ep.Routes, params)
	}
	for _, c := range routeSortColumns {
		next := params
		next.sort = c.key
		col := sortColumn{Label: c.label, Href: next.href(), Numeric: c.numeric}
		if c.key == params.sort {
			col.Sort = "descending"
			if !c.numeric {
				col.Sort = "ascending"
			}
		}
		page.Columns = append(page.Columns, col)
	}
	if err := s.renderHTML(w, http.StatusOK, "endpoints.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// markDeploys marks charts with the deploys that went live from from on,
// from Render when the admin has its settings. Render failing or timing
// out is logged and said on the page, as on the service page; the error
// returned is the database's.
func (s *Server) markDeploys(r *http.Request, charts []endpointChart, from, now time.Time) (deployState, error) {
	if s.services.Render == nil {
		return deployState{}, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), renderTimeout)
	defer cancel()
	deploys, err := s.services.Render.Deploys(ctx, deploysFetched)
	if err != nil {
		s.logger.Warn("endpoints page: read Render's deploys", "error", err)
		return deployState{Rendering: true, Failed: true}, nil
	}
	versions, err := s.deployVersions(r.Context(), deploys, now, s.config.AppInternalURL != "")
	if err != nil {
		return deployState{}, err
	}
	markers := deployMarkers(deploys, versions, from)
	for i := range charts {
		charts[i].Markers = markers
	}
	return deployState{Rendering: true}, nil
}

// endpointStep is a step of the charts: its requests, and how much of it
// has passed, which is less than the step for the current hour's.
type endpointStep struct {
	T       time.Time
	Stats   rss.RequestStats
	Covered time.Duration
}

// endpointSteps are the period's steps from from to to, with series'
// requests in them; a step with none has none, rather than being left out.
// now ends the last steps' coverage.
func endpointSteps(series []rss.EndpointPoint, from, to, now time.Time, p endpointPeriod) []endpointStep {
	at := map[time.Time]rss.RequestStats{}
	for _, pt := range series {
		at[pt.T.UTC()] = pt.RequestStats
	}
	end := now.UTC().Truncate(time.Minute)
	var steps []endpointStep
	for t := from; t.Before(to); t = t.Add(p.Step) {
		stepEnd := t.Add(p.Step)
		if end.Before(stepEnd) {
			stepEnd = end
		}
		steps = append(steps, endpointStep{T: t, Stats: at[t], Covered: stepEnd.Sub(t)})
	}
	return steps
}

const endpointChartHeight = 140

// endpointCharts are the period's charts of ep, from from to to: requests,
// the 5xx rate and p95 for the overview, and for a route's page the 4xx
// rate and p50 and p99 too. Each series ends with a gap at to, so the time
// axis spans the whole period and a deploy in its last step is marked.
func endpointCharts(ep rss.Endpoints, from, to, now time.Time, p endpointPeriod, route bool) []endpointChart {
	steps := endpointSteps(ep.Series, from, to, now, p)
	t := ep.Total
	charts := []endpointChart{
		{requestsChart(steps, p), "Total", count(t.Requests)},
		{errorRateChart(steps, false), "Overall", percentOf(t.Classes[4], t.Requests)},
		{latencyChart(steps, []float64{0.95}), "Overall", ms(percentileOf(t.Buckets, 0.95)) + " ms"},
	}
	if route {
		charts[1] = endpointChart{errorRateChart(steps, true), "Overall", percentOf(t.Classes[4], t.Requests) + " 5xx · " + percentOf(t.Classes[3], t.Requests) + " 4xx"}
		charts[2] = endpointChart{latencyChart(steps, []float64{0.5, 0.95, 0.99}), "Overall", "p95 " + ms(percentileOf(t.Buckets, 0.95)) + " ms"}
	}
	for i := range charts {
		for j := range charts[i].Series {
			charts[i].Series[j].Points = append(charts[i].Series[j].Points, chart.Point{T: to, V: math.NaN()})
		}
	}
	return charts
}

// requestsChart charts the requests per minute or hour: each step's
// requests over the minutes or hours of it that have passed.
func requestsChart(steps []endpointStep, p endpointPeriod) chart.LineChart {
	points := make([]chart.Point, len(steps))
	for i, st := range steps {
		v := math.NaN()
		if st.Covered > 0 {
			v = float64(st.Stats.Requests) / (float64(st.Covered) / float64(p.Per))
		}
		points[i] = chart.Point{T: st.T, V: v}
	}
	unit := "/min"
	if p.Per == time.Hour {
		unit = "/h"
	}
	return chart.LineChart{
		Title: "Requests " + p.PerLabel, Unit: unit, Height: endpointChartHeight,
		Series: []chart.Series{{Name: "Requests", Points: points}},
	}
}

// errorRateChart charts each step's share of 5xx, and of 4xx when
// clientErrors; a step with no requests is a gap.
func errorRateChart(steps []endpointStep, clientErrors bool) chart.LineChart {
	rate := func(class int) []chart.Point {
		points := make([]chart.Point, len(steps))
		for i, st := range steps {
			v := math.NaN()
			if st.Stats.Requests > 0 {
				v = float64(st.Stats.Classes[class-1]) * 100 / float64(st.Stats.Requests)
			}
			points[i] = chart.Point{T: st.T, V: v}
		}
		return points
	}
	c := chart.LineChart{Title: "5xx rate", Unit: "%", Height: endpointChartHeight, Series: []chart.Series{{Name: "5xx", Points: rate(5)}}}
	if clientErrors {
		c.Title = "Error rate"
		c.Series = append(c.Series, chart.Series{Name: "4xx", Points: rate(4)})
	}
	return c
}

// latencyChart charts each step's latency at qs (0.95 for p95); a step
// with no requests is a gap.
func latencyChart(steps []endpointStep, qs []float64) chart.LineChart {
	c := chart.LineChart{Title: "Latency", Unit: "ms", Height: endpointChartHeight}
	for _, q := range qs {
		s := chart.Series{Name: "p" + strconv.Itoa(int(math.Round(q*100)))}
		for _, st := range steps {
			s.Points = append(s.Points, chart.Point{T: st.T, V: percentileOf(st.Stats.Buckets, q)})
		}
		c.Series = append(c.Series, s)
	}
	if len(qs) == 1 {
		c.Title = c.Series[0].Name + " latency"
	}
	return c
}

// percentileOf is buckets' value at q, NaN with no requests.
func percentileOf(buckets []int, q float64) float64 {
	return metrics.Percentile(metrics.DurationBounds, buckets, q)
}

// ms formats a latency in milliseconds: one decimal under 10, whole above,
// "—" for none, and past the last bound only that it's above it.
func ms(v float64) string {
	last := metrics.DurationBounds[len(metrics.DurationBounds)-1]
	switch {
	case math.IsNaN(v):
		return "—"
	case v >= last:
		return ">" + count(int(last))
	case v < 9.95:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return count(int(math.Round(v)))
}

// percentOf is part's share of whole, precise near the ends, where error
// rates and status mixes are: "0%", "<0.1%", "2.4%", "12%", "99.8%",
// ">99.9%", "100%"; only none or all of it are 0% or 100%. "—" when whole
// is 0.
func percentOf(part, whole int) string {
	if whole == 0 {
		return "—"
	}
	p := float64(part) * 100 / float64(whole)
	switch {
	case part == 0:
		return "0%"
	case part == whole:
		return "100%"
	case p < 0.05:
		return "<0.1%"
	case p >= 99.95:
		return ">99.9%"
	case p < 9.95 || p >= 90.05:
		return strconv.FormatFloat(p, 'f', 1, 64) + "%"
	}
	return strconv.Itoa(int(math.Round(p))) + "%"
}

// kb formats an average response size in kilobytes, to one decimal.
func kb(bytes int64, requests int) string {
	if requests == 0 {
		return "—"
	}
	return strconv.FormatFloat(float64(bytes)/float64(requests)/1024, 'f', 1, 64)
}

// routeRows are routes' rows sorted by params.sort: by route, or by a
// number, most first, with routes missing it (no requests) last; ties by
// route and method.
func routeRows(routes []rss.EndpointRoute, params endpointsParams) []routeRow {
	value := func(r rss.EndpointRoute) float64 {
		switch params.sort {
		case "4xx", "5xx":
			class := 4
			if params.sort == "5xx" {
				class = 5
			}
			return float64(r.Classes[class-1]) / float64(r.Requests)
		case "p50":
			return percentileOf(r.Buckets, 0.5)
		case "p95":
			return percentileOf(r.Buckets, 0.95)
		case "p99":
			return percentileOf(r.Buckets, 0.99)
		case "size":
			return float64(r.BytesSum) / float64(r.Requests)
		}
		return float64(r.Requests)
	}
	sorted := slices.Clone(routes)
	slices.SortStableFunc(sorted, func(a, b rss.EndpointRoute) int {
		byName := cmp.Or(cmp.Compare(a.Route, b.Route), cmp.Compare(a.Method, b.Method))
		if params.sort == "route" {
			return byName
		}
		va, vb := value(a), value(b)
		if math.IsNaN(va) {
			va = math.Inf(-1)
		}
		if math.IsNaN(vb) {
			vb = math.Inf(-1)
		}
		return cmp.Or(cmp.Compare(vb, va), byName)
	})
	rows := make([]routeRow, len(sorted))
	for i, r := range sorted {
		rows[i] = routeRow{
			Route: r.Route, Method: r.Method, Href: routeHref(r.Route, r.Method, params),
			Requests:     r.Requests,
			ClientErrors: percentOf(r.Classes[3], r.Requests),
			ServerErrors: percentOf(r.Classes[4], r.Requests),
			P50:          ms(percentileOf(r.Buckets, 0.5)),
			P95:          ms(percentileOf(r.Buckets, 0.95)),
			P99:          ms(percentileOf(r.Buckets, 0.99)),
			Size:         kb(r.BytesSum, r.Requests),
		}
	}
	return rows
}

// routePage is a route's page's data.
type routePage struct {
	Nav           navData
	Route, Method string
	Back          string // the overview, for the same period
	Periods       []filterLink
	Period        endpointPeriod
	Total         rss.RequestStats
	Charts        []endpointChart
	Deploys       deployState
	Histogram     chart.Histogram
	Statuses      []countRow
	Instances     []instanceRow
	Errors        []errorRow
	MoreErrors    bool // the list stopped at ErrorsShown
	ErrorsShown   int  // maxRouteErrors
	ErrorsKept    int  // how many server errors www keeps, across routes
}

// instanceRow is an instance's requests on the route.
type instanceRow struct {
	Instance     string
	Requests     int
	Share        string
	ServerErrors string
	P95          string
}

// errorRow is a server error as the route's page shows it: its message's
// first line, which the stylesheet cuts to the column, and all of it.
type errorRow struct {
	At                 time.Time
	Instance           string
	Panic              bool
	FirstLine, Message string
}

// routeHandler shows a route's requests and recent server errors. A route
// with neither in the period shows that, not a 404: it may have had them
// before, or be the route the URL names but never served.
func (s *Server) routeHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	route, method := q.Get("route"), q.Get("method")
	if route == "" || metrics.NormalizeMethod(method) != method {
		s.notFoundResponse(w, r)
		return
	}
	params := readEndpointsParams(r)
	period := params.period
	now := s.now().UTC().Truncate(time.Second)
	from, to := period.window(now)
	ep, err := s.services.Endpoints.Endpoints(r.Context(), rss.EndpointQuery{
		From: from, To: to, Hourly: period.Hourly, Step: period.Step, Route: route, Method: method,
	})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	// One more than shown, to know whether any were left out.
	errs, err := s.services.Endpoints.RouteErrors(r.Context(), route, method, maxRouteErrors+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := routePage{
		Nav: s.nav(r, "endpoints"), Route: route, Method: method,
		Back: params.href(), Period: period, Total: ep.Total,
		MoreErrors: len(errs) > maxRouteErrors, ErrorsShown: maxRouteErrors, ErrorsKept: metrics.ErrorsKept,
	}
	for _, p := range endpointPeriods {
		next := params
		next.period = p
		page.Periods = append(page.Periods, filterLink{Label: p.Key, Href: routeHref(route, method, next), Current: p.Key == period.Key})
	}
	for _, e := range errs[:min(len(errs), maxRouteErrors)] {
		first, _, _ := strings.Cut(strings.TrimSpace(e.Message), "\n")
		page.Errors = append(page.Errors, errorRow{
			At: e.OccurredAt, Instance: e.Instance, Panic: e.Panic,
			FirstLine: first, Message: e.Message,
		})
	}

	if t := ep.Total; t.Requests > 0 {
		page.Charts = endpointCharts(ep, from, to, now, period, true)
		page.Deploys, err = s.markDeploys(r, page.Charts, from, now)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		page.Histogram = chart.Histogram{Title: "Latency", Unit: "ms", Bounds: metrics.DurationBounds, Counts: t.Buckets, Height: 180}
		for i, n := range t.Classes {
			if n > 0 {
				page.Statuses = append(page.Statuses, countRow{Label: strconv.Itoa(i+1) + "xx", Views: n, Share: percentOf(n, t.Requests)})
			}
		}
		if len(ep.Instances) > 1 {
			for _, in := range ep.Instances {
				page.Instances = append(page.Instances, instanceRow{
					Instance: in.Instance, Requests: in.Requests, Share: percentOf(in.Requests, t.Requests),
					ServerErrors: percentOf(in.Classes[4], in.Requests), P95: ms(percentileOf(in.Buckets, 0.95)),
				})
			}
		}
	}
	if err := s.renderHTML(w, http.StatusOK, "endpoint.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
