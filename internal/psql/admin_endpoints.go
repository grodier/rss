package psql

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// AdminEndpointsRepository reads www's request metrics (request_metrics)
// and its recent server errors (server_errors) for the admin's endpoints
// pages.
type AdminEndpointsRepository struct {
	DB *sql.DB
}

func NewAdminEndpointsRepository(db *sql.DB) *AdminEndpointsRepository {
	return &AdminEndpointsRepository{DB: db}
}

// endpointRows are the rows a query reads, with Endpoints' columns: $1
// and $2 are its window, $4 and $5 its route and method ("" for every
// route). Hourly rows cover the hours rolled up so far, and the minute
// rows of the hours since, as RollUp picks them, the rest of the window.
func endpointRows(hourly bool) string {
	const cols = `period_start, instance, route, method, status_class, requests, duration_ms_sum, bytes_sum, buckets`
	const where = `period_start >= $1 AND period_start < $2 AND ($4 = '' OR (route = $4 AND method = $5))`
	if !hourly {
		return `SELECT ` + cols + ` FROM request_metrics WHERE resolution = 'minute' AND ` + where
	}
	return `SELECT ` + cols + ` FROM request_metrics WHERE resolution = 'hour' AND ` + where + `
	    UNION ALL
	    SELECT ` + cols + ` FROM request_metrics WHERE resolution = 'minute' AND ` + where + `
	      AND period_start >= coalesce(
	          (SELECT max(period_start) + interval '1 hour' FROM request_metrics WHERE resolution = 'hour'),
	          '-infinity')`
}

// Endpoints reads q's requests: in total, a step at a time, and by route
// and method without a Route, or by instance with one, from minute rows.
// The rows are read as they are, a step's start beside each, and summed
// here, latency buckets element-wise: on a few days of local data that
// was 2 to 3 times faster than summing the buckets in SQL, which unnests
// every row's array (see "Admin: endpoints" in docs/features.md).
func (r *AdminEndpointsRepository) Endpoints(ctx context.Context, q rss.EndpointQuery) (rss.Endpoints, error) {
	if q.Step <= 0 {
		return rss.Endpoints{}, fmt.Errorf("endpoints: step %v", q.Step)
	}
	rows, err := r.DB.QueryContext(ctx, `WITH m AS (`+endpointRows(q.Hourly)+`)
	    SELECT date_bin($3::interval, period_start, $1), instance, route, method, status_class,
	        requests, duration_ms_sum, bytes_sum, buckets
	    FROM m`, q.From, q.To, interval(q.Step), q.Route, q.Method)
	if err != nil {
		return rss.Endpoints{}, fmt.Errorf("endpoints: %w", err)
	}
	defer rows.Close()

	newStats := func() *rss.RequestStats {
		return &rss.RequestStats{Buckets: make([]int, len(metrics.DurationBounds)+1)}
	}
	total := newStats()
	steps := map[time.Time]*rss.RequestStats{}
	routes := map[[2]string]*rss.RequestStats{}
	instances := map[string]*rss.RequestStats{}
	for rows.Next() {
		var buckets pq.Int64Array
		var t time.Time
		var instance, route, method string
		var class int
		var row rss.RequestStats
		if err := rows.Scan(&t, &instance, &route, &method, &class,
			&row.Requests, &row.DurationMsSum, &row.BytesSum, &buckets); err != nil {
			return rss.Endpoints{}, fmt.Errorf("endpoints: %w", err)
		}
		t = t.UTC()
		groups := []*rss.RequestStats{total, at(steps, t, newStats)}
		if q.Route == "" {
			groups = append(groups, at(routes, [2]string{route, method}, newStats))
		} else if !q.Hourly {
			// Hourly rows have no instance.
			groups = append(groups, at(instances, instance, newStats))
		}
		for _, g := range groups {
			g.Requests += row.Requests
			if class >= 1 && class <= len(g.Classes) {
				g.Classes[class-1] += row.Requests
			}
			g.DurationMsSum += row.DurationMsSum
			g.BytesSum += row.BytesSum
			for i, n := range buckets {
				if i < len(g.Buckets) {
					g.Buckets[i] += int(n)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return rss.Endpoints{}, fmt.Errorf("endpoints: %w", err)
	}

	out := rss.Endpoints{Total: *total}
	for t, s := range steps {
		out.Series = append(out.Series, rss.EndpointPoint{T: t, RequestStats: *s})
	}
	for k, s := range routes {
		out.Routes = append(out.Routes, rss.EndpointRoute{Route: k[0], Method: k[1], RequestStats: *s})
	}
	for k, s := range instances {
		out.Instances = append(out.Instances, rss.EndpointInstance{Instance: k, RequestStats: *s})
	}
	slices.SortFunc(out.Series, func(a, b rss.EndpointPoint) int { return a.T.Compare(b.T) })
	slices.SortFunc(out.Routes, func(a, b rss.EndpointRoute) int {
		return cmp.Or(cmp.Compare(a.Route, b.Route), cmp.Compare(a.Method, b.Method))
	})
	slices.SortFunc(out.Instances, func(a, b rss.EndpointInstance) int { return cmp.Compare(a.Instance, b.Instance) })
	return out, nil
}

// at returns m's value at k, adding a new one first if there's none.
func at[K comparable, V any](m map[K]*V, k K, new func() *V) *V {
	v, ok := m[k]
	if !ok {
		v = new()
		m[k] = v
	}
	return v
}

// RouteErrors returns the newest limit server errors on route with method,
// newest first.
func (r *AdminEndpointsRepository) RouteErrors(ctx context.Context, route, method string, limit int) ([]rss.ServerError, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, occurred_at, instance, route, method, message, panic
	    FROM server_errors
	    WHERE route = $1 AND method = $2
	    ORDER BY occurred_at DESC, id DESC
	    LIMIT $3`, route, method, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.ServerError
	for rows.Next() {
		var e rss.ServerError
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Instance, &e.Route, &e.Method, &e.Message, &e.Panic); err != nil {
			return nil, err
		}
		e.OccurredAt = e.OccurredAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
