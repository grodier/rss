package psql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// newRUMRepo returns a repository on a freshly migrated schema of its own:
// RollUp and DeleteBefore work on the whole tables, and RollUp starts after
// the newest daily row, so other tests' rows would change the outcome.
func newRUMRepo(t *testing.T) (*RUMRepository, *sql.DB) {
	t.Helper()
	db := newMigrateDB(t)
	mustMigrate(t, db)
	return NewRUMRepository(db), db
}

// rumView returns a desktop Chrome view of route received at, with lcp as
// its only metric (nil for none).
func rumView(at time.Time, route string, lcp *float64) rss.RUMView {
	return rss.RUMView{
		ReceivedAt:   at,
		Route:        route,
		NavType:      "navigate",
		Device:       "desktop",
		Browser:      "chrome",
		BrowserMajor: 140,
		LCPMs:        lcp,
	}
}

func recordViews(t *testing.T, repo *RUMRepository, views ...rss.RUMView) {
	t.Helper()
	for _, v := range views {
		if err := repo.Record(context.Background(), v); err != nil {
			t.Fatalf("Record(%+v): %v", v, err)
		}
	}
}

// viewRoutes returns the routes of every rum_views row, oldest first.
func viewRoutes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT route FROM rum_views ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			t.Fatal(err)
		}
		out = append(out, route)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type rumDailyRow struct {
	day                            string
	route, device, browser, metric string
	views                          int
	buckets                        []int64
}

// listRUMDaily returns every rum_daily row, by day, route, device, browser
// and metric.
func listRUMDaily(t *testing.T, db *sql.DB) []rumDailyRow {
	t.Helper()
	rows, err := db.Query(`SELECT to_char(day, 'YYYY-MM-DD'), route, device, browser, metric, views, buckets
		FROM rum_daily ORDER BY day, route, device, browser, metric`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []rumDailyRow
	for rows.Next() {
		var r rumDailyRow
		if err := rows.Scan(&r.day, &r.route, &r.device, &r.browser, &r.metric, &r.views, pq.Array(&r.buckets)); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// buckets returns metric's bucket counts with n views in each bucket given
// as index, count pairs.
func buckets(metric string, pairs ...int) []int64 {
	b := make([]int64, len(metrics.RUMBounds[metric])+1)
	for i := 0; i < len(pairs); i += 2 {
		b[pairs[i]] = int64(pairs[i+1])
	}
	return b
}

// showView prints v with its metrics' values rather than their addresses.
func showView(v rss.RUMView) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestRUMRecord(t *testing.T) {
	repo, db := newRUMRepo(t)

	full := rss.RUMView{
		Route:         "/feeds/{id}",
		NavType:       "reload",
		Device:        "phone",
		Browser:       "safari",
		BrowserMajor:  26,
		Installed:     true,
		Connection:    "4g",
		Protocol:      "h3",
		ReferrerHost:  "news.example.com",
		TTFBMs:        new(120.5),
		FCPMs:         new(300.0),
		LCPMs:         new(610.3),
		INPMs:         new(96.0),
		CLS:           new(0.05),
		DCLMs:         new(420.3),
		LoadMs:        new(980.0),
		ServerMs:      new(12.3),
		TransferBytes: new(int64(12345)),
		LCPElement:    "img",
		INPTarget:     "data-share-url",
	}
	minimal := rss.RUMView{Route: "/", NavType: "navigate", Device: "desktop", Browser: "other"}
	before := time.Now()
	recordViews(t, repo, full, minimal)

	rows, err := db.Query(`SELECT received_at, route, nav_type, device, browser, browser_major, installed,
		    connection, protocol, referrer_host, ttfb_ms, fcp_ms, lcp_ms, inp_ms, cls, dcl_ms, load_ms,
		    server_ms, transfer_bytes, lcp_element, inp_target
		FROM rum_views ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []rss.RUMView
	for rows.Next() {
		var v rss.RUMView
		err := rows.Scan(&v.ReceivedAt, &v.Route, &v.NavType, &v.Device, &v.Browser, &v.BrowserMajor, &v.Installed,
			&v.Connection, &v.Protocol, &v.ReferrerHost, &v.TTFBMs, &v.FCPMs, &v.LCPMs, &v.INPMs, &v.CLS, &v.DCLMs, &v.LoadMs,
			&v.ServerMs, &v.TransferBytes, &v.LCPElement, &v.INPTarget)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d views, want 2", len(got))
	}

	// REAL columns read back as the shortest decimal that is the same
	// float32, so these values come back exactly; the minimal view's
	// metrics come back NULL.
	for i, want := range []rss.RUMView{full, minimal} {
		v := got[i]
		if v.ReceivedAt.Before(before.Add(-time.Minute)) || v.ReceivedAt.After(time.Now().Add(time.Minute)) {
			t.Errorf("view %d received at %v, want about now (%v)", i, v.ReceivedAt, before)
		}
		v.ReceivedAt = time.Time{}
		if !reflect.DeepEqual(v, want) {
			t.Errorf("view %d =\n%s\nwant\n%s", i, showView(v), showView(want))
		}
	}
}

func TestRUMRollUp(t *testing.T) {
	repo, db := newRUMRepo(t)
	ctx := context.Background()
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return day.Add(d) }

	lcpBounds := metrics.RUMBounds["lcp"]
	if lcpBounds[0] != 50 || lcpBounds[len(lcpBounds)-1] != 20000 || !slices.Contains(lcpBounds, 2500) || !slices.Contains(lcpBounds, 4000) {
		t.Fatalf("RUMBounds[lcp] = %v, want 50 ms to 20 s with LCP's thresholds", lcpBounds)
	}
	lcp2500 := slices.Index(lcpBounds, 2500)

	phone := rumView(at(3*time.Hour), "/", new(40.0))
	phone.Device = "phone"
	cls := rumView(at(4*time.Hour), "/", nil)
	cls.CLS = new(0.1)
	cls.ServerMs = new(7.5)
	recordViews(t, repo,
		rumView(at(time.Hour), "/", new(40.0)),       // first bucket
		rumView(at(2*time.Hour), "/", new(2500.0)),   // on a bound: that bound's bucket
		rumView(at(2*time.Hour), "/", new(2500.5)),   // just above it: the next
		rumView(at(23*time.Hour), "/", new(25000.0)), // above every bound: the last
		rumView(at(5*time.Hour), "/", nil),           // no LCP: not counted for it
		phone,
		cls,
		rumView(at(6*time.Hour), "/feeds/{id}", new(100.0)),
		// The next day isn't over at the cut-off below: left alone.
		rumView(at(25*time.Hour), "/", new(100.0)),
	)

	before := at(30 * time.Hour)
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	// Idempotent: a second run (or another instance's) adds nothing.
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp again: %v", err)
	}

	server := slices.Index(metrics.RUMBounds["server"], 10)
	want := []rumDailyRow{
		{"2026-03-01", "/", "desktop", "chrome", "cls", 1, buckets("cls", slices.Index(metrics.RUMBounds["cls"], 0.1), 1)},
		{"2026-03-01", "/", "desktop", "chrome", "lcp", 4, buckets("lcp", 0, 1, lcp2500, 1, lcp2500+1, 1, len(lcpBounds), 1)},
		{"2026-03-01", "/", "desktop", "chrome", "server", 1, buckets("server", server, 1)},
		{"2026-03-01", "/", "phone", "chrome", "lcp", 1, buckets("lcp", 0, 1)},
		{"2026-03-01", "/feeds/{id}", "desktop", "chrome", "lcp", 1, buckets("lcp", slices.Index(lcpBounds, 100), 1)},
	}
	if got := listRUMDaily(t, db); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rum_daily =\n%v\nwant\n%v", got, want)
	}

	// Once the next day is over, a later run rolls it up too.
	if err := repo.RollUp(ctx, at(49*time.Hour)); err != nil {
		t.Fatalf("RollUp next day: %v", err)
	}
	got := listRUMDaily(t, db)
	if len(got) != len(want)+1 {
		t.Fatalf("after the next day, rum_daily = %v, want one more row", got)
	}
	if next := got[len(got)-1]; next.day != "2026-03-02" || next.metric != "lcp" || next.views != 1 {
		t.Errorf("next day's row = %v, want 2026-03-02's one LCP view", next)
	}
}

func TestRUMTrim(t *testing.T) {
	repo, db := newRUMRepo(t)
	ctx := context.Background()
	now := time.Now()
	for i := range 5 {
		recordViews(t, repo, rumView(now, fmt.Sprintf("/view-%d", i), nil))
	}

	// Keeping as many as there are, or more, deletes nothing.
	for _, keep := range []int{5, 6} {
		if err := repo.Trim(ctx, keep); err != nil {
			t.Fatalf("Trim(%d): %v", keep, err)
		}
		if got := viewRoutes(t, db); len(got) != 5 {
			t.Errorf("after Trim(%d), views = %v, want all 5", keep, got)
		}
	}
	if err := repo.Trim(ctx, 3); err != nil {
		t.Fatalf("Trim(3): %v", err)
	}
	if got, want := viewRoutes(t, db), []string{"/view-2", "/view-3", "/view-4"}; !slices.Equal(got, want) {
		t.Errorf("after Trim(3), views = %v, want the newest 3, %v", got, want)
	}
}

func TestRUMDeleteBefore(t *testing.T) {
	repo, db := newRUMRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	recordViews(t, repo,
		rumView(now.AddDate(0, -14, 0), "/old-daily", new(100.0)),
		rumView(now.AddDate(0, -12, 0), "/new-daily", new(100.0)),
		rumView(now.Add(-31*day), "/old-view", new(100.0)),
		rumView(now.Add(-29*day), "/new-view", new(100.0)),
	)
	if err := repo.RollUp(ctx, now); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if err := repo.DeleteBefore(ctx, now.Add(-metrics.RUMViewRetention), now.AddDate(0, -metrics.RUMDailyMonths, 0)); err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}

	if views, want := viewRoutes(t, db), []string{"/new-view"}; !slices.Equal(views, want) {
		t.Errorf("views left = %v, want %v", views, want)
	}
	var daily []string
	for _, r := range listRUMDaily(t, db) {
		daily = append(daily, r.route)
	}
	if want := []string{"/new-daily", "/old-view", "/new-view"}; !slices.Equal(daily, want) {
		t.Errorf("daily rows left = %v, want %v", daily, want)
	}
}
