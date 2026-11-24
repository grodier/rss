package admin

import (
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// A route's web performance page digs into one of the web performance
// page's routes (#589): how its metrics are spread, whether the time goes
// to the server or the network, and what's slow on it. It has the web
// performance page's filters, and reads the same sources.

// webPerfRouteHref is route's page's URL with p's filters, so the way back
// keeps them too, leaving defaults out.
func webPerfRouteHref(route string, p webPerfParams) string {
	v := p.values()
	v.Set("route", route)
	return "/web-performance/route?" + v.Encode()
}

// webPerfRoutePage is a route's web performance page's data.
type webPerfRoutePage struct {
	Nav      navData
	Route    string
	Back     string // the web performance page, with the same filters
	Endpoint string // the route's endpoints page
	webPerfFilters
	Period     webPeriod
	Percentile string
	Views      int
	Few        bool // under fewViews views
	Assessment assessment
	Vitals     []vitalRow
	Deploys    deployState
	// Distributions are each metric's histogram, the server's time last.
	Distributions []distribution
	Server        serverNetwork
	Breakdowns    []breakdown
	LCPElements   []detailRow
	INPTargets    []detailRow
	NavTypes      []countRow
	Transfer      []statTile // nil when no view reported a size
}

// distribution is a metric's histogram over the period.
type distribution struct {
	Name, Full string
	Note       string // its thresholds and how many views reported it
	Histogram  chart.Chart
}

// serverNetwork compares TTFB with the server's time: their values at the
// chosen percentile and the difference, and both over time.
type serverNetwork struct {
	Stats []statTile
	Chart *chart.LineChart
	// ServerViews is how many views reported the server's time, which
	// only a response with its Server-Timing header gives.
	ServerViews int
}

// detailRow is an LCP element or an interaction's target: its views, their
// share, how many were slow (not good), and the metric at the chosen
// percentile.
type detailRow struct {
	Label string // a tag or data-* name; "" when not reported
	Views int
	Share string
	Slow  string // the share not good, for interactions
	Value ratedValue
	Few   bool
}

// navTypeLabels name rum_views' navigation types.
var navTypeLabels = map[string]string{
	"navigate":     "Navigation",
	"reload":       "Reload",
	"back_forward": "Back or Forward, loaded again",
	"restore":      "Back or Forward, restored from the cache",
	"prerender":    "Prerendered",
}

// webPerfRouteHandler shows a route's real-user views. A route with none in
// the period shows that, not a 404: it may have had some before, or never
// be a page.
func (s *Server) webPerfRouteHandler(w http.ResponseWriter, r *http.Request) {
	route := r.URL.Query().Get("route")
	if route == "" {
		s.notFoundResponse(w, r)
		return
	}
	params := readWebPerfParams(r)
	period := params.periodOf()
	now := s.now()
	q := period.query(now, params.device)
	wr, err := s.services.WebPerf.WebPerfRoute(r.Context(), q, route)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := webPerfRouteData(wr, route, params, period, steps(q))
	page.Nav = s.nav(r, "webperf")
	if wr.Total.Views > 0 {
		markers, state, err := s.deploysSince(r, q.From, now.UTC())
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		page.Deploys = state
		charts := []*chart.LineChart{page.Server.Chart}
		for _, v := range page.Vitals {
			charts = append(charts, v.Chart)
		}
		for _, c := range charts {
			markToEnd(c, markers, q.To)
		}
	}
	if err := s.renderHTML(w, http.StatusOK, "webperf_route.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// markToEnd marks c, if any, with markers, and ends each of its series
// with a gap at end, so its time axis runs to the period's end and a deploy
// in its last step is marked, as on the endpoints pages.
func markToEnd(c *chart.LineChart, markers []chart.Marker, end time.Time) {
	if c == nil {
		return
	}
	c.Markers = markers
	for i := range c.Series {
		c.Series[i].Points = append(c.Series[i].Points, chart.Point{T: end, V: math.NaN()})
	}
}

// webPerfRouteData is a route's page's data from wr, whose series have a
// point at each of steps that had views.
func webPerfRouteData(wr rss.WebPerfRoute, route string, params webPerfParams, period webPeriod, steps []time.Time) webPerfRoutePage {
	page := webPerfRoutePage{
		Route: route, Back: params.href(), Endpoint: endpointHref(route, period),
		webPerfFilters: filtersFor(params, func(p webPerfParams) string { return webPerfRouteHref(route, p) }),
		Period:         period, Percentile: params.percentile, Views: wr.Total.Views,
		Few: wr.Total.Views > 0 && wr.Total.Views < fewViews,
	}
	if wr.Total.Views == 0 {
		return page
	}

	// The vitals' rows are the web performance page's, from the route's
	// views.
	page.Assessment = assess(wr.Total.Metrics)
	for _, m := range webMetrics {
		page.Vitals = append(page.Vitals, vital(m, wr.Total, wr.Series, params.percentile, steps))
	}
	for _, m := range routeMetrics {
		page.Distributions = append(page.Distributions, distributionOf(m, wr))
	}
	page.Server = serverVsNetwork(wr, params.percentile, steps)
	page.Breakdowns = []breakdown{
		{Title: "By device", Column: "Device", Rows: segmentRows(wr.Devices, params.percentile, deviceLabel)},
		{Title: "By browser", Column: "Browser", Rows: segmentRows(wr.Browsers, params.percentile, browserLabel)},
	}
	page.LCPElements = detailRows(wr.LCPElements, "lcp", params.percentile, false)
	page.INPTargets = detailRows(wr.INPTargets, "inp", params.percentile, true)
	page.NavTypes = shares(wr.NavTypes)
	for i, c := range wr.NavTypes {
		if l, ok := navTypeLabels[c.Key]; ok {
			page.NavTypes[i].Label = l
		}
	}
	page.Transfer = transferTiles(wr.Transfer)
	return page
}

// routeMetrics are the metrics a route's page shows the distributions of:
// the web performance page's, then the server's time.
var routeMetrics = append(slices.Clip(webMetrics), webMetric{"server", "Server time", "from Server-Timing", true})

// endpointHref is route's endpoints page, as a GET, for the period nearest
// period among the endpoints pages': they keep 30 days.
func endpointHref(route string, period webPeriod) string {
	key := "30d"
	switch period.Key {
	case "24h", "7d":
		key = period.Key
	}
	for _, p := range endpointPeriods {
		if p.Key == key {
			return routeHref(route, http.MethodGet, endpointsParams{period: p, sort: defaultRouteSort})
		}
	}
	panic("no endpoints period " + key)
}

// distributionOf is metric's histogram from wr's buckets, with its
// thresholds; a metric no view reported has none.
func distributionOf(m webMetric, wr rss.WebPerfRoute) distribution {
	metric := m.Key
	d := distribution{Name: m.Name, Full: m.Full}
	s, ok := wr.Total.Metrics[metric]
	if !ok || s.Views == 0 {
		d.Note = "No view reported " + m.Name + "."
		return d
	}
	unit := "ms"
	if metric == "cls" {
		unit = ""
	}
	h := chart.Histogram{Title: m.Name + " distribution", Unit: unit, Bounds: metrics.RUMBounds[metric], Counts: wr.Buckets[metric], Height: 160}
	d.Note = count(s.Views) + " views"
	if t, ok := metrics.RUMThresholds[metric]; ok {
		h.Thresholds = []chart.Threshold{{Value: t.Good, Label: "good"}, {Value: t.Poor, Label: "poor"}}
		d.Note = "Good up to " + formatMetric(metric, t.Good) + ", poor above " + formatMetric(metric, t.Poor) + " · " + d.Note
	}
	d.Histogram = h
	return d
}

// serverVsNetwork is TTFB beside the server's time at q: the server's
// time is from the response's Server-Timing header, so what's left is
// the network, connecting and redirects.
func serverVsNetwork(wr rss.WebPerfRoute, q string, steps []time.Time) serverNetwork {
	ttfb, ttfbOK := wr.Total.Metrics["ttfb"]
	server, serverOK := wr.Total.Metrics["server"]
	sn := serverNetwork{ServerViews: server.Views}
	value := func(metric string, s rss.RUMStat, ok bool) (float64, string) {
		if !ok || s.Views == 0 {
			return math.NaN(), "—"
		}
		v := percentile(s, q)
		return v, formatMetric(metric, v)
	}
	t, tText := value("ttfb", ttfb, ttfbOK)
	sv, sText := value("server", server, serverOK)
	diff := "—"
	if !math.IsNaN(t) && !math.IsNaN(sv) {
		diff = formatMetric("ttfb", math.Max(t-sv, 0))
	}
	sn.Stats = []statTile{
		{Label: q + " TTFB", Text: tText, Note: "until the first byte arrived"},
		{Label: q + " server time", Text: sText, Note: "rendering the page, from Server-Timing"},
		{Label: "Difference", Text: diff, Note: "the network, connecting and redirects"},
	}

	if sn.ServerViews > 0 {
		sn.Chart = &chart.LineChart{
			Title: "TTFB and server time, " + q + " over time", Unit: "ms", Height: 160,
			Series: []chart.Series{
				percentileSeries(wr.Series, "ttfb", q, "TTFB", steps),
				percentileSeries(wr.Series, "server", q, "Server", steps),
			},
		}
	}
	return sn
}

// detailRows are groups' rows, each with metric at q and its share of the
// views; slow adds the share of views that weren't good.
func detailRows(groups []rss.RUMGroup, metric, q string, slow bool) []detailRow {
	total := 0
	for _, g := range groups {
		total += g.Views
	}
	rows := make([]detailRow, len(groups))
	for i, g := range groups {
		s, ok := g.Metrics[metric]
		row := detailRow{Label: g.Key, Views: g.Views, Share: share(g.Views, total), Value: rated(metric, s, ok, q), Few: g.Views < fewViews}
		if slow && ok && s.Views > 0 {
			row.Slow = percentOf(s.NeedsImprovement+s.Poor, s.Views)
		}
		rows[i] = row
	}
	return rows
}

// transferTiles are the documents' sizes over the network, and how many
// came from the browser's cache; nil when no view reported a size.
func transferTiles(t rss.RUMTransfer) []statTile {
	if t.Views == 0 {
		return nil
	}
	size := func(b float64) string {
		if t.Views == t.Cached {
			return "—"
		}
		return strconv.FormatFloat(b/1024, 'f', 1, 64) + " KB"
	}
	return []statTile{
		{Label: "Median size", Text: size(t.P50), Note: "p75 " + size(t.P75) + " · p95 " + size(t.P95)},
		{Label: "From the browser’s cache", Text: percentOf(t.Cached, t.Views), Note: count(t.Cached) + " of " + count(t.Views) + " views transferred nothing"},
	}
}

// webPerfRouteLink is route's page for the endpoints pages' period p:
// the same period, or 24h for their last hour.
func webPerfRouteLink(route string, p endpointPeriod) string {
	key := p.Key
	if key == "1h" {
		key = "24h"
	}
	return webPerfRouteHref(route, webPerfParams{period: key, percentile: "p75", sort: "views"})
}
