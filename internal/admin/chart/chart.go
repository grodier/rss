// Package chart draws the admin's charts, line charts and histograms, as SVG
// on the server, with no chart library and no script: they fit the admin's
// CSP and work without JavaScript (see "Admin" in docs/decisions.md).
//
// A chart stretches to its container's width at a fixed height: positions
// across are percentages of the drawing's width and positions down are
// pixels, so its text stays the same size at every width. Labels on the
// time axis and under a histogram's bars carry classes that the admin's
// stylesheet hides when the chart is narrow (chart.css), so fewer are drawn
// on phones. Colors are classes only, styled with the color tokens.
package chart

import (
	"cmp"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Chart is what the admin's chart template function draws.
type Chart interface {
	SVG() template.HTML
}

// Series is one line or set of bars.
type Series struct {
	Name   string
	Points []Point
}

// Point is a series' value at a time.
type Point struct {
	T time.Time
	V float64 // NaN for no data: a gap in a line
}

// Marker is a vertical line at a time, e.g. a deploy, with its label.
type Marker struct {
	T     time.Time
	Label string
}

// Threshold is a horizontal line at a value, e.g. a Core Web Vital's "good"
// limit, with its label.
type Threshold struct {
	Value float64
	Label string
}

// LineChart draws up to three series over time.
type LineChart struct {
	Title      string
	Unit       string // "ms", "%", "MB", "" …, shown on the axis and in labels
	Series     []Series
	Markers    []Marker
	Height     int     // in pixels, 200 by default; the width is the container's
	YMin       float64 // 0 by default
	Thresholds []Threshold
	Bands      bool // #571's checkpoint only: thresholds as tinted bands
}

// Histogram draws bucket counts as bars, with each bucket's upper bound under
// it. Counts has one more element than Bounds: the last bucket counts
// everything above the last bound, as in internal/metrics.
type Histogram struct {
	Title  string
	Unit   string
	Bounds []float64
	Counts []int
	Height int // in pixels, 200 by default
}

const (
	defaultHeight = 200

	// The plot's top leaves room for the top tick's label and markers'
	// labels; its bottom for the time axis' labels.
	plotTop    = 18
	axisHeight = 22

	// maxSeries is how many series colors the stylesheet has.
	maxSeries = 3

	// lineGutter is the room, in pixels, that chart.css leaves to the right
	// of a line chart's drawing for each line's latest value.
	lineGutter = 56

	// The pixel width of a tick label's character at 11px (generous for
	// digits), the space between two labels, and the least distance
	// between two lines' latest values.
	charWidth    = 6.5
	labelSpacing = 10
	endLabelGap  = 13
)

// widths are the smallest widths, in pixels, of a chart's container at
// which chart.css shows each class of axis labels: chart-min-lg's at 40rem
// and above, chart-min-md's at 26rem and above, and the rest always, down to
// a third of a laptop's column or a 320px phone's.
var widths = struct{ lg, md, sm float64 }{640, 416, 272}

// SVG draws c.
func (c LineChart) SVG() template.HTML {
	height := c.Height
	if height <= 0 {
		height = defaultHeight
	}
	plotBottom := float64(height - axisHeight)
	series := c.Series
	if len(series) > maxSeries {
		series = series[:maxSeries]
	}

	// The time range is every point's, so a gap at either end still shows;
	// the value range starts at YMin and reaches the highest value or
	// threshold.
	var tMin, tMax time.Time
	vMax := math.Inf(-1)
	for _, s := range series {
		for _, p := range s.Points {
			if tMin.IsZero() || p.T.Before(tMin) {
				tMin = p.T
			}
			if tMax.IsZero() || p.T.After(tMax) {
				tMax = p.T
			}
			if !math.IsNaN(p.V) && !math.IsInf(p.V, 0) {
				vMax = max(vMax, p.V)
			}
		}
	}
	for _, th := range c.Thresholds {
		vMax = max(vMax, th.Value)
	}
	if math.IsInf(vMax, -1) || vMax <= c.YMin {
		vMax = c.YMin + 1
	}
	yTicks, yStep := Ticks(c.YMin, vMax, 5)
	yLo, yHi := yTicks[0], yTicks[len(yTicks)-1]

	span := tMax.Sub(tMin)
	xPct := func(t time.Time) float64 {
		if span <= 0 {
			return 50
		}
		return float64(t.Sub(tMin)) / float64(span) * 100
	}
	yPx := func(v float64) float64 {
		return plotTop + (yHi-v)/(yHi-yLo)*(plotBottom-plotTop)
	}

	var b strings.Builder
	b.WriteString(`<div class="chart chart-line">`)
	if len(series) > 1 {
		b.WriteString(`<ul class="chart-legend" role="list" aria-hidden="true">`)
		for i, s := range series {
			fmt.Fprintf(&b, `<li class="chart-series-%d"><span class="chart-key"></span>%s</li>`, i+1, esc(s.Name))
		}
		b.WriteString(`</ul>`)
	}
	fmt.Fprintf(&b, `<svg class="chart-svg" role="img" height="%d">`, height)
	fmt.Fprintf(&b, `<title>%s</title><desc>%s</desc>`, esc(c.Title), esc(c.describe(series, tMin, tMax)))

	if c.Bands && len(c.Thresholds) > 0 {
		edges := []float64{yLo}
		for _, th := range c.Thresholds {
			edges = append(edges, min(max(th.Value, yLo), yHi))
		}
		edges = append(edges, yHi)
		for i := 1; i < len(edges); i++ {
			fmt.Fprintf(&b, `<rect class="chart-band chart-band-%d" x="0" y="%s" width="100%%" height="%s"/>`,
				i, num(yPx(edges[i])), num(yPx(edges[i-1])-yPx(edges[i])))
		}
	}

	// Gridlines at the value ticks, thresholds and markers, under the
	// lines.
	b.WriteString(`<g class="chart-grid">`)
	for _, v := range yTicks {
		fmt.Fprintf(&b, `<line x1="0" x2="100%%" y1="%s" y2="%s"/>`, num(yPx(v)), num(yPx(v)))
	}
	b.WriteString(`</g>`)
	var thresholds []Threshold
	for _, th := range c.Thresholds {
		if th.Value >= yLo && th.Value <= yHi {
			thresholds = append(thresholds, th)
			y := num(yPx(th.Value))
			fmt.Fprintf(&b, `<line class="chart-threshold" x1="0" x2="100%%" y1="%s" y2="%s"/>`, y, y)
		}
	}
	var markers []Marker
	if span > 0 {
		for _, m := range c.Markers {
			if !m.T.Before(tMin) && !m.T.After(tMax) {
				markers = append(markers, m)
				x := num(xPct(m.T))
				fmt.Fprintf(&b, `<line class="chart-marker" x1="%s%%" x2="%s%%" y1="%d" y2="%s"/>`, x, x, plotTop-4, num(plotBottom))
			}
		}
	}

	// The lines, in an inner drawing stretched to the plot, so a path's
	// coordinates can be numbers: 0 to 1000 across, pixels down.
	fmt.Fprintf(&b, `<svg class="chart-plot" x="0" y="0" width="100%%" height="%d" viewBox="0 0 1000 %d" preserveAspectRatio="none">`, height, height)
	for i, s := range series {
		d := path(s.Points, func(p Point) (float64, float64) { return xPct(p.T) * 10, yPx(p.V) })
		if d != "" {
			fmt.Fprintf(&b, `<path class="chart-series-%d" d="%s"/>`, i+1, d)
		}
	}
	b.WriteString(`</svg>`)

	// The labels, over the lines: each value tick's above its gridline at
	// the left, the top one with the unit; the time axis' below the plot,
	// fewer on narrower charts; and the markers' at the top.
	b.WriteString(`<g class="chart-labels">`)
	for i, v := range yTicks {
		label := formatTick(v, yStep)
		if i == len(yTicks)-1 {
			label = withUnit(label, c.Unit)
		}
		fmt.Fprintf(&b, `<text x="0" y="%s">%s</text>`, num(yPx(v)-4), esc(label))
	}
	if span > 0 {
		ticks, step := timeTicks(tMin, tMax)
		labels := make([]string, len(ticks))
		pcts := make([]float64, len(ticks))
		for i, t := range ticks {
			labels[i] = timeLabel(t, step)
			pcts[i] = xPct(t)
		}
		classes := thin(labels, pcts, lineGutter)
		for i, label := range labels {
			if classes[i] == "-" {
				continue
			}
			// A label at the left edge starts there, so it isn't cut off;
			// the gutter leaves room for one at the right edge.
			textAnchor := "middle"
			if pcts[i] < 1 {
				textAnchor = "start"
			}
			fmt.Fprintf(&b, `<text x="%s%%" y="%d" text-anchor="%s"%s>%s</text>`, num(pcts[i]), height-6, textAnchor, classAttr(classes[i]), esc(label))
		}
	}
	for _, m := range markers {
		x := xPct(m.T)
		fmt.Fprintf(&b, `<text class="chart-marker" x="%s%%" y="10"%s>%s</text>`, num(x), anchor(x), esc(m.Label))
	}
	b.WriteString(`</g>`)

	// At the right, each threshold's label and each line's latest value,
	// at its end, moved apart so none overlap.
	type label struct {
		series int // 0 for a threshold
		x, y   float64
		labelY float64
		text   string
	}
	var labels []label
	for _, th := range thresholds {
		labels = append(labels, label{x: 100, y: yPx(th.Value), text: th.Label})
	}
	for i, s := range series {
		if p, ok := latest(s.Points); ok {
			labels = append(labels, label{series: i + 1, x: xPct(p.T), y: yPx(p.V), text: withUnit(formatValue(p.V), c.Unit)})
		}
	}
	slices.SortStableFunc(labels, func(a, b label) int { return cmp.Compare(a.y, b.y) })
	ys := make([]float64, len(labels))
	for i, l := range labels {
		ys[i] = l.y
	}
	spread(ys, endLabelGap, plotTop, plotBottom)
	for i := range labels {
		labels[i].labelY = ys[i]
	}
	for _, l := range labels {
		if l.series == 0 {
			fmt.Fprintf(&b, `<text class="chart-threshold" x="100%%" y="%s" dx="7" dy="0.35em">%s</text>`, num(l.labelY), esc(l.text))
			continue
		}
		fmt.Fprintf(&b, `<g class="chart-end chart-series-%d"><circle cx="%s%%" cy="%s" r="3"/><text x="%s%%" y="%s" dx="7" dy="0.35em">%s</text></g>`,
			l.series, num(l.x), num(l.y), num(l.x), num(l.labelY), esc(l.text))
	}

	b.WriteString(`</svg></div>`)
	return template.HTML(b.String())
}

// describe says in a sentence what c shows, for screen readers: each
// series' latest value and range, the period, and the thresholds and
// markers.
func (c LineChart) describe(series []Series, tMin, tMax time.Time) string {
	var parts []string
	for _, s := range series {
		p, ok := latest(s.Points)
		if !ok {
			parts = append(parts, fmt.Sprintf("%s has no data", s.Name))
			continue
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, q := range s.Points {
			if !math.IsNaN(q.V) {
				lo, hi = min(lo, q.V), max(hi, q.V)
			}
		}
		parts = append(parts, fmt.Sprintf("%s is %s, ranging from %s to %s",
			s.Name, withUnit(formatValue(p.V), c.Unit), withUnit(formatValue(lo), c.Unit), withUnit(formatValue(hi), c.Unit)))
	}
	if len(parts) == 0 {
		return "No data."
	}
	d := "Latest, " + strings.Join(parts, "; ") + "."
	if !tMin.IsZero() {
		d += fmt.Sprintf(" From %s to %s UTC.", tMin.UTC().Format("Jan 2 15:04"), tMax.UTC().Format("Jan 2 15:04"))
	}
	if len(c.Thresholds) > 0 {
		var ts []string
		for _, th := range c.Thresholds {
			ts = append(ts, fmt.Sprintf("%s at %s", th.Label, withUnit(formatValue(th.Value), c.Unit)))
		}
		d += " Thresholds: " + strings.Join(ts, ", ") + "."
	}
	if len(c.Markers) > 0 {
		var ms []string
		for _, m := range c.Markers {
			ms = append(ms, fmt.Sprintf("%s at %s", m.Label, m.T.UTC().Format("Jan 2 15:04")))
		}
		d += " Marked: " + strings.Join(ms, ", ") + "."
	}
	return d
}

// SVG draws h.
func (h Histogram) SVG() template.HTML {
	height := h.Height
	if height <= 0 {
		height = defaultHeight
	}
	plotBottom := float64(height - axisHeight)
	n := len(h.Counts)

	most := 0
	for _, c := range h.Counts {
		most = max(most, c)
	}
	yTicks, yStep := Ticks(0, float64(max(most, 1)), 5)
	if yStep < 1 {
		yTicks, yStep = Ticks(0, max(float64(most), 4), 5)
	}
	yHi := yTicks[len(yTicks)-1]
	yPx := func(v float64) float64 { return plotTop + (yHi-v)/yHi*(plotBottom-plotTop) }

	var b strings.Builder
	b.WriteString(`<div class="chart chart-histogram">`)
	fmt.Fprintf(&b, `<svg class="chart-svg" role="img" height="%d">`, height)
	fmt.Fprintf(&b, `<title>%s</title><desc>%s</desc>`, esc(h.Title), esc(h.describe()))

	b.WriteString(`<g class="chart-grid">`)
	for _, v := range yTicks {
		fmt.Fprintf(&b, `<line x1="0" x2="100%%" y1="%s" y2="%s"/>`, num(yPx(v)), num(yPx(v)))
	}
	b.WriteString(`</g><g class="chart-labels">`)
	for _, v := range yTicks {
		fmt.Fprintf(&b, `<text x="0" y="%s">%s</text>`, num(yPx(v)-4), esc(formatTick(v, yStep)))
	}
	if h.Unit != "" {
		fmt.Fprintf(&b, `<text x="100%%" y="10" text-anchor="end">%s</text>`, esc("upper bounds in "+h.Unit))
	}
	b.WriteString(`</g>`)

	if n > 0 {
		slot := 100 / float64(n)
		b.WriteString(`<g class="chart-bars chart-series-1">`)
		for i, c := range h.Counts {
			if c <= 0 {
				continue
			}
			y := yPx(float64(c))
			fmt.Fprintf(&b, `<rect x="%s%%" y="%s" width="%s%%" height="%s"/>`,
				num(float64(i)*slot+slot*0.08), num(y), num(slot*0.84), num(plotBottom-y))
		}
		b.WriteString(`</g>`)

		labels := make([]string, n)
		pcts := make([]float64, n)
		for i := range n {
			labels[i] = bucketLabel(h.Bounds, i)
			pcts[i] = (float64(i) + 0.5) * slot
		}
		classes := thin(labels, pcts, 0)
		b.WriteString(`<g class="chart-labels chart-x">`)
		for i, label := range labels {
			if classes[i] == "-" {
				continue
			}
			fmt.Fprintf(&b, `<text x="%s%%" y="%d" text-anchor="middle"%s>%s</text>`, num(pcts[i]), height-6, classAttr(classes[i]), esc(label))
		}
		b.WriteString(`</g>`)
	}

	b.WriteString(`</svg></div>`)
	return template.HTML(b.String())
}

// describe says in a sentence what h shows, for screen readers: how many
// values, their median and 95th percentile, and the fullest bucket.
func (h Histogram) describe() string {
	total, fullest := 0, 0
	for i, c := range h.Counts {
		total += c
		if c > h.Counts[fullest] {
			fullest = i
		}
	}
	if total == 0 {
		return "No values."
	}
	values := "values"
	if total == 1 {
		values = "value"
	}
	bucket := "all of them"
	switch {
	case fullest < len(h.Bounds):
		bucket = "in the bucket up to " + withUnit(formatValue(h.Bounds[fullest]), h.Unit)
	case len(h.Bounds) > 0:
		bucket = "in the bucket above " + withUnit(formatValue(h.Bounds[len(h.Bounds)-1]), h.Unit)
	}
	return fmt.Sprintf("%s %s; median about %s, 95th percentile about %s; the most, %s, %s.",
		formatValue(float64(total)), values,
		withUnit(formatValue(Percentile(h.Bounds, h.Counts, 0.5)), h.Unit),
		withUnit(formatValue(Percentile(h.Bounds, h.Counts, 0.95)), h.Unit),
		formatValue(float64(h.Counts[fullest])), bucket)
}

// Percentile returns the value at quantile q (0 to 1) from histogram
// buckets, interpolating within the bucket; the last (open) bucket returns
// its lower bound. bounds are the buckets' upper bounds, and counts has one
// more element than bounds; the first bucket starts at 0. It returns NaN
// when the counts are empty.
func Percentile(bounds []float64, counts []int, q float64) float64 {
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return math.NaN()
	}
	rank := min(max(q, 0), 1) * float64(total)
	cum := 0.0
	for i, c := range counts {
		if c <= 0 {
			continue
		}
		if cum+float64(c) >= rank {
			lo := 0.0
			if i > 0 && i-1 < len(bounds) {
				lo = bounds[i-1]
			}
			if i >= len(bounds) {
				return lo
			}
			return lo + (bounds[i]-lo)*(rank-cum)/float64(c)
		}
		cum += float64(c)
	}
	return math.NaN() // unreachable: rank ≤ total
}

// Ticks returns round values from at most lo up to at least hi, about n of
// them, and the step between them: 1, 2, 2.5 or 5 times a power of ten.
func Ticks(lo, hi float64, n int) ([]float64, float64) {
	if hi <= lo {
		hi = lo + 1
	}
	raw := (hi - lo) / float64(max(n-1, 1))
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5} {
		if m*mag >= raw*(1-1e-9) {
			step = m * mag
			break
		}
	}
	first := math.Floor(lo/step+1e-9) * step
	var ticks []float64
	for i := 0; ; i++ {
		v := first + float64(i)*step
		// Snap away float error, e.g. 0.30000000000000004, rounding to a
		// decimal place past the step's.
		p := math.Pow(10, max(0, math.Ceil(-math.Log10(step)))+1)
		v = math.Round(v*p) / p
		ticks = append(ticks, v)
		if v >= hi-step*1e-9 {
			break
		}
	}
	return ticks, step
}

// timeSteps are the time axis' tick intervals, from the finest.
var timeSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 28 * 24 * time.Hour,
}

// timeTicks returns the times between lo and hi at the finest of timeSteps
// that gives at most 6 of them, on multiples of the step in UTC, and the step.
func timeTicks(lo, hi time.Time) ([]time.Time, time.Duration) {
	step := timeSteps[len(timeSteps)-1]
	for _, s := range timeSteps {
		if hi.Sub(lo)/s <= 6 {
			step = s
			break
		}
	}
	var ticks []time.Time
	for t := lo.UTC().Truncate(step); !t.After(hi); t = t.Add(step) {
		if !t.Before(lo) {
			ticks = append(ticks, t)
		}
	}
	return ticks, step
}

// timeLabel is t's label on an axis whose ticks are step apart, in UTC: the
// date for whole days and midnights, the time otherwise.
func timeLabel(t time.Time, step time.Duration) string {
	t = t.UTC()
	if step >= 24*time.Hour || (t.Hour() == 0 && t.Minute() == 0) {
		return t.Format("Jan 2")
	}
	return t.Format("15:04")
}

// thin returns, for each axis label at pcts (percentages of the drawing's
// width), the class that keeps labels from overlapping at each of widths:
// "" for a label shown at every width, "chart-min-md" or "chart-min-lg" for
// one shown only on wider charts, and "-" for one never drawn. Every kth
// label is kept, k growing as the chart narrows. gutter is the drawing's
// margin within its container, in pixels.
func thin(labels []string, pcts []float64, gutter float64) []string {
	classes := make([]string, len(labels))
	if len(labels) < 2 {
		return classes
	}
	longest := 0
	for _, l := range labels {
		longest = max(longest, len([]rune(l)))
	}
	need := float64(longest)*charWidth + labelSpacing
	gap := math.Inf(1)
	for i := 1; i < len(pcts); i++ {
		gap = min(gap, pcts[i]-pcts[i-1])
	}
	stride := func(container float64, from int) int {
		k := from
		for gap/100*(container-gutter)*float64(k) < need {
			k += from
		}
		return k
	}
	lg := stride(widths.lg, 1)
	md := stride(widths.md, lg)
	sm := stride(widths.sm, md)
	for i := range labels {
		switch {
		case i%lg != 0:
			classes[i] = "-"
		case i%md != 0:
			classes[i] = "chart-min-lg"
		case i%sm != 0:
			classes[i] = "chart-min-md"
		}
	}
	return classes
}

// spread moves the sorted positions ys apart so that each is at least gap
// below the one before, keeping them between lo and hi where they fit.
func spread(ys []float64, gap, lo, hi float64) {
	for i := range ys {
		if i > 0 {
			ys[i] = max(ys[i], ys[i-1]+gap)
		}
	}
	for i := len(ys) - 1; i >= 0; i-- {
		limit := hi
		if i < len(ys)-1 {
			limit = ys[i+1] - gap
		}
		ys[i] = max(min(ys[i], limit), lo)
	}
}

// path returns the SVG path through points, starting a new subpath after
// each gap (a NaN value), so a gap leaves the line broken. A point with gaps
// on both sides is a zero-length subpath, drawn as a dot by its round cap.
func path(points []Point, xy func(Point) (float64, float64)) string {
	var subpaths []string
	var cur []string
	flush := func() {
		switch len(cur) {
		case 0:
		case 1:
			subpaths = append(subpaths, "M"+cur[0]+"h0")
		default:
			subpaths = append(subpaths, "M"+strings.Join(cur, "L"))
		}
		cur = nil
	}
	for _, p := range points {
		if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
			flush()
			continue
		}
		x, y := xy(p)
		cur = append(cur, num(x)+" "+num(y))
	}
	flush()
	return strings.Join(subpaths, " ")
}

// latest returns the last point in points with a value.
func latest(points []Point) (Point, bool) {
	for i := len(points) - 1; i >= 0; i-- {
		if !math.IsNaN(points[i].V) && !math.IsInf(points[i].V, 0) {
			return points[i], true
		}
	}
	return Point{}, false
}

// bucketLabel is bucket i's upper bound, compact ("1.5k"), or the last
// bound with a "+" for the open bucket above it.
func bucketLabel(bounds []float64, i int) string {
	if len(bounds) == 0 {
		return "all"
	}
	if i >= len(bounds) {
		return compact(bounds[len(bounds)-1]) + "+"
	}
	return compact(bounds[i])
}

// compact formats v with a "k" for thousands: 1500 is "1.5k".
func compact(v float64) string {
	if math.Abs(v) >= 1000 {
		return trimZeros(strconv.FormatFloat(v/1000, 'f', 1, 64)) + "k"
	}
	return formatValue(v)
}

// formatTick formats a tick on an axis whose ticks are step apart, with as
// many decimals as the step needs.
func formatTick(v, step float64) string {
	decimals := 0
	if step < 1 {
		decimals = int(math.Ceil(-math.Log10(step) - 1e-9))
		if step*math.Pow(10, float64(decimals)) != math.Round(step*math.Pow(10, float64(decimals))) {
			decimals++
		}
	}
	return group(strconv.FormatFloat(v, 'f', decimals, 64))
}

// formatValue formats a value to about three significant figures, with
// thousands separators.
func formatValue(v float64) string {
	if math.IsNaN(v) {
		return "no data"
	}
	a := math.Abs(v)
	decimals := 0
	switch {
	case a == 0 || a >= 100:
	case a >= 10:
		decimals = 1
	case a >= 1:
		decimals = 2
	default:
		decimals = 3
	}
	return group(trimZeros(strconv.FormatFloat(v, 'f', decimals, 64)))
}

// trimZeros removes trailing zeros after a decimal point, and the point.
func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	return strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
}

// group puts thousands separators in a formatted number's integer part.
func group(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if hasFrac {
		whole += "." + frac
	}
	return sign + whole
}

// withUnit adds unit to a formatted value: "12%", "120 ms".
func withUnit(s, unit string) string {
	switch unit {
	case "":
		return s
	case "%":
		return s + "%"
	}
	return s + " " + unit
}

// anchor keeps a label at x (a percentage) inside the drawing: centered,
// or starting or ending at x near an edge.
func anchor(x float64) string {
	switch {
	case x < 12:
		return ` text-anchor="start"`
	case x > 88:
		return ` text-anchor="end"`
	}
	return ` text-anchor="middle"`
}

func classAttr(class string) string {
	if class == "" {
		return ""
	}
	return ` class="` + class + `"`
}

// num formats a coordinate with at most two decimals.
func num(v float64) string {
	return trimZeros(strconv.FormatFloat(v, 'f', 2, 64))
}

// esc escapes text from data for the SVG's markup.
func esc(s string) string {
	return template.HTMLEscapeString(s)
}
