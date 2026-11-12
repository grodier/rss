package admin

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// The web performance page shows the real-user beacons (#565): how fast
// pages load and respond for real people, at a percentile chosen on the
// page, with the share of views rated good, needs improvement and poor by
// Google's thresholds. Page views only: no visitors or sessions
// ("Observability" in docs/decisions.md).

// webPeriod is one of the page's periods: its window ends now, or for
// rollups at today's midnight, so it holds whole UTC days.
type webPeriod struct {
	Key     string
	Label   string // "in the last 7 days"
	Span    time.Duration
	Months  int // instead of Span
	Rollups bool
	Step    time.Duration
	StepFor string // "per hour", for the page views chart's title
}

// webPeriods are the page's periods, in the filter's order. Up to 30 days
// read rum_views, which keeps 30; beyond, rum_daily's whole days.
var webPeriods = []webPeriod{
	{Key: "24h", Label: "in the last 24 hours", Span: 24 * time.Hour, Step: time.Hour, StepFor: "per hour"},
	{Key: "7d", Label: "in the last 7 days", Span: 7 * 24 * time.Hour, Step: 6 * time.Hour, StepFor: "per 6 hours"},
	{Key: "30d", Label: "in the last 30 days", Span: 30 * 24 * time.Hour, Step: 24 * time.Hour, StepFor: "per day"},
	{Key: "90d", Label: "in the 90 days before today (daily rollups)", Span: 90 * 24 * time.Hour, Rollups: true, Step: 24 * time.Hour, StepFor: "per day"},
	{Key: "13m", Label: "in the 13 months before today (daily rollups)", Months: 13, Rollups: true, Step: 7 * 24 * time.Hour, StepFor: "per week"},
}

const defaultWebPeriod = "7d"

// query is the period's query at now, on device.
func (p webPeriod) query(now time.Time, device string) rss.WebPerfQuery {
	q := rss.WebPerfQuery{To: now, Device: device, Rollups: p.Rollups, Step: p.Step}
	if p.Rollups {
		q.To = now.UTC().Truncate(24 * time.Hour)
	}
	span := p.Span
	if p.Months > 0 {
		span = q.To.Sub(q.To.AddDate(0, -p.Months, 0))
	}
	// Whole steps only, so the last point isn't a part of one: 13 months
	// are 56 weeks.
	q.From = q.To.Add(-span / p.Step * p.Step)
	return q
}

// webDevices are the device filter's values; "" is every device.
var webDevices = []struct{ Key, Label string }{
	{"", "All"}, {"phone", "Phone"}, {"tablet", "Tablet"}, {"desktop", "Desktop"},
}

// webPercentiles are the percentile filter's values, in RUMStat's order.
var webPercentiles = []string{"p75", "p90", "p95"}

// The Core Web Vitals, rated at p75 as Google assesses them, then the two
// loading timings shown less prominently.
var webMetrics = []struct {
	Key, Name, Full string
	Minor           bool
}{
	{"lcp", "LCP", "Largest Contentful Paint", false},
	{"inp", "INP", "Interaction to Next Paint", false},
	{"cls", "CLS", "Cumulative Layout Shift", false},
	{"fcp", "FCP", "First Contentful Paint", true},
	{"ttfb", "TTFB", "Time to First Byte", true},
}

// fewViews is how many views a breakdown's row needs before its
// percentiles are worth reading; one with fewer is dimmed.
const fewViews = 100

// topRoutes is how many routes the routes table shows before "Show all".
const topRoutes = 20

// topReferrers is how many referrer hosts the page lists.
const topReferrers = 20

// webPerfPage is the web performance page's data.
type webPerfPage struct {
	Nav         navData
	Periods     []filterLink
	Devices     []filterLink
	Percentiles []filterLink
	Period      webPeriod
	Percentile  string // p75, p90 or p95
	Views       int
	Assessment  assessment
	Vitals      []vitalRow
	ViewsChart  chart.Chart // nil with no views
	Breakdowns  []breakdown
	Routes      routesTable
	Referrers   []countRow
	Protocols   []countRow
	Connections []countRow
}

// filterLink is one choice of a filter: its label, its URL (keeping the
// other filters), and whether it's the current one.
type filterLink struct {
	Label   string
	Href    string
	Current bool
}

// assessment is the Core Web Vitals' verdict at p75: Passed when LCP, INP
// and CLS are all good, as Google assesses them.
type assessment struct {
	Rating string // good, needs-improvement or poor (the worst), "" when not assessed
	Text   string
}

// vitalRow is one metric's row: its value at the chosen percentile, the
// other two, the shares of views by rating, and its chart over time.
type vitalRow struct {
	Name, Full string
	Minor      bool
	Selected   ratedValue
	Others     []ratedValue
	Views      int
	Bar        []ratingSegment
	Shares     string // "76% good · 15% needs improvement · 8% poor"
	Chart      chart.Chart
}

// ratedValue is a metric's value at a percentile, formatted, with its
// rating.
type ratedValue struct {
	Percentile string
	Value      string
	Rating     string // good, needs-improvement, poor, or "" for no value
}

// ratingSegment is a rating's part of a shares bar, in percent.
type ratingSegment struct {
	Rating       string
	Start, Width float64
}

// breakdown is a table of the views by a dimension, or why it's missing.
type breakdown struct {
	Title, Column string
	Rows          []segmentRow
	Missing       string // set when the period's rollups don't keep it
}

// segmentRow is a dimension's value (a device, a browser, a route…) with
// its views and its LCP, INP and CLS at the chosen percentile.
type segmentRow struct {
	Label  string
	Views  int
	Values []ratedValue // LCP, INP, CLS
	Note   string
	Few    bool
}

// routesTable is the routes, sorted by a column, with links to sort by
// each.
type routesTable struct {
	Rows    []segmentRow
	Columns []sortColumn
	// More is how many routes "Show all" adds, and MoreHref its link; 0
	// when all are shown.
	More     int
	MoreHref string
}

// sortColumn is a sortable column's header.
type sortColumn struct {
	Label   string
	Href    string
	Sort    string // aria-sort: ascending, descending or ""
	Numeric bool
}

// countRow is a value with its views, and its share where shown.
type countRow struct {
	Label string
	Views int
	Share string
}

// webPerfParams are the page's query parameters, defaults filled in.
type webPerfParams struct {
	period, device, percentile, sort string
	allRoutes                        bool
}

// readWebPerfParams reads r's filters. An unknown value is its default:
// every link the page makes is valid, so a bad one is a hand-edited URL.
func readWebPerfParams(r *http.Request) webPerfParams {
	v := r.URL.Query()
	p := webPerfParams{period: defaultWebPeriod, percentile: "p75", sort: "views"}
	for _, wp := range webPeriods {
		if v.Get("period") == wp.Key {
			p.period = wp.Key
		}
	}
	for _, d := range webDevices {
		if d.Key != "" && v.Get("device") == d.Key {
			p.device = d.Key
		}
	}
	for _, q := range webPercentiles {
		if v.Get("p") == q {
			p.percentile = q
		}
	}
	switch s := v.Get("sort"); s {
	case "route", "lcp", "inp", "cls":
		p.sort = s
	}
	p.allRoutes = v.Get("routes") == "all"
	return p
}

// href is the page's URL with p's filters, leaving defaults out.
func (p webPerfParams) href() string {
	v := url.Values{}
	if p.period != defaultWebPeriod {
		v.Set("period", p.period)
	}
	if p.device != "" {
		v.Set("device", p.device)
	}
	if p.percentile != "p75" {
		v.Set("p", p.percentile)
	}
	if p.sort != "views" {
		v.Set("sort", p.sort)
	}
	if p.allRoutes {
		v.Set("routes", "all")
	}
	if len(v) == 0 {
		return "/web-performance"
	}
	return "/web-performance?" + v.Encode()
}

// webPerfHandler shows the web performance page.
func (s *Server) webPerfHandler(w http.ResponseWriter, r *http.Request) {
	params := readWebPerfParams(r)
	var period webPeriod
	for _, p := range webPeriods {
		if p.Key == params.period {
			period = p
		}
	}
	wp, err := s.services.WebPerf.WebPerf(r.Context(), period.query(s.now(), params.device))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := webPerfData(wp, params, period)
	data.Nav = s.nav(r, "webperf")
	if err := s.renderHTML(w, http.StatusOK, "webperf.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// webPerfData is the page's data from wp.
func webPerfData(wp rss.WebPerf, params webPerfParams, period webPeriod) webPerfPage {
	page := webPerfPage{Period: period, Percentile: params.percentile, Views: wp.Total.Views}
	for _, p := range webPeriods {
		next := params
		next.period = p.Key
		page.Periods = append(page.Periods, filterLink{Label: p.Key, Href: next.href(), Current: p.Key == params.period})
	}
	for _, d := range webDevices {
		next := params
		next.device = d.Key
		page.Devices = append(page.Devices, filterLink{Label: d.Label, Href: next.href(), Current: d.Key == params.device})
	}
	for _, q := range webPercentiles {
		next := params
		next.percentile = q
		page.Percentiles = append(page.Percentiles, filterLink{Label: q, Href: next.href(), Current: q == params.percentile})
	}
	if wp.Total.Views == 0 {
		return page
	}

	page.Assessment = assess(wp.Total.Metrics)
	for _, m := range webMetrics {
		page.Vitals = append(page.Vitals, vital(m.Key, m.Name, m.Full, m.Minor, wp, params.percentile))
	}
	page.ViewsChart = routeViewsChart(wp, period)

	page.Breakdowns = []breakdown{
		{Title: "By device", Column: "Device", Rows: segmentRows(wp.Devices, params.percentile, deviceLabel)},
		{Title: "By browser", Column: "Browser", Rows: segmentRows(wp.Browsers, params.percentile, browserLabel)},
		{Title: "Installed or in the browser", Column: "Where", Rows: segmentRows(wp.Installed, params.percentile, installedLabel)},
	}
	if period.Rollups {
		page.Breakdowns[2].Missing = "Only the last 30 days’ views say whether the app was installed: choose 24h, 7d or 30d."
	}
	page.Routes = routes(wp.Routes, params)

	for i, c := range wp.Referrers {
		if i == topReferrers {
			break
		}
		page.Referrers = append(page.Referrers, countRow{Label: c.Key, Views: c.Views})
	}
	page.Protocols = shares(wp.Protocols)
	page.Connections = shares(wp.Connections)
	return page
}

// percentile is s's value at q (p75, p90 or p95).
func percentile(s rss.RUMStat, q string) float64 {
	switch q {
	case "p90":
		return s.P90
	case "p95":
		return s.P95
	}
	return s.P75
}

// rated is metric's stat at q, formatted and rated; a metric no view
// reported is "—".
func rated(metric string, s rss.RUMStat, ok bool, q string) ratedValue {
	if !ok || s.Views == 0 {
		return ratedValue{Percentile: q, Value: "—"}
	}
	v := percentile(s, q)
	return ratedValue{Percentile: q, Value: formatMetric(metric, v), Rating: rating(metric, v)}
}

// rating is v's rating by metric's thresholds: a value on a threshold is
// in the better rating, as Google rates them.
func rating(metric string, v float64) string {
	t, ok := metrics.RUMThresholds[metric]
	switch {
	case !ok || math.IsNaN(v):
		return ""
	case v <= t.Good:
		return "good"
	case v <= t.Poor:
		return "needs-improvement"
	}
	return "poor"
}

// ratingWords are the ratings as the page says them.
var ratingWords = map[string]string{"good": "Good", "needs-improvement": "Needs improvement", "poor": "Poor"}

// formatMetric formats a metric's value: CLS to two places, times in
// milliseconds under a second and in seconds to two places from one.
func formatMetric(metric string, v float64) string {
	if metric == "cls" {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	if v >= 999.5 {
		return strconv.FormatFloat(v/1000, 'f', 2, 64) + " s"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// assess returns the Core Web Vitals' assessment from LCP's, INP's and
// CLS's p75.
func assess(stats map[string]rss.RUMStat) assessment {
	var missing, failing []string
	worst := "good"
	for _, m := range webMetrics[:3] {
		s, ok := stats[m.Key]
		if !ok || s.Views == 0 {
			missing = append(missing, m.Name)
			continue
		}
		switch r := rating(m.Key, s.P75); r {
		case "needs-improvement":
			failing = append(failing, m.Name+" needs improvement")
			if worst == "good" {
				worst = r
			}
		case "poor":
			failing = append(failing, m.Name+" is poor")
			worst = r
		}
	}
	switch {
	case len(failing) > 0:
		return assessment{Rating: worst, Text: "at p75, as Google assesses it: " + strings.Join(failing, ", ")}
	case len(missing) > 0:
		return assessment{Text: "No " + strings.Join(missing, " or ") + " reported"}
	}
	return assessment{Rating: "good", Text: "LCP, INP and CLS are good at p75, as Google assesses them"}
}

// vital is metric's row.
func vital(metric, name, full string, minor bool, wp rss.WebPerf, q string) vitalRow {
	s, ok := wp.Total.Metrics[metric]
	row := vitalRow{Name: name, Full: full, Minor: minor, Views: s.Views, Selected: rated(metric, s, ok, q)}
	for _, p := range webPercentiles {
		if p != q {
			row.Others = append(row.Others, rated(metric, s, ok, p))
		}
	}
	if !ok || s.Views == 0 {
		return row
	}
	row.Bar, row.Shares = ratingShares(s)

	points := make([]chart.Point, len(wp.Series))
	for i, p := range wp.Series {
		v := math.NaN()
		if ps, ok := p.Metrics[metric]; ok && ps.Views > 0 {
			v = percentile(ps, q)
		}
		points[i] = chart.Point{T: p.T, V: v}
	}
	t := metrics.RUMThresholds[metric]
	unit := "ms"
	if metric == "cls" {
		unit = ""
	}
	row.Chart = chart.LineChart{
		Title:      name + ", " + q + " over time",
		Unit:       unit,
		Height:     140,
		Series:     []chart.Series{{Name: q, Points: points}},
		Thresholds: []chart.Threshold{{Value: t.Good, Label: "good"}, {Value: t.Poor, Label: "poor"}},
	}
	return row
}

// ratingShares are s's views by rating as a bar and in words.
func ratingShares(s rss.RUMStat) ([]ratingSegment, string) {
	parts := []struct {
		rating string
		n      int
	}{{"good", s.Good}, {"needs-improvement", s.NeedsImprovement}, {"poor", s.Poor}}
	var bar []ratingSegment
	var words []string
	start := 0.0
	for _, p := range parts {
		width := float64(p.n) / float64(s.Views) * 100
		if width > 0 {
			bar = append(bar, ratingSegment{Rating: p.rating, Start: start, Width: width})
		}
		start += width
		words = append(words, share(p.n, s.Views)+" "+strings.ToLower(ratingWords[p.rating]))
	}
	return bar, strings.Join(words, " · ")
}

// segmentRows are groups' rows at q, in groups' order, labelled by label.
func segmentRows(groups []rss.RUMGroup, q string, label func(string) string) []segmentRow {
	rows := make([]segmentRow, 0, len(groups))
	for _, g := range groups {
		row := segmentRow{Label: label(g.Key), Views: g.Views, Few: g.Views < fewViews}
		var missing []string
		for _, m := range webMetrics[:3] {
			s, ok := g.Metrics[m.Key]
			v := rated(m.Key, s, ok, q)
			if v.Rating == "" {
				missing = append(missing, m.Name)
			}
			row.Values = append(row.Values, v)
		}
		var notes []string
		if g.Key == "safari" {
			notes = append(notes, "Safari’s INP is known to over-report")
		}
		if len(missing) > 0 {
			notes = append(notes, "No "+strings.Join(missing, " or ")+" reported")
		}
		row.Note = strings.Join(notes, ". ")
		rows = append(rows, row)
	}
	return rows
}

func deviceLabel(k string) string {
	switch k {
	case "phone":
		return "Phone"
	case "tablet":
		return "Tablet"
	case "desktop":
		return "Desktop"
	}
	return k
}

// browserLabel names a browser family as rum.ts reports it.
func browserLabel(k string) string {
	switch k {
	case "chrome":
		return "Chrome"
	case "edge":
		return "Edge"
	case "firefox":
		return "Firefox"
	case "safari":
		return "Safari"
	case "samsung":
		return "Samsung Internet"
	case "other":
		return "Other"
	}
	return k
}

func installedLabel(k string) string {
	if k == "true" {
		return "Installed"
	}
	return "In the browser"
}

// routeSorts are the routes table's columns: the key they sort by, and
// whether that's numeric (most first) rather than by name.
var routeSorts = []struct {
	key, label string
	numeric    bool
}{
	{"route", "Route", false},
	{"views", "Views", true},
	{"lcp", "LCP", true},
	{"inp", "INP", true},
	{"cls", "CLS", true},
}

// routes is the routes table, sorted by params.sort: by name, or by views
// or a metric at the chosen percentile, most first, with routes missing it
// last.
func routes(groups []rss.RUMGroup, params webPerfParams) routesTable {
	sorted := append([]rss.RUMGroup(nil), groups...)
	value := func(g rss.RUMGroup) float64 {
		if params.sort == "views" {
			return float64(g.Views)
		}
		s, ok := g.Metrics[params.sort]
		if !ok || s.Views == 0 {
			return math.Inf(-1)
		}
		return percentile(s, params.percentile)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if params.sort == "route" {
			return sorted[i].Key < sorted[j].Key
		}
		a, b := value(sorted[i]), value(sorted[j])
		if a != b {
			return a > b
		}
		return sorted[i].Key < sorted[j].Key
	})

	var t routesTable
	if !params.allRoutes && len(sorted) > topRoutes {
		t.More = len(sorted) - topRoutes
		all := params
		all.allRoutes = true
		t.MoreHref = all.href()
		sorted = sorted[:topRoutes]
	}
	t.Rows = segmentRows(sorted, params.percentile, func(k string) string { return k })
	for _, c := range routeSorts {
		next := params
		next.sort = c.key
		col := sortColumn{Label: c.label, Href: next.href(), Numeric: c.numeric}
		if c.key != "route" && c.key != "views" {
			col.Label = params.percentile + " " + c.label
		}
		if c.key == params.sort {
			col.Sort = "descending"
			if !c.numeric {
				col.Sort = "ascending"
			}
		}
		t.Columns = append(t.Columns, col)
	}
	return t
}

// shares are counts' rows with their share of all of them; "" is "Not
// reported".
func shares(counts []rss.RUMCount) []countRow {
	total := 0
	for _, c := range counts {
		total += c.Views
	}
	rows := make([]countRow, len(counts))
	for i, c := range counts {
		label := c.Key
		if label == "" {
			label = "Not reported"
		}
		rows[i] = countRow{Label: label, Views: c.Views, Share: share(c.Views, total)}
	}
	return rows
}

// routeViewsChart charts the views of the routes with the most views, a
// line each, a point per step.
func routeViewsChart(wp rss.WebPerf, period webPeriod) chart.Chart {
	top := append([]rss.RUMGroup(nil), wp.Routes...)
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].Views != top[j].Views {
			return top[i].Views > top[j].Views
		}
		return top[i].Key < top[j].Key
	})
	top = top[:min(len(top), 3)]
	var steps []time.Time
	seen := map[time.Time]bool{}
	views := map[string]map[time.Time]int{}
	for _, v := range wp.RouteViews {
		if !seen[v.T] {
			seen[v.T] = true
			steps = append(steps, v.T)
		}
		if views[v.Route] == nil {
			views[v.Route] = map[time.Time]int{}
		}
		views[v.Route][v.T] += v.Views
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Before(steps[j]) })
	c := chart.LineChart{Title: fmt.Sprintf("Page views %s, top routes", period.StepFor), Height: 160}
	for _, g := range top {
		s := chart.Series{Name: g.Key}
		for _, t := range steps {
			s.Points = append(s.Points, chart.Point{T: t, V: float64(views[g.Key][t])})
		}
		c.Series = append(c.Series, s)
	}
	return c
}
