package psql

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// webPerfViews records n views received on day, spread over its hours,
// with every metric spread from good to poor, on two devices, browsers and
// routes, half of them installed and a third from another site.
func webPerfViews(t *testing.T, repo *RUMRepository, day time.Time, n int) {
	t.Helper()
	devices := []string{"phone", "desktop"}
	browsers := []string{"chrome", "safari"}
	routes := []string{"/", "/articles/{id}"}
	for i := range n {
		f := float64(i) / float64(n) // 0 to 1
		// Skewed toward small values, with a long tail, as real timings are.
		at := func(lo, hi float64) *float64 {
			v := lo + (hi-lo)*f*f*f
			return &v
		}
		v := rss.RUMView{
			ReceivedAt:   day.Add(time.Duration(i%24) * time.Hour).Add(time.Duration(i) * time.Second),
			Route:        routes[i%2],
			NavType:      "navigate",
			Device:       devices[(i/2)%2],
			Browser:      browsers[(i/3)%2],
			BrowserMajor: 140,
			Installed:    i%2 == 0,
			Protocol:     []string{"h2", "h3"}[i%2],
			TTFBMs:       at(40, 3000),
			FCPMs:        at(300, 5000),
			LCPMs:        at(400, 9000),
			CLS:          at(0, 0.6),
		}
		if v.Browser != "safari" || i%5 == 0 {
			v.INPMs = at(16, 900)
		}
		if i%3 == 0 {
			v.ReferrerHost = "news.example"
		}
		recordViews(t, repo, v)
	}
}

func TestAdminWebPerfRawAndRollupsAgree(t *testing.T) {
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	webPerfViews(t, repo, day, 600)
	if err := repo.RollUp(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	q := rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Step: 24 * time.Hour}
	raw, err := perf.WebPerf(ctx, q)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	q.Rollups = true
	rolled, err := perf.WebPerf(ctx, q)
	if err != nil {
		t.Fatalf("rollups: %v", err)
	}

	if raw.Total.Views != 600 || rolled.Total.Views != 600 {
		t.Errorf("views: raw %d, rollups %d, want 600", raw.Total.Views, rolled.Total.Views)
	}
	compareStats(t, "total", raw.Total.Metrics, rolled.Total.Metrics, 5)
	for _, dim := range []struct {
		name        string
		raw, rolled []rss.RUMGroup
	}{
		{"device", raw.Devices, rolled.Devices},
		{"browser", raw.Browsers, rolled.Browsers},
		{"route", raw.Routes, rolled.Routes},
	} {
		if len(dim.raw) != 2 || len(dim.rolled) != 2 {
			t.Fatalf("%s groups: raw %d, rollups %d, want 2", dim.name, len(dim.raw), len(dim.rolled))
		}
		for i := range dim.raw {
			r, o := dim.raw[i], dim.rolled[i]
			if r.Key != o.Key || r.Views != o.Views {
				t.Errorf("%s %d: raw %s with %d views, rollups %s with %d", dim.name, i, r.Key, r.Views, o.Key, o.Views)
			}
			compareStats(t, dim.name+" "+r.Key, r.Metrics, o.Metrics, 3)
		}
	}
	if len(raw.Series) != 1 || len(rolled.Series) != 1 || !raw.Series[0].T.Equal(day) || !rolled.Series[0].T.Equal(day) {
		t.Fatalf("series: raw %+v, rollups %+v, want one point at %v", raw.Series, rolled.Series, day)
	}
	compareStats(t, "series", raw.Series[0].Metrics, rolled.Series[0].Metrics, 5)

	// Only the raw views know where they came from and how.
	if len(raw.Installed) != 2 || raw.Installed[0].Key != "false" || raw.Installed[1].Views != 300 {
		t.Errorf("raw installed = %+v, want false and true with 300 each", raw.Installed)
	}
	if len(raw.Referrers) != 1 || raw.Referrers[0] != (rss.RUMCount{Key: "news.example", Views: 200}) {
		t.Errorf("raw referrers = %+v, want news.example's 200 without the views from no site", raw.Referrers)
	}
	if len(raw.Protocols) != 2 || raw.Protocols[0].Views != 300 {
		t.Errorf("raw protocols = %+v, want h2 and h3 with 300 each", raw.Protocols)
	}
	if rolled.Installed != nil || rolled.Referrers != nil || rolled.Protocols != nil {
		t.Errorf("rollups have installed %v, referrers %v, protocols %v; want none", rolled.Installed, rolled.Referrers, rolled.Protocols)
	}
	routeViews := map[string]int{}
	for _, v := range rolled.RouteViews {
		routeViews[v.Route] += v.Views
	}
	for _, v := range raw.RouteViews {
		routeViews[v.Route] -= v.Views
	}
	for route, diff := range routeViews {
		if diff != 0 {
			t.Errorf("route %s: rollups' views differ from raw by %d", route, diff)
		}
	}
}

// compareStats checks that raw and rolled both have n metrics, with the
// same views and ratings, and percentiles in the same bucket.
func compareStats(t *testing.T, name string, raw, rolled map[string]rss.RUMStat, n int) {
	t.Helper()
	if len(raw) != n || len(rolled) != n {
		t.Fatalf("%s: raw has %d metrics, rollups %d, want %d", name, len(raw), len(rolled), n)
	}
	for metric, r := range raw {
		o := rolled[metric]
		if r.Views != o.Views || r.Good != o.Good || r.NeedsImprovement != o.NeedsImprovement || r.Poor != o.Poor {
			t.Errorf("%s %s: raw %d views (%d/%d/%d), rollups %d (%d/%d/%d)", name, metric,
				r.Views, r.Good, r.NeedsImprovement, r.Poor, o.Views, o.Good, o.NeedsImprovement, o.Poor)
		}
		bounds := metrics.RUMBounds[metric]
		for _, p := range []struct {
			q           string
			raw, rolled float64
		}{{"p75", r.P75, o.P75}, {"p90", r.P90, o.P90}, {"p95", r.P95, o.P95}} {
			lo, hi := bucketOf(bounds, p.raw)
			if p.rolled < lo-1e-6 || p.rolled > hi+1e-6 {
				t.Errorf("%s %s %s: rollups' %v is outside raw %v's bucket [%v, %v]", name, metric, p.q, p.rolled, p.raw, lo, hi)
			}
		}
	}
}

// bucketOf returns the bounds of v's bucket: (lo, hi], or the last bound
// twice above it, where Percentile returns the lower bound.
func bucketOf(bounds []float64, v float64) (float64, float64) {
	lo := 0.0
	for _, b := range bounds {
		if float32(v) <= float32(b) {
			return lo, b
		}
		lo = b
	}
	return lo, math.Max(lo, v)
}

func TestAdminWebPerfDeviceAndWindow(t *testing.T) {
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	webPerfViews(t, repo, day, 40)
	webPerfViews(t, repo, day.AddDate(0, 0, 1), 8) // outside the window

	wp, err := perf.WebPerf(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Device: "phone", Step: 6 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if wp.Total.Views != 20 {
		t.Errorf("phone views = %d, want 20 of the day's 40", wp.Total.Views)
	}
	if len(wp.Devices) != 1 || wp.Devices[0].Key != "phone" {
		t.Errorf("devices = %+v, want phone only", wp.Devices)
	}
	if len(wp.Series) != 4 {
		t.Errorf("series has %d points, want 4 six-hour steps", len(wp.Series))
	}
	for i, p := range wp.Series {
		if want := day.Add(time.Duration(i) * 6 * time.Hour); !p.T.Equal(want) {
			t.Errorf("point %d at %v, want %v", i, p.T, want)
		}
	}

	empty, err := perf.WebPerf(ctx, rss.WebPerfQuery{From: day.AddDate(0, 0, -7), To: day, Step: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total.Views != 0 || len(empty.Total.Metrics) != 0 || empty.Series != nil || empty.Routes != nil {
		t.Errorf("an empty window = %+v, want nothing", empty)
	}
}
