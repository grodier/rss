package psql

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// webPerfRouteRows is how many LCP elements and interaction targets
// WebPerfRoute returns: they're tag and data-* names, so there are few,
// but anyone can post a beacon.
const webPerfRouteRows = 20

// WebPerfRoute reads route's views in q: from rum_views, with exact
// percentiles, or from rum_daily, with percentiles from the summed buckets,
// as WebPerf does.
func (r *AdminWebPerfRepository) WebPerfRoute(ctx context.Context, q rss.WebPerfQuery, route string) (rss.WebPerfRoute, error) {
	if q.Step <= 0 {
		return rss.WebPerfRoute{}, fmt.Errorf("web perf route: step %v", q.Step)
	}
	if q.Rollups {
		return r.routeRollups(ctx, q, route)
	}
	return r.routeRaw(ctx, q, route)
}

// rawRouteWhere picks q's views of a route: rawWhere's, and $4 the route.
const rawRouteWhere = rawWhere + ` AND route = $4`

// unrate drops the views by rating of the metrics that have no thresholds
// (the server's time), which the queries count against zero.
func unrate(stats map[string]rss.RUMStat) {
	for metric, s := range stats {
		if _, ok := metrics.RUMThresholds[metric]; !ok {
			s.Good, s.NeedsImprovement, s.Poor = 0, 0, 0
			stats[metric] = s
		}
	}
}

func (r *AdminWebPerfRepository) routeRaw(ctx context.Context, q rss.WebPerfQuery, route string) (rss.WebPerfRoute, error) {
	var wr rss.WebPerfRoute
	args := []any{q.From, q.To, q.Device, route}

	err := r.scanRaw(ctx, `SELECT count(*)`+rawStats(rumColumns)+`
	    FROM rum_views
	    WHERE `+rawRouteWhere, args, rumColumns, func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
		if err := scan(&wr.Total.Views); err != nil {
			return err
		}
		wr.Total.Metrics = read()
		unrate(wr.Total.Metrics)
		return nil
	})
	if err != nil {
		return wr, fmt.Errorf("web perf route total: %w", err)
	}
	if wr.Total.Views == 0 {
		return wr, nil
	}

	err = r.scanRaw(ctx, `SELECT device, browser, count(*)`+rawStats(webPerfVitals)+`
	    FROM rum_views
	    WHERE `+rawRouteWhere+`
	    GROUP BY GROUPING SETS ((device), (browser))`, args, webPerfVitals,
		func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
			var device, browser sql.NullString
			var g rss.RUMGroup
			if err := scan(&device, &browser, &g.Views); err != nil {
				return err
			}
			g.Metrics = read()
			if device.Valid {
				g.Key = device.String
				wr.Devices = append(wr.Devices, g)
			} else {
				g.Key = browser.String
				wr.Browsers = append(wr.Browsers, g)
			}
			return nil
		})
	if err != nil {
		return wr, fmt.Errorf("web perf route groups: %w", err)
	}
	sortGroups(wr.Devices, wr.Browsers)

	err = r.scanRaw(ctx, `SELECT date_bin($5::interval, received_at, $1) AS t`+rawStats(rumColumns)+`
	    FROM rum_views
	    WHERE `+rawRouteWhere+`
	    GROUP BY t
	    ORDER BY t`, append(args, interval(q.Step)), rumColumns,
		func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
			var p rss.RUMPoint
			if err := scan(&p.T); err != nil {
				return err
			}
			p.T = p.T.UTC()
			p.Metrics = read()
			unrate(p.Metrics)
			wr.Series = append(wr.Series, p)
			return nil
		})
	if err != nil {
		return wr, fmt.Errorf("web perf route series: %w", err)
	}

	if wr.Buckets, err = r.rawBuckets(ctx, args); err != nil {
		return wr, err
	}

	// What the largest paint was, and which interactions were slowest: a
	// view names its LCP element and its slowest interaction's target.
	lcp := []rumColumn{{"lcp", "lcp_ms"}}
	wr.LCPElements, err = r.rawKeyed(ctx, `SELECT lcp_element, count(*)`+rawStats(lcp)+`
	    FROM rum_views
	    WHERE `+rawRouteWhere+` AND lcp_ms IS NOT NULL
	    GROUP BY lcp_element
	    ORDER BY count(*) DESC, lcp_element
	    LIMIT `+fmt.Sprint(webPerfRouteRows), args, lcp)
	if err != nil {
		return wr, fmt.Errorf("web perf route lcp elements: %w", err)
	}
	inp := []rumColumn{{"inp", "inp_ms"}}
	wr.INPTargets, err = r.rawKeyed(ctx, `SELECT inp_target, count(*)`+rawStats(inp)+`
	    FROM rum_views
	    WHERE `+rawRouteWhere+` AND inp_ms IS NOT NULL
	    GROUP BY inp_target
	    ORDER BY count(*) FILTER (WHERE inp_ms > `+fmt.Sprintf("%g", metrics.RUMThresholds["inp"].Good)+`::real) DESC, count(*) DESC, inp_target
	    LIMIT `+fmt.Sprint(webPerfRouteRows), args, inp)
	if err != nil {
		return wr, fmt.Errorf("web perf route inp targets: %w", err)
	}

	rows, err := r.DB.QueryContext(ctx, `SELECT nav_type, count(*)::int
	    FROM rum_views
	    WHERE `+rawRouteWhere+`
	    GROUP BY nav_type`, args...)
	if err != nil {
		return wr, err
	}
	if wr.NavTypes, err = scanCounts(rows); err != nil {
		return wr, fmt.Errorf("web perf route nav types: %w", err)
	}
	sortCounts(wr.NavTypes)

	// The sizes of the documents that came over the network: one from the
	// browser's cache transfers nothing.
	var sizes pq.Float64Array
	err = r.DB.QueryRowContext(ctx, `SELECT count(transfer_bytes)::int, count(*) FILTER (WHERE transfer_bytes = 0)::int,
	        percentile_cont('{0.5,0.75,0.95}'::float8[]) WITHIN GROUP (ORDER BY transfer_bytes) FILTER (WHERE transfer_bytes > 0)
	    FROM rum_views
	    WHERE `+rawRouteWhere, args...).Scan(&wr.Transfer.Views, &wr.Transfer.Cached, &sizes)
	if err != nil {
		return wr, fmt.Errorf("web perf route transfer: %w", err)
	}
	if len(sizes) == 3 {
		wr.Transfer.P50, wr.Transfer.P75, wr.Transfer.P95 = sizes[0], sizes[1], sizes[2]
	}
	return wr, nil
}

// rawBuckets counts the route's views picked by args into each metric's
// metrics.RUMBounds buckets, as RollUp does: a view goes in the bucket of
// the first bound it isn't above, or the last.
func (r *AdminWebPerfRepository) rawBuckets(ctx context.Context, args []any) (map[string][]int, error) {
	// A row per view and metric it reported, each with its bounds.
	values := make([]string, len(rumColumns))
	for i, c := range rumColumns {
		args = append(args, pq.Array(metrics.RUMBounds[c.metric]))
		values[i] = fmt.Sprintf("('%s', %s, $%d::real[])", c.metric, c.column, len(args))
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT m.metric, (SELECT count(*) FROM unnest(m.bounds) AS bound WHERE m.v > bound)::int, count(*)::int
	    FROM rum_views, LATERAL (VALUES `+strings.Join(values, ", ")+`) AS m(metric, v, bounds)
	    WHERE `+rawRouteWhere+` AND m.v IS NOT NULL
	    GROUP BY 1, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("web perf route buckets: %w", err)
	}
	defer rows.Close()
	out := map[string][]int{}
	for rows.Next() {
		var metric string
		var i, n int
		if err := rows.Scan(&metric, &i, &n); err != nil {
			return nil, err
		}
		addBucket(out, metric, i+1, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("web perf route buckets: %w", err)
	}
	return out, nil
}

// rawKeyed runs query, whose select list is a key, its views and
// rawStats(ms), and returns its rows as groups.
func (r *AdminWebPerfRepository) rawKeyed(ctx context.Context, query string, args []any, ms []rumColumn) ([]rss.RUMGroup, error) {
	var out []rss.RUMGroup
	err := r.scanRaw(ctx, query, args, ms, func(scan func(...any) error, read func() map[string]rss.RUMStat) error {
		var g rss.RUMGroup
		if err := scan(&g.Key, &g.Views); err != nil {
			return err
		}
		g.Metrics = read()
		out = append(out, g)
		return nil
	})
	return out, err
}

// rollupRouteWhere picks q's rum_daily rows of a route: rollupWhere's, and
// $4 the route.
const rollupRouteWhere = rollupWhere + ` AND route = $4`

func (r *AdminWebPerfRepository) routeRollups(ctx context.Context, q rss.WebPerfQuery, route string) (rss.WebPerfRoute, error) {
	var wr rss.WebPerfRoute
	args := []any{q.From, q.To, q.Device, route}

	// The views of every group, as rollupViews counts them, then the
	// summed buckets of each group's metrics, in the same grouping sets.
	groups := map[[2]string]*rss.RUMGroup{} // by dimension and key
	group := func(device, browser sql.NullString) *rss.RUMGroup {
		k := [2]string{}
		switch {
		case device.Valid:
			k = [2]string{"device", device.String}
		case browser.Valid:
			k = [2]string{"browser", browser.String}
		}
		g, ok := groups[k]
		if !ok {
			g = &rss.RUMGroup{Key: k[1], Metrics: map[string]rss.RUMStat{}}
			groups[k] = g
		}
		return g
	}
	rows, err := r.DB.QueryContext(ctx, `WITH views AS (
	        SELECT day, device, browser, max(views) AS views
	        FROM rum_daily
	        WHERE `+rollupRouteWhere+`
	        GROUP BY day, device, browser
	    )
	    SELECT device, browser, coalesce(sum(views), 0)::int
	    FROM views
	    GROUP BY GROUPING SETS ((), (device), (browser))`, args...)
	if err != nil {
		return wr, err
	}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var device, browser sql.NullString
			var n int
			if err := rows.Scan(&device, &browser, &n); err != nil {
				return err
			}
			group(device, browser).Views = n
		}
		return rows.Err()
	}()
	if err != nil {
		return wr, fmt.Errorf("web perf route group views: %w", err)
	}
	if g := groups[[2]string{}]; g == nil || g.Views == 0 {
		wr.Total.Metrics = map[string]rss.RUMStat{}
		return wr, nil
	}

	buckets := map[*rss.RUMGroup]map[string][]int{}
	rows, err = r.DB.QueryContext(ctx, `WITH sums AS (
	        SELECT device, browser, metric, b.i::int AS i, sum(b.n) AS n
	        FROM rum_daily, unnest(buckets) WITH ORDINALITY AS b(n, i)
	        WHERE `+rollupRouteWhere+`
	        GROUP BY device, browser, metric, b.i
	    )
	    SELECT device, browser, metric, i, sum(n)::int
	    FROM sums
	    GROUP BY GROUPING SETS ((metric, i), (device, metric, i), (browser, metric, i))`, args...)
	if err != nil {
		return wr, err
	}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var device, browser sql.NullString
			var metric string
			var i, n int
			if err := rows.Scan(&device, &browser, &metric, &i, &n); err != nil {
				return err
			}
			g := group(device, browser)
			if buckets[g] == nil {
				buckets[g] = map[string][]int{}
			}
			addBucket(buckets[g], metric, i, n)
		}
		return rows.Err()
	}()
	if err != nil {
		return wr, fmt.Errorf("web perf route group buckets: %w", err)
	}
	for k, g := range groups {
		if k[0] != "" {
			// Like the raw views', the breakdowns have only the vitals.
			maps.DeleteFunc(buckets[g], func(metric string, _ []int) bool { return !isVital(metric) })
		}
		g.Metrics = stats(buckets[g])
		switch k[0] {
		case "device":
			wr.Devices = append(wr.Devices, *g)
		case "browser":
			wr.Browsers = append(wr.Browsers, *g)
		default:
			wr.Total = *g
			wr.Buckets = buckets[g]
			unrate(wr.Total.Metrics)
		}
	}
	sortGroups(wr.Devices, wr.Browsers)

	// The metrics over time: each step's days' buckets summed.
	points := map[time.Time]map[string][]int{}
	rows, err = r.DB.QueryContext(ctx, `SELECT date_bin($5::interval, day::timestamp AT TIME ZONE 'UTC', $1) AS t, metric, b.i::int, sum(b.n)::int
	    FROM rum_daily, unnest(buckets) WITH ORDINALITY AS b(n, i)
	    WHERE `+rollupRouteWhere+`
	    GROUP BY t, metric, b.i`, append(args, interval(q.Step))...)
	if err != nil {
		return wr, err
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
			addBucket(points[t], metric, i, n)
		}
		return rows.Err()
	}()
	if err != nil {
		return wr, fmt.Errorf("web perf route series: %w", err)
	}
	for t, m := range points {
		p := rss.RUMPoint{T: t, Metrics: stats(m)}
		unrate(p.Metrics)
		wr.Series = append(wr.Series, p)
	}
	sort.Slice(wr.Series, func(i, j int) bool { return wr.Series[i].T.Before(wr.Series[j].T) })
	return wr, nil
}

// HasViews reports whether route had a view received from since on, from
// rum_views: whether it's a page the reader shows.
func (r *AdminWebPerfRepository) HasViews(ctx context.Context, route string, since time.Time) (bool, error) {
	var ok bool
	err := r.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM rum_views WHERE received_at >= $1 AND route = $2)`, since, route).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("web perf has views: %w", err)
	}
	return ok, nil
}
