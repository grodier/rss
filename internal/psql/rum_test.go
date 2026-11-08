package psql

import (
	"context"
	"database/sql"
	"fmt"
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

func ptr[T any](v T) *T { return &v }

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

func TestRUMRecord(t *testing.T) {
	repo, db := newRUMRepo(t)
	ctx := context.Background()

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
		TTFBMs:        ptr(120.5),
		FCPMs:         ptr(300.0),
		LCPMs:         ptr(610.3),
		INPMs:         ptr(96.0),
		CLS:           ptr(0.05),
		DCLMs:         ptr(420.3),
		LoadMs:        ptr(980.0),
		ServerMs:      ptr(12.3),
		TransferBytes: ptr[int64](12345),
		LCPElement:    "img",
		INPTarget:     "data-share-url",
	}
	before := time.Now()
	recordViews(t, repo, full, rss.RUMView{Route: "/", NavType: "navigate", Device: "desktop", Browser: "other"})

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

	for i, v := range got {
		if v.ReceivedAt.Before(before.Add(-time.Minute)) || v.ReceivedAt.After(time.Now().Add(time.Minute)) {
			t.Errorf("view %d received at %v, want about now (%v)", i, v.ReceivedAt, before)
		}
	}
	// REAL columns read back as the shortest decimal that is the same
	// float32, so these values come back exactly.
	if f := deref(got[0]); fmt.Sprint(f) != fmt.Sprint(deref(full)) {
		t.Errorf("full view = %v,\nwant %v", f, deref(full))
	}
	m := got[1]
	if m.TTFBMs != nil || m.FCPMs != nil || m.LCPMs != nil || m.INPMs != nil || m.CLS != nil ||
		m.DCLMs != nil || m.LoadMs != nil || m.ServerMs != nil || m.TransferBytes != nil {
		t.Errorf("minimal view = %+v, want every metric NULL", deref(m))
	}
	if m.BrowserMajor != 0 || m.Installed || m.Connection != "" || m.Protocol != "" || m.ReferrerHost != "" || m.LCPElement != "" || m.INPTarget != "" {
		t.Errorf("minimal view = %+v, want empty context", deref(m))
	}

	// The table's checks hold: a nav type or device of another kind is refused.
	bad := rumView(time.Time{}, "/", nil)
	bad.NavType = "teleport"
	if err := repo.Record(ctx, bad); err == nil {
		t.Error("Record with nav type teleport succeeded, want the check to refuse it")
	}
}

// deref shows a view's metrics as values, for comparing and printing.
func deref(v rss.RUMView) map[string]any {
	val := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	var transfer any
	if v.TransferBytes != nil {
		transfer = *v.TransferBytes
	}
	return map[string]any{
		"context": fmt.Sprint(v.Route, v.NavType, v.Device, v.Browser, v.BrowserMajor, v.Installed, v.Connection,
			v.Protocol, v.ReferrerHost, v.LCPElement, v.INPTarget),
		"ttfb": val(v.TTFBMs), "fcp": val(v.FCPMs), "lcp": val(v.LCPMs), "inp": val(v.INPMs), "cls": val(v.CLS),
		"dcl": val(v.DCLMs), "load": val(v.LoadMs), "server": val(v.ServerMs), "transfer": transfer,
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

	phone := rumView(at(3*time.Hour), "/", ptr(40.0))
	phone.Device = "phone"
	cls := rumView(at(4*time.Hour), "/", nil)
	cls.CLS = ptr(0.1)
	cls.ServerMs = ptr(7.5)
	recordViews(t, repo,
		rumView(at(time.Hour), "/", ptr(40.0)),       // first bucket
		rumView(at(2*time.Hour), "/", ptr(2500.0)),   // on a bound: that bound's bucket
		rumView(at(2*time.Hour), "/", ptr(2500.5)),   // just above it: the next
		rumView(at(23*time.Hour), "/", ptr(25000.0)), // above every bound: the last
		rumView(at(5*time.Hour), "/", nil),           // no LCP: not counted for it
		phone,
		cls,
		rumView(at(6*time.Hour), "/feeds/{id}", ptr(100.0)),
		// The next day isn't over at the cut-off below: left alone.
		rumView(at(25*time.Hour), "/", ptr(100.0)),
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
	routes := func() []string {
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
		return out
	}

	// Keeping as many as there are, or more, deletes nothing.
	for _, keep := range []int{5, 6} {
		if err := repo.Trim(ctx, keep); err != nil {
			t.Fatalf("Trim(%d): %v", keep, err)
		}
		if got := routes(); len(got) != 5 {
			t.Errorf("after Trim(%d), views = %v, want all 5", keep, got)
		}
	}
	if err := repo.Trim(ctx, 3); err != nil {
		t.Fatalf("Trim(3): %v", err)
	}
	if got, want := routes(), []string{"/view-2", "/view-3", "/view-4"}; !slices.Equal(got, want) {
		t.Errorf("after Trim(3), views = %v, want the newest 3, %v", got, want)
	}
}

func TestRUMDeleteBefore(t *testing.T) {
	repo, db := newRUMRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	recordViews(t, repo,
		rumView(now.AddDate(0, -14, 0), "/old-daily", ptr(100.0)),
		rumView(now.AddDate(0, -12, 0), "/new-daily", ptr(100.0)),
		rumView(now.Add(-31*day), "/old-view", ptr(100.0)),
		rumView(now.Add(-29*day), "/new-view", ptr(100.0)),
	)
	if err := repo.RollUp(ctx, now); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if err := repo.DeleteBefore(ctx, now.Add(-metrics.RUMViewRetention), now.AddDate(0, -metrics.RUMDailyMonths, 0)); err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}

	var views []string
	rows, err := db.Query(`SELECT route FROM rum_views ORDER BY route`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			t.Fatal(err)
		}
		views = append(views, route)
	}
	if want := []string{"/new-view"}; !slices.Equal(views, want) {
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
