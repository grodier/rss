package chart

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/*.svg")

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// points returns a point a minute apart for each value, from t0.
func points(values ...float64) []Point {
	ps := make([]Point, len(values))
	for i, v := range values {
		ps[i] = Point{T: t0.Add(time.Duration(i) * time.Minute), V: v}
	}
	return ps
}

func TestTicks(t *testing.T) {
	for _, tt := range []struct {
		lo, hi float64
		want   []float64
		step   float64
	}{
		{0, 100, []float64{0, 25, 50, 75, 100}, 25},
		{0, 7, []float64{0, 2, 4, 6, 8}, 2},
		{0, 1, []float64{0, 0.25, 0.5, 0.75, 1}, 0.25},
		{0, 0.3, []float64{0, 0.1, 0.2, 0.3}, 0.1},
		{0, 4300, []float64{0, 2000, 4000, 6000}, 2000},
		{0, 212, []float64{0, 100, 200, 300}, 100},
		{2, 9, []float64{2, 4, 6, 8, 10}, 2},
		{0, 0, []float64{0, 0.25, 0.5, 0.75, 1}, 0.25},
	} {
		got, step := Ticks(tt.lo, tt.hi, 5)
		if !slices.Equal(got, tt.want) || step != tt.step {
			t.Errorf("Ticks(%v, %v, 5) = %v, %v; want %v, %v", tt.lo, tt.hi, got, step, tt.want, tt.step)
		}
	}
}

func TestTimeTicks(t *testing.T) {
	for _, tt := range []struct {
		name   string
		span   time.Duration
		start  time.Time
		labels []string
	}{
		{"an hour", time.Hour, t0, []string{"12:00", "12:10", "12:20", "12:30", "12:40", "12:50", "13:00"}},
		{"a day, with midnight as the date", 24 * time.Hour, t0, []string{"12:00", "18:00", "Nov 10", "06:00", "12:00"}},
		{"a week, by whole days", 7 * 24 * time.Hour, t0, []string{"Nov 11", "Nov 13", "Nov 15"}},
		{"in UTC", time.Hour, t0.In(time.FixedZone("EST", -5*3600)), []string{"12:00", "12:10", "12:20", "12:30", "12:40", "12:50", "13:00"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ticks, step := timeTicks(tt.start, tt.start.Add(tt.span))
			var labels []string
			for _, tick := range ticks {
				labels = append(labels, timeLabel(tick, step))
			}
			if !slices.Equal(labels, tt.labels) {
				t.Errorf("labels = %q, want %q", labels, tt.labels)
			}
		})
	}
}

// TestThin checks that labels which wouldn't fit are drawn only on wider
// charts, keeping every kth: 18 buckets' labels are 35.6px apart at 40rem,
// 23.1px at 26rem and 15.1px at 17rem.
func TestThin(t *testing.T) {
	for _, tt := range []struct {
		label string
		want  []string // the classes, repeating
	}{
		// 36px each: every other at 40rem, then every fourth at 17rem.
		{"1.5k", []string{"", "-", "chart-min-md", "-"}},
		// 29.5px each: all at 40rem, every other below it.
		{"1.5", []string{"", "chart-min-lg"}},
		// 16.5px each: all down to 26rem, every other below it.
		{"5", []string{"", "chart-min-md"}},
	} {
		t.Run(tt.label, func(t *testing.T) {
			labels := make([]string, 18)
			pcts := make([]float64, 18)
			for i := range labels {
				labels[i] = tt.label
				pcts[i] = (float64(i) + 0.5) * 100 / 18
			}
			got := thin(labels, pcts, 0)
			for i, class := range got {
				if want := tt.want[i%len(tt.want)]; class != want {
					t.Fatalf("label %d's class = %q, want %q (all: %q)", i, class, want, got)
				}
			}
		})
	}
	if got := thin([]string{"a", "b"}, []float64{0, 100}, 0); !slices.Equal(got, []string{"", ""}) {
		t.Errorf("two labels far apart: %q, want both always shown", got)
	}
}

func TestLineChartGaps(t *testing.T) {
	nan := math.NaN()
	for _, tt := range []struct {
		name   string
		values []float64
		want   string
	}{
		{"no gap", []float64{1, 2, 3}, "M0 1L100 2L200 3"},
		{"a gap splits the line", []float64{1, 2, nan, 3, 4}, "M0 1L100 2 M300 3L400 4"},
		{"a point between gaps is a dot", []float64{1, 2, nan, 3, nan, 4}, "M0 1L100 2 M300 3h0 M500 4h0"},
		{"gaps at both ends", []float64{nan, 1, 2, nan}, "M100 1L200 2"},
		{"no values", []float64{nan, nan}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A point's minute, times 100, across, and its value down.
			got := path(points(tt.values...), func(p Point) (float64, float64) {
				return p.T.Sub(t0).Minutes() * 100, p.V
			})
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
		})
	}

	// In a chart, the gap shows as a second subpath.
	svg := string(LineChart{Title: "Gaps", Series: []Series{{Name: "a", Points: points(1, 2, nan, 3, 4)}}}.SVG())
	i := strings.Index(svg, `<path class="chart-series-1" d="`)
	if i < 0 {
		t.Fatalf("no path in:\n%s", svg)
	}
	d := svg[i:]
	d = d[:strings.Index(d, "/>")]
	if n := strings.Count(d, "M"); n != 2 {
		t.Errorf("path has %d subpaths, want 2: %s", n, d)
	}
}

func TestLineChartEscapes(t *testing.T) {
	const evil = `<script>alert("x")</script>`
	charts := map[string]Chart{
		"line": LineChart{
			Title:      evil,
			Unit:       evil,
			Series:     []Series{{Name: evil, Points: points(1, 2)}, {Name: "b", Points: points(2, 3)}},
			Markers:    []Marker{{T: t0.Add(time.Minute / 2), Label: evil}},
			Thresholds: []Threshold{{Value: 1.5, Label: evil}},
		},
		"histogram": Histogram{Title: evil, Unit: evil, Bounds: []float64{1}, Counts: []int{1, 2}},
	}
	for name, c := range charts {
		t.Run(name, func(t *testing.T) {
			svg := string(c.SVG())
			if strings.Contains(svg, "<script") {
				t.Errorf("SVG has an unescaped <script>:\n%s", svg)
			}
			if !strings.Contains(svg, "&lt;script&gt;") {
				t.Errorf("SVG lacks the escaped text:\n%s", svg)
			}
		})
	}
}

func TestLineChartMarkers(t *testing.T) {
	// 101 points a minute apart: a marker's x is its minute, in percent.
	values := make([]float64, 101)
	c := LineChart{
		Series: []Series{{Name: "a", Points: points(values...)}},
		Markers: []Marker{
			{T: t0.Add(50 * time.Minute), Label: "Deploy b"},
			{T: t0.Add(10 * time.Minute), Label: "Deploy a"},
			// Within 1.5% of the one before: drawn as one line, at the
			// newer.
			{T: t0.Add(51 * time.Minute), Label: "Deploy c"},
			{T: t0.Add(52 * time.Minute), Label: "Deploy d"},
			// Outside the chart's time range.
			{T: t0.Add(-time.Minute), Label: "Deploy before"},
		},
	}
	svg := string(c.SVG())
	for _, want := range []string{
		`<line class="chart-marker" x1="10%" x2="10%"`,
		`<line class="chart-marker" x1="52%" x2="52%"`,
		`<line class="chart-marker-hit" x1="10%" x2="10%" y1="14" y2="178"><title>Deploy a, Nov 9 12:10 UTC</title></line>`,
		"<title>Deploy b, Nov 9 12:50 UTC\nDeploy c, Nov 9 12:51 UTC\nDeploy d, Nov 9 12:52 UTC</title>",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %s:\n%s", want, svg)
		}
	}
	if n := strings.Count(svg, `<line class="chart-marker"`); n != 2 {
		t.Errorf("%d marker lines, want 2", n)
	}
	if strings.Contains(svg, "<title>Deploy before") || strings.Contains(svg, `<text class="chart-marker"`) {
		t.Errorf("SVG draws a marker outside its range, or a marker's text:\n%s", svg)
	}
	// The description still names every marker.
	if !strings.Contains(svg, "Marked: Deploy b at Nov 9 12:50, Deploy a at Nov 9 12:10, Deploy c at Nov 9 12:51, Deploy d at Nov 9 12:52") {
		t.Errorf("description doesn't list the markers:\n%s", svg)
	}
}

func TestLineChartLegendOnlyForSeveralSeries(t *testing.T) {
	one := string(LineChart{Series: []Series{{Name: "a", Points: points(1, 2)}}}.SVG())
	if strings.Contains(one, "chart-legend") {
		t.Errorf("one series has a legend:\n%s", one)
	}
	two := string(LineChart{Series: []Series{{Name: "a", Points: points(1, 2)}, {Name: "b", Points: points(2, 3)}}}.SVG())
	if !strings.Contains(two, `<ul class="chart-legend"`) {
		t.Errorf("two series have no legend:\n%s", two)
	}
}

func TestDescriptions(t *testing.T) {
	nan := math.NaN()
	for _, tt := range []struct {
		name string
		c    Chart
		want string
	}{
		{"no data", LineChart{Title: "Empty"}, "<desc>No data.</desc>"},
		{"a series without values", LineChart{Series: []Series{{Name: "a", Points: points(nan)}}}, "<desc>Latest, a has no data. From Nov 9 12:00 to Nov 9 12:00 UTC.</desc>"},
		{"no values", Histogram{Bounds: []float64{1}, Counts: []int{0, 0}}, "<desc>No values.</desc>"},
		{"the most in the open bucket", Histogram{Unit: "ms", Bounds: []float64{1000}, Counts: []int{1, 3}}, "<desc>4 values; median about 1,000 ms, 95th percentile about 1,000 ms; the most, 3, in the bucket above 1,000 ms.</desc>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if svg := string(tt.c.SVG()); !strings.Contains(svg, tt.want) {
				t.Errorf("SVG lacks %s:\n%s", tt.want, svg)
			}
		})
	}
}

// goldenCharts are drawn and checked against testdata/<name>.svg, written
// with -update and looked at in a browser.
var goldenCharts = map[string]Chart{
	"line": LineChart{
		Title: "Response time",
		Unit:  "ms",
		Series: []Series{
			{Name: "p50", Points: points(32, 41, 38, math.NaN(), math.NaN(), 35, 30, 33)},
			{Name: "p95", Points: points(120, 180, 140, math.NaN(), math.NaN(), 150, 110, 125)},
		},
		Markers:    []Marker{{T: t0.Add(5 * time.Minute), Label: "Deploy 4f2a1c9"}},
		Thresholds: []Threshold{{Value: 160, Label: "Slow"}},
	},
	"histogram": Histogram{
		Title:  "Largest Contentful Paint",
		Unit:   "ms",
		Bounds: []float64{500, 1000, 2500, 4000},
		Counts: []int{12, 40, 31, 9, 2},
	},
}

func TestGolden(t *testing.T) {
	for name, c := range goldenCharts {
		t.Run(name, func(t *testing.T) {
			// One element per line, so a change's diff is readable.
			got := strings.ReplaceAll(string(c.SVG()), "><", ">\n<") + "\n"
			path := filepath.Join("testdata", name+".svg")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("SVG differs from %s (go test -update rewrites it):\n%s", path, got)
			}
		})
	}
}
