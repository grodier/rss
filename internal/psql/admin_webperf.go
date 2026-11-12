package psql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// AdminWebPerfRepository reads the real-user views (rum_views) and their
// daily rollups (rum_daily) for the admin's web performance page.
type AdminWebPerfRepository struct {
	DB *sql.DB
}

func NewAdminWebPerfRepository(db *sql.DB) *AdminWebPerfRepository {
	return &AdminWebPerfRepository{DB: db}
}

// webPerfMetric is a metric the page shows, by its rum_daily name, with
// its rum_views column.
type webPerfMetric struct{ metric, column string }

// webPerfMetrics are the metrics the page shows; webPerfVitals the Core Web
// Vitals, all the breakdowns show. Each percentile of raw views sorts them,
// so the breakdowns leave FCP and TTFB out.
var (
	webPerfVitals  = []webPerfMetric{{"lcp", "lcp_ms"}, {"inp", "inp_ms"}, {"cls", "cls"}}
	webPerfMetrics = append([]webPerfMetric{{"ttfb", "ttfb_ms"}, {"fcp", "fcp_ms"}}, webPerfVitals...)
)

// webPerfQuantiles are RUMStat's percentiles, in its order.
var webPerfQuantiles = []float64{0.75, 0.90, 0.95}

// WebPerf reads q's views: from rum_views, with exact percentiles, or from
// rum_daily, with percentiles from the summed buckets.
func (r *AdminWebPerfRepository) WebPerf(ctx context.Context, q rss.WebPerfQuery) (rss.WebPerf, error) {
	if q.Step <= 0 {
		return rss.WebPerf{}, fmt.Errorf("web perf: step %v", q.Step)
	}
	if q.Rollups {
		return r.rollups(ctx, q)
	}
	return r.raw(ctx, q)
}

// rawStats are the select list's expressions for each of ms, in order:
// how many views reported it, its percentiles, and how many were good and
// poor (the rest need improvement). The thresholds are constants, not
// input.
func rawStats(ms []webPerfMetric) string {
	var b strings.Builder
	for _, m := range ms {
		t := metrics.RUMThresholds[m.metric]
		fmt.Fprintf(&b, `,
	    count(%[1]s),
	    percentile_cont('{0.75,0.9,0.95}'::float8[]) WITHIN GROUP (ORDER BY %[1]s),
	    count(*) FILTER (WHERE %[1]s <= %[2]g),
	    count(*) FILTER (WHERE %[1]s > %[3]g)`, m.column, t.Good, t.Poor)
	}
	return b.String()
}

// scanRawStats returns scan destinations for rawStats(ms)' columns, and a
// function that reads them into a group's metrics.
func scanRawStats(ms []webPerfMetric) ([]any, func() map[string]rss.RUMStat) {
	n := len(ms)
	views := make([]int, n)
	pcts := make([]pq.Float64Array, n)
	good := make([]int, n)
	poor := make([]int, n)
	dest := make([]any, 0, 4*n)
	for i := range n {
		dest = append(dest, &views[i], &pcts[i], &good[i], &poor[i])
	}
	return dest, func() map[string]rss.RUMStat {
		out := map[string]rss.RUMStat{}
		for i, m := range ms {
			if views[i] == 0 || len(pcts[i]) != len(webPerfQuantiles) {
				continue
			}
			out[m.metric] = rss.RUMStat{
				Views: views[i],
				P75:   pcts[i][0], P90: pcts[i][1], P95: pcts[i][2],
				Good: good[i], NeedsImprovement: views[i] - good[i] - poor[i], Poor: poor[i],
			}
		}
		return out
	}
}

// rawWhere picks q's views: $1 and $2 are its window, $3 its device.
const rawWhere = `received_at >= $1 AND received_at < $2 AND ($3 = '' OR device = $3)`

// scanRaw runs query, whose select list ends with rawStats(ms), and calls
// row for each row with a scan that reads the columns before them into its
// destinations, and read, which returns the row's stats.
func (r *AdminWebPerfRepository) scanRaw(ctx context.Context, query string, args []any, ms []webPerfMetric,
	row func(scan func(...any) error, read func() map[string]rss.RUMStat) error) error {
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	stats, read := scanRawStats(ms)
	for rows.Next() {
		scan := func(dest ...any) error { return rows.Scan(append(dest, stats...)...) }
		if err := row(scan, read); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *AdminWebPerfRepository) raw(ctx context.Context, q rss.WebPerfQuery) (rss.WebPerf, error) {
	var wp rss.WebPerf
	args := []any{q.From, q.To, q.Device}

	// Every view, then by device, browser, installed and route, in one
	// scan. A dimension's column is NULL in the sets that don't group by
	// it, and none of them is NULL in a row.
	err := r.scanRaw(ctx, `SELECT count(*)`+rawStats(webPerfMetrics)+`
	    FROM rum_views
	    WHERE `+rawWhere, args, webPerfMetrics, func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
		if err := scan(&wp.Total.Views); err != nil {
			return err
		}
		wp.Total.Metrics = read()
		return nil
	})
	if err != nil {
		return wp, fmt.Errorf("web perf total: %w", err)
	}
	err = r.scanRaw(ctx, `SELECT device, browser, installed::text, route, count(*)`+rawStats(webPerfVitals)+`
	    FROM rum_views
	    WHERE `+rawWhere+`
	    GROUP BY GROUPING SETS ((device), (browser), (installed), (route))`, args, webPerfVitals,
		func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
			var device, browser, installed, route sql.NullString
			var g rss.RUMGroup
			if err := scan(&device, &browser, &installed, &route, &g.Views); err != nil {
				return err
			}
			g.Metrics = read()
			switch {
			case device.Valid:
				g.Key = device.String
				wp.Devices = append(wp.Devices, g)
			case browser.Valid:
				g.Key = browser.String
				wp.Browsers = append(wp.Browsers, g)
			case installed.Valid:
				g.Key = installed.String
				wp.Installed = append(wp.Installed, g)
			case route.Valid:
				g.Key = route.String
				wp.Routes = append(wp.Routes, g)
			}
			return nil
		})
	if err != nil {
		return wp, fmt.Errorf("web perf groups: %w", err)
	}
	sortGroups(wp.Devices, wp.Browsers, wp.Installed, wp.Routes)

	// The metrics over time, a step at a time from From.
	err = r.scanRaw(ctx, `SELECT date_bin($4::interval, received_at, $1) AS t`+rawStats(webPerfMetrics)+`
	    FROM rum_views
	    WHERE `+rawWhere+`
	    GROUP BY t
	    ORDER BY t`, append(args, interval(q.Step)), webPerfMetrics,
		func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
			var p rss.RUMPoint
			if err := scan(&p.T); err != nil {
				return err
			}
			p.T = p.T.UTC()
			p.Metrics = read()
			wp.Series = append(wp.Series, p)
			return nil
		})
	if err != nil {
		return wp, fmt.Errorf("web perf series: %w", err)
	}

	wp.RouteViews, err = r.routeViews(ctx, `SELECT date_bin($4::interval, received_at, $1) AS t, route, count(*)::int
	    FROM rum_views
	    WHERE `+rawWhere+`
	    GROUP BY t, route
	    ORDER BY t, route`, append(args, interval(q.Step))...)
	if err != nil {
		return wp, err
	}

	// Where the views came from, and over what.
	rows, err := r.DB.QueryContext(ctx, `SELECT referrer_host, protocol, connection, count(*)::int
	    FROM rum_views
	    WHERE `+rawWhere+`
	    GROUP BY GROUPING SETS ((referrer_host), (protocol), (connection))`, args...)
	if err != nil {
		return wp, err
	}
	defer rows.Close()
	for rows.Next() {
		var referrer, protocol, connection sql.NullString
		var n int
		if err := rows.Scan(&referrer, &protocol, &connection, &n); err != nil {
			return wp, err
		}
		switch {
		case referrer.Valid:
			if referrer.String != "" {
				wp.Referrers = append(wp.Referrers, rss.RUMCount{Key: referrer.String, Views: n})
			}
		case protocol.Valid:
			wp.Protocols = append(wp.Protocols, rss.RUMCount{Key: protocol.String, Views: n})
		case connection.Valid:
			wp.Connections = append(wp.Connections, rss.RUMCount{Key: connection.String, Views: n})
		}
	}
	if err := rows.Err(); err != nil {
		return wp, fmt.Errorf("web perf sources: %w", err)
	}
	for _, cs := range [][]rss.RUMCount{wp.Referrers, wp.Protocols, wp.Connections} {
		sortCounts(cs)
	}
	return wp, nil
}

// rollupWhere picks q's rum_daily rows: $1 and $2 are its window's
// midnights, $3 its device.
const rollupWhere = `day >= ($1::timestamptz AT TIME ZONE 'UTC')::date
	      AND day < ($2::timestamptz AT TIME ZONE 'UTC')::date
	      AND ($3 = '' OR device = $3)`

// rollupViews are rum_daily's views per day, route, device and browser: a
// row per metric counts the views that reported it, so the most of them is
// how many views there were, but for any that reported none.
const rollupViews = `WITH views AS (
	    SELECT day, route, device, browser, max(views) AS views
	    FROM rum_daily
	    WHERE ` + rollupWhere + `
	    GROUP BY day, route, device, browser
	)`

func (r *AdminWebPerfRepository) rollups(ctx context.Context, q rss.WebPerfQuery) (rss.WebPerf, error) {
	var wp rss.WebPerf
	args := []any{q.From, q.To, q.Device}

	// The views of every group, then the summed buckets of each group's
	// metrics, in the same grouping sets.
	groups := map[[2]string]*rss.RUMGroup{} // by dimension and key
	group := func(device, browser, route sql.NullString) *rss.RUMGroup {
		k := [2]string{}
		switch {
		case device.Valid:
			k = [2]string{"device", device.String}
		case browser.Valid:
			k = [2]string{"browser", browser.String}
		case route.Valid:
			k = [2]string{"route", route.String}
		}
		g, ok := groups[k]
		if !ok {
			g = &rss.RUMGroup{Key: k[1], Metrics: map[string]rss.RUMStat{}}
			groups[k] = g
		}
		return g
	}
	rows, err := r.DB.QueryContext(ctx, rollupViews+`
	    SELECT device, browser, route, sum(views)::int
	    FROM views
	    GROUP BY GROUPING SETS ((), (device), (browser), (route))`, args...)
	if err != nil {
		return wp, err
	}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var device, browser, route sql.NullString
			var n int
			if err := rows.Scan(&device, &browser, &route, &n); err != nil {
				return err
			}
			group(device, browser, route).Views = n
		}
		return rows.Err()
	}()
	if err != nil {
		return wp, fmt.Errorf("web perf group views: %w", err)
	}

	buckets := map[*rss.RUMGroup]map[string][]int{}
	// Summed by every dimension at once first, which a hash does quickly,
	// so the grouping sets' sorts have a few thousand rows rather than
	// every day's buckets.
	rows, err = r.DB.QueryContext(ctx, `WITH sums AS (
	        SELECT device, browser, route, metric, b.i::int AS i, sum(b.n) AS n
	        FROM rum_daily, unnest(buckets) WITH ORDINALITY AS b(n, i)
	        WHERE `+rollupWhere+` AND metric = ANY($4)
	        GROUP BY device, browser, route, metric, b.i
	    )
	    SELECT device, browser, route, metric, i, sum(n)::int
	    FROM sums
	    GROUP BY GROUPING SETS ((metric, i), (device, metric, i), (browser, metric, i), (route, metric, i))`,
		append(args, pq.Array(metricNames()))...)
	if err != nil {
		return wp, err
	}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var device, browser, route sql.NullString
			var metric string
			var i, n int
			if err := rows.Scan(&device, &browser, &route, &metric, &i, &n); err != nil {
				return err
			}
			addBucket(buckets, group(device, browser, route), metric, i, n)
		}
		return rows.Err()
	}()
	if err != nil {
		return wp, fmt.Errorf("web perf group buckets: %w", err)
	}
	for k, g := range groups {
		g.Metrics = stats(buckets[g])
		if k[0] != "" {
			// Like the raw views', the breakdowns have only the vitals.
			for metric := range g.Metrics {
				if !slices.ContainsFunc(webPerfVitals, func(m webPerfMetric) bool { return m.metric == metric }) {
					delete(g.Metrics, metric)
				}
			}
		}
		switch k[0] {
		case "device":
			wp.Devices = append(wp.Devices, *g)
		case "browser":
			wp.Browsers = append(wp.Browsers, *g)
		case "route":
			wp.Routes = append(wp.Routes, *g)
		default:
			wp.Total = *g
		}
	}
	sortGroups(wp.Devices, wp.Browsers, wp.Routes)

	// The metrics over time: each step's days' buckets summed.
	points := map[time.Time]map[string][]int{}
	rows, err = r.DB.QueryContext(ctx, `SELECT date_bin($5::interval, day::timestamp AT TIME ZONE 'UTC', $1) AS t, metric, b.i::int, sum(b.n)::int
	    FROM rum_daily, unnest(buckets) WITH ORDINALITY AS b(n, i)
	    WHERE `+rollupWhere+` AND metric = ANY($4)
	    GROUP BY t, metric, b.i`,
		append(args, pq.Array(metricNames()), interval(q.Step))...)
	if err != nil {
		return wp, err
	}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var t time.Time
			var metric string
			var i, n int
			if err := rows.Scan(&t, &metric, &i, &n); err != nil {
				return err
			}
			t = t.UTC()
			if points[t] == nil {
				points[t] = map[string][]int{}
			}
			m := points[t]
			if m[metric] == nil {
				m[metric] = make([]int, len(metrics.RUMBounds[metric])+1)
			}
			if i >= 1 && i <= len(m[metric]) {
				m[metric][i-1] += n
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return wp, fmt.Errorf("web perf series: %w", err)
	}
	for t, m := range points {
		wp.Series = append(wp.Series, rss.RUMPoint{T: t, Metrics: stats(m)})
	}
	sort.Slice(wp.Series, func(i, j int) bool { return wp.Series[i].T.Before(wp.Series[j].T) })

	wp.RouteViews, err = r.routeViews(ctx, rollupViews+`
	    SELECT date_bin($4::interval, day::timestamp AT TIME ZONE 'UTC', $1) AS t, route, sum(views)::int
	    FROM views
	    GROUP BY t, route
	    ORDER BY t, route`, append(args, interval(q.Step))...)
	return wp, err
}

func (r *AdminWebPerfRepository) routeViews(ctx context.Context, query string, args ...any) ([]rss.RUMRouteViews, error) {
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.RUMRouteViews
	for rows.Next() {
		var v rss.RUMRouteViews
		if err := rows.Scan(&v.T, &v.Route, &v.Views); err != nil {
			return nil, err
		}
		v.T = v.T.UTC()
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("web perf route views: %w", err)
	}
	return out, nil
}

// addBucket adds n views to bucket i (from 1, as unnest's ordinality
// counts) of g's metric.
func addBucket(buckets map[*rss.RUMGroup]map[string][]int, g *rss.RUMGroup, metric string, i, n int) {
	if buckets[g] == nil {
		buckets[g] = map[string][]int{}
	}
	m := buckets[g]
	if m[metric] == nil {
		m[metric] = make([]int, len(metrics.RUMBounds[metric])+1)
	}
	if i >= 1 && i <= len(m[metric]) {
		m[metric][i-1] += n
	}
}

// stats returns each metric's stats from its buckets: the percentiles
// interpolated within a bucket, and the views by rating, exact because the
// thresholds are among the bounds.
func stats(buckets map[string][]int) map[string]rss.RUMStat {
	out := map[string]rss.RUMStat{}
	for metric, counts := range buckets {
		bounds := metrics.RUMBounds[metric]
		t := metrics.RUMThresholds[metric]
		var s rss.RUMStat
		for i, n := range counts {
			s.Views += n
			switch {
			case i < len(bounds) && bounds[i] <= t.Good:
				s.Good += n
			case i > 0 && bounds[i-1] >= t.Poor:
				s.Poor += n
			default:
				s.NeedsImprovement += n
			}
		}
		if s.Views == 0 {
			continue
		}
		s.P75 = metrics.Percentile(bounds, counts, webPerfQuantiles[0])
		s.P90 = metrics.Percentile(bounds, counts, webPerfQuantiles[1])
		s.P95 = metrics.Percentile(bounds, counts, webPerfQuantiles[2])
		out[metric] = s
	}
	return out
}

func metricNames() []string {
	names := make([]string, len(webPerfMetrics))
	for i, m := range webPerfMetrics {
		names[i] = m.metric
	}
	return names
}

// interval is d as a Postgres interval.
func interval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(math.Round(d.Seconds())))
}

// sortCounts sorts cs by views, most first, then by key.
func sortCounts(cs []rss.RUMCount) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Views != cs[j].Views {
			return cs[i].Views > cs[j].Views
		}
		return cs[i].Key < cs[j].Key
	})
}

// sortGroups sorts each of gss by key.
func sortGroups(gss ...[]rss.RUMGroup) {
	for _, gs := range gss {
		sort.Slice(gs, func(i, j int) bool { return gs[i].Key < gs[j].Key })
	}
}
