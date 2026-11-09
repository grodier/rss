package admin

// The charts' sample page, for #571's design checkpoint only: it's on the
// branch, never merged, like #562's sample page.

import (
	"math"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
)

type sampleChartsPage struct {
	Nav       navData
	Variant   string
	Latency   chart.LineChart
	LCP       chart.LineChart
	Memory    chart.LineChart
	Histogram chart.Histogram
}

func (s *Server) sampleChartsHandler(w http.ResponseWriter, r *http.Request) {
	variant := r.URL.Query().Get("variant")
	end := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)

	// Latency: p50 and p95 every 15 minutes for a day, with an hour and a
	// half without data, and a deploy.
	var p50, p95 []chart.Point
	for i := range 97 {
		t := end.Add(time.Duration(i-96) * 15 * time.Minute)
		v50 := 38 + 9*math.Sin(float64(i)/7) + 4*math.Sin(float64(i)*1.7)
		v95 := 120 + 35*math.Sin(float64(i)/9+1) + 18*math.Sin(float64(i)*2.3)
		if i > 70 {
			v50 -= 8
			v95 -= 30
		}
		if i >= 40 && i < 46 {
			v50, v95 = math.NaN(), math.NaN()
		}
		p50 = append(p50, chart.Point{T: t, V: v50})
		p95 = append(p95, chart.Point{T: t, V: v95})
	}

	// LCP's 75th percentile, daily for 30 days.
	var lcp []chart.Point
	for i := range 30 {
		t := end.Truncate(24*time.Hour).AddDate(0, 0, i-29)
		lcp = append(lcp, chart.Point{T: t, V: 2300 + 700*math.Sin(float64(i)/4) + 260*math.Sin(float64(i)*1.9)})
	}

	// Memory: three series, every 5 minutes for 6 hours.
	var heap, stack, rss []chart.Point
	for i := range 73 {
		t := end.Add(time.Duration(i-72) * 5 * time.Minute)
		h := 42 + 6*math.Sin(float64(i)/5) + float64(i%12)*0.6
		heap = append(heap, chart.Point{T: t, V: h})
		stack = append(stack, chart.Point{T: t, V: 6 + math.Sin(float64(i)/3)})
		rss = append(rss, chart.Point{T: t, V: h + 31 + 2*math.Sin(float64(i)/11)})
	}

	data := sampleChartsPage{
		Nav:     s.nav(r, "overview"),
		Variant: variant,
		Latency: chart.LineChart{
			Title:   "Response time",
			Unit:    "ms",
			Series:  []chart.Series{{Name: "p50", Points: p50}, {Name: "p95", Points: p95}},
			Markers: []chart.Marker{{T: end.Add(-7 * time.Hour), Label: "Deploy 4f2a1c9"}},
		},
		LCP: chart.LineChart{
			Title:      "Largest Contentful Paint, 75th percentile",
			Unit:       "ms",
			Series:     []chart.Series{{Name: "LCP p75", Points: lcp}},
			Thresholds: []chart.Threshold{{Value: 2500, Label: "Good"}, {Value: 4000, Label: "Poor"}},
			Bands:      variant == "bands",
		},
		Memory: chart.LineChart{
			Title:  "Memory",
			Unit:   "MB",
			Series: []chart.Series{{Name: "Heap", Points: heap}, {Name: "Stacks", Points: stack}, {Name: "Resident", Points: rss}},
		},
		Histogram: chart.Histogram{
			Title:  "Largest Contentful Paint, last 7 days",
			Unit:   "ms",
			Bounds: []float64{50, 75, 100, 150, 200, 300, 500, 800, 1000, 1500, 1800, 2500, 3000, 4000, 6000, 10000, 20000},
			Counts: []int{0, 2, 4, 9, 18, 41, 96, 188, 240, 410, 330, 520, 270, 190, 88, 31, 9, 3},
		},
	}
	if err := s.renderHTML(w, http.StatusOK, "sample_charts.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
