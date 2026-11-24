package psql

import (
	"context"
	"fmt"
	"math"
	"reflect"
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
		// What only a route's page reads.
		v.ServerMs = at(2, 400)
		v.LCPElement = []string{"img", "h1", "p"}[i%3]
		if v.INPMs != nil {
			v.INPTarget = []string{"button", "data-save"}[(i/4)%2]
		}
		if i%10 == 0 {
			v.NavType = "reload"
		}
		size := int64(i%4) * 9000 // a quarter from the cache
		v.TransferBytes = &size
		recordViews(t, repo, v)
	}
}

func TestAdminWebPerfRawAndRollupsAgree(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

	for _, rollups := range []bool{false, true} {
		empty, err := perf.WebPerf(ctx, rss.WebPerfQuery{From: day.AddDate(0, 0, -7), To: day, Rollups: rollups, Step: 24 * time.Hour})
		if err != nil {
			t.Fatalf("an empty window (rollups %v): %v", rollups, err)
		}
		if empty.Total.Views != 0 || len(empty.Total.Metrics) != 0 || empty.Series != nil || empty.Routes != nil {
			t.Errorf("an empty window (rollups %v) = %+v, want nothing", rollups, empty)
		}
	}
}

// A value on a threshold is in the better rating, from raw views as from
// rollups, though the columns are real and the thresholds aren't.
func TestAdminWebPerfThresholdEdges(t *testing.T) {
	t.Parallel()
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	for _, cls := range []float64{0.1, 0.25, 0.2501} {
		v := rumView(day.Add(time.Hour), "/", nil)
		v.CLS = &cls
		recordViews(t, repo, v)
	}
	if err := repo.RollUp(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	for _, rollups := range []bool{false, true} {
		wp, err := perf.WebPerf(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Rollups: rollups, Step: 24 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		s := wp.Total.Metrics["cls"]
		if s.Good != 1 || s.NeedsImprovement != 1 || s.Poor != 1 {
			t.Errorf("rollups %v: CLS 0.1, 0.25 and 0.2501 rated %d good, %d needing improvement, %d poor; want 1 each",
				rollups, s.Good, s.NeedsImprovement, s.Poor)
		}
	}
}

// The Back and Forward views: how many were restored, and the reasons the
// rest weren't, most first, for the window and device only.
func TestAdminWebPerfBackForward(t *testing.T) {
	t.Parallel()
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	view := func(nav, device string, at time.Time, reasons ...string) rss.RUMView {
		v := rumView(at, "/", nil)
		v.NavType, v.Device, v.NotRestored = nav, device, reasons
		return v
	}
	in := day.Add(time.Hour)
	recordViews(t, repo,
		view("restore", "desktop", in),
		view("restore", "desktop", in),
		view("restore", "phone", in),
		view("back_forward", "desktop", in, "unload-listener", "masked"),
		view("back_forward", "desktop", in, "unload-listener"),
		view("back_forward", "phone", in, "response-cache-control-no-store"),
		view("back_forward", "desktop", in), // Safari: no reasons
		view("navigate", "desktop", in),
		view("back_forward", "desktop", day.AddDate(0, 0, 1), "cookie-disabled"), // outside the window
	)

	wp, err := perf.WebPerf(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Step: 6 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want := rss.RUMBackForward{
		Restored: 3, NotRestored: 4, Unexplained: 1,
		Reasons: []rss.RUMCount{{Key: "unload-listener", Views: 2}, {Key: "masked", Views: 1}, {Key: "response-cache-control-no-store", Views: 1}},
	}
	if !reflect.DeepEqual(wp.BackForward, want) {
		t.Errorf("back and forward = %+v, want %+v", wp.BackForward, want)
	}

	wp, err = perf.WebPerf(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Device: "phone", Step: 6 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want = rss.RUMBackForward{Restored: 1, NotRestored: 1, Reasons: []rss.RUMCount{{Key: "response-cache-control-no-store", Views: 1}}}
	if !reflect.DeepEqual(wp.BackForward, want) {
		t.Errorf("phone's back and forward = %+v, want %+v", wp.BackForward, want)
	}

	// The rollups don't keep them.
	if err := repo.RollUp(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	wp, err = perf.WebPerf(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Rollups: true, Step: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wp.BackForward, rss.RUMBackForward{}) {
		t.Errorf("rollups' back and forward = %+v, want none", wp.BackForward)
	}
}

// A route's views from rum_views and from rum_daily agree as the page's do,
// with the same buckets, and only the raw views know what was slow.
func TestAdminWebPerfRouteRawAndRollupsAgree(t *testing.T) {
	t.Parallel()
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	webPerfViews(t, repo, day, 600)
	if err := repo.RollUp(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	q := rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Step: 24 * time.Hour}
	raw, err := perf.WebPerfRoute(ctx, q, "/")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	q.Rollups = true
	rolled, err := perf.WebPerfRoute(ctx, q, "/")
	if err != nil {
		t.Fatalf("rollups: %v", err)
	}

	if raw.Total.Views != 300 || rolled.Total.Views != 300 {
		t.Errorf("views: raw %d, rollups %d, want the route's 300 of 600", raw.Total.Views, rolled.Total.Views)
	}
	compareStats(t, "total", raw.Total.Metrics, rolled.Total.Metrics, 6)
	if s := raw.Total.Metrics["server"]; s.Good+s.NeedsImprovement+s.Poor != 0 || s.Views != 300 {
		t.Errorf("raw server = %+v, want 300 views and none by rating", s)
	}
	if !reflect.DeepEqual(raw.Buckets, rolled.Buckets) {
		t.Errorf("buckets: raw %v, rollups %v", raw.Buckets, rolled.Buckets)
	}
	for metric, s := range raw.Total.Metrics {
		n := 0
		for _, c := range raw.Buckets[metric] {
			n += c
		}
		if n != s.Views {
			t.Errorf("%s's buckets hold %d views, want %d", metric, n, s.Views)
		}
	}
	for _, dim := range []struct {
		name        string
		raw, rolled []rss.RUMGroup
	}{
		{"device", raw.Devices, rolled.Devices},
		{"browser", raw.Browsers, rolled.Browsers},
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
	compareStats(t, "series", raw.Series[0].Metrics, rolled.Series[0].Metrics, 6)

	// Only the raw views know what was slow, how they came and their size.
	if len(raw.LCPElements) != 3 || len(raw.INPTargets) != 2 || len(raw.NavTypes) != 2 || raw.Transfer.Views != 300 {
		t.Errorf("raw has %d LCP elements, %d INP targets, nav types %v, transfer %+v; want 3, 2, two types and 300 sizes",
			len(raw.LCPElements), len(raw.INPTargets), raw.NavTypes, raw.Transfer)
	}
	if rolled.LCPElements != nil || rolled.INPTargets != nil || rolled.NavTypes != nil || rolled.Transfer != (rss.RUMTransfer{}) {
		t.Errorf("rollups have LCP elements %v, INP targets %v, nav types %v, transfer %+v; want none",
			rolled.LCPElements, rolled.INPTargets, rolled.NavTypes, rolled.Transfer)
	}
}

// What a route's raw views say about what was slow: the LCP elements, the
// slowest interactions' targets with the most slow ones first, the
// navigation types and the transfer sizes, of the route, window and device
// only.
func TestAdminWebPerfRouteDetails(t *testing.T) {
	t.Parallel()
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	in := day.Add(time.Hour)
	f := func(v float64) *float64 { return &v }
	size := func(n int64) *int64 { return &n }
	view := func(nav, element string, lcp *float64, target string, inp *float64, transfer *int64) rss.RUMView {
		v := rumView(in, "/feeds/{id}", lcp)
		v.NavType, v.LCPElement, v.INPTarget, v.INPMs, v.TransferBytes = nav, element, target, inp, transfer
		return v
	}
	other := view("navigate", "img", f(9000), "button", f(900), size(1))
	other.Route = "/"
	phone := view("navigate", "img", f(9000), "button", f(900), size(1))
	phone.Device = "phone"
	late := view("navigate", "img", f(9000), "button", f(900), size(1))
	late.ReceivedAt = day.AddDate(0, 0, 1)
	recordViews(t, repo,
		view("navigate", "img", f(3000), "button", f(100), size(0)),
		view("navigate", "img", f(3000), "button", f(100), size(0)),
		view("reload", "img", f(1000), "button", f(100), size(1000)),
		view("navigate", "h1", f(800), "data-save", f(600), size(2000)),
		view("reload", "", f(900), "data-save", f(300), size(3000)),
		view("restore", "", nil, "", nil, nil), // restored: no LCP, no size
		other, phone, late,
	)

	wr, err := perf.WebPerfRoute(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Device: "desktop", Step: 6 * time.Hour}, "/feeds/{id}")
	if err != nil {
		t.Fatal(err)
	}
	if wr.Total.Views != 6 {
		t.Errorf("views = %d, want 6", wr.Total.Views)
	}
	keys := func(gs []rss.RUMGroup) []string {
		var out []string
		for _, g := range gs {
			out = append(out, fmt.Sprintf("%s:%d", g.Key, g.Views))
		}
		return out
	}
	if got, want := keys(wr.LCPElements), []string{"img:3", ":1", "h1:1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("LCP elements = %v, want %v", got, want)
	}
	if s := wr.LCPElements[0].Metrics["lcp"]; s.Views != 3 || s.Good != 1 || s.NeedsImprovement != 2 {
		t.Errorf("img's LCP = %+v, want 3 views, 1 good and 2 needing improvement", s)
	}
	// data-save's 2 slow interactions come before button's 3 fast ones.
	if got, want := keys(wr.INPTargets), []string{"data-save:2", "button:3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("INP targets = %v, want %v", got, want)
	}
	if s := wr.INPTargets[0].Metrics["inp"]; s.NeedsImprovement != 1 || s.Poor != 1 {
		t.Errorf("data-save's INP = %+v, want 1 needing improvement and 1 poor", s)
	}
	wantNav := []rss.RUMCount{{Key: "navigate", Views: 3}, {Key: "reload", Views: 2}, {Key: "restore", Views: 1}}
	if !reflect.DeepEqual(wr.NavTypes, wantNav) {
		t.Errorf("nav types = %v, want %v", wr.NavTypes, wantNav)
	}
	if want := (rss.RUMTransfer{Views: 5, Cached: 2, P50: 2000, P75: 2500, P95: 2900}); wr.Transfer != want {
		t.Errorf("transfer = %+v, want %+v", wr.Transfer, want)
	}
	if n := wr.Buckets["lcp"]; len(n) != len(metrics.RUMBounds["lcp"])+1 || n[7] != 1 || n[8] != 2 || n[12] != 2 {
		t.Errorf("LCP buckets = %v, want 800 in the 800 bucket, 900 and 1000 in the 1000 bucket, and two 3000s in the 3000 bucket", n)
	}

	// A route with no views has nothing, from either source.
	for _, rollups := range []bool{false, true} {
		q := rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1), Rollups: rollups, Step: 24 * time.Hour}
		empty, err := perf.WebPerfRoute(ctx, q, "/nowhere")
		if err != nil {
			t.Fatalf("an unknown route (rollups %v): %v", rollups, err)
		}
		if empty.Total.Views != 0 || len(empty.Total.Metrics) != 0 || empty.Series != nil || empty.Buckets != nil || empty.Devices != nil {
			t.Errorf("an unknown route (rollups %v) = %+v, want nothing", rollups, empty)
		}
	}
	if _, err := perf.WebPerfRoute(ctx, rss.WebPerfQuery{From: day, To: day.AddDate(0, 0, 1)}, "/"); err == nil {
		t.Error("a query with no step: no error")
	}
}

func TestAdminWebPerfHasViews(t *testing.T) {
	t.Parallel()
	repo, db := newRUMRepo(t)
	perf := NewAdminWebPerfRepository(db)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	recordViews(t, repo, rumView(at, "/feeds/{id}", nil))
	for _, tt := range []struct {
		route string
		since time.Time
		want  bool
	}{
		{"/feeds/{id}", at, true},
		{"/feeds/{id}", at.Add(time.Second), false},
		{"/healthcheck", at.Add(-time.Hour), false},
	} {
		got, err := perf.HasViews(ctx, tt.route, tt.since)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("HasViews(%s, %v) = %v, want %v", tt.route, tt.since, got, tt.want)
		}
	}
}
