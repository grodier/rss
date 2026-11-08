package psql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/lib/pq"
)

// MetricsRepository stores www's request and runtime metrics
// (request_metrics, runtime_metrics).
type MetricsRepository struct {
	DB *sql.DB
}

func NewMetricsRepository(db *sql.DB) *MetricsRepository {
	return &MetricsRepository{DB: db}
}

// SaveRequests saves minute rows. A row whose period, instance, route,
// method and status class are already there adds to it (a process restarted
// within a minute on the same host writes that minute twice).
func (r *MetricsRepository) SaveRequests(ctx context.Context, rows []metrics.RequestRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO request_metrics
		    (period_start, resolution, instance, route, method, status_class,
		     requests, duration_ms_sum, bytes_sum, buckets)
		VALUES ($1, 'minute', $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (resolution, period_start, instance, route, method, status_class) DO UPDATE SET
		    requests = request_metrics.requests + EXCLUDED.requests,
		    duration_ms_sum = request_metrics.duration_ms_sum + EXCLUDED.duration_ms_sum,
		    bytes_sum = request_metrics.bytes_sum + EXCLUDED.bytes_sum,
		    buckets = ARRAY(
		        SELECT coalesce(a, 0) + coalesce(b, 0)
		        FROM unnest(request_metrics.buckets, EXCLUDED.buckets) WITH ORDINALITY AS u(a, b, i)
		        ORDER BY i
		    )`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx, row.PeriodStart, row.Instance, row.Route, row.Method, row.StatusClass,
			row.Requests, row.DurationMsSum, row.BytesSum, pq.Array(row.Buckets))
		if err != nil {
			return fmt.Errorf("save request metrics for %s %s: %w", row.Method, row.Route, err)
		}
	}
	return tx.Commit()
}

// ListRequests returns the request rows of resolution ("minute" or "hour")
// whose period starts in [from, to), oldest first.
func (r *MetricsRepository) ListRequests(ctx context.Context, resolution string, from, to time.Time) ([]metrics.RequestRow, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT period_start, instance, route, method, status_class,
		    requests, duration_ms_sum, bytes_sum, buckets
		FROM request_metrics
		WHERE resolution = $1 AND period_start >= $2 AND period_start < $3
		ORDER BY period_start, instance, route, method, status_class`, resolution, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []metrics.RequestRow
	for rows.Next() {
		var m metrics.RequestRow
		err := rows.Scan(&m.PeriodStart, &m.Instance, &m.Route, &m.Method, &m.StatusClass,
			&m.Requests, &m.DurationMsSum, &m.BytesSum, pq.Array(&m.Buckets))
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveRuntime saves a runtime sample; a second sample for the same minute
// and instance is dropped.
func (r *MetricsRepository) SaveRuntime(ctx context.Context, row metrics.RuntimeRow) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO runtime_metrics
		    (period_start, instance, heap_bytes, total_bytes, goroutines, gc_pause_p99_ms,
		     db_open, db_in_use, db_wait_count, db_wait_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT DO NOTHING`,
		row.PeriodStart, row.Instance, row.HeapBytes, row.TotalBytes, row.Goroutines, row.GCPauseP99Ms,
		row.DBOpen, row.DBInUse, row.DBWaitCount, row.DBWaitMs)
	return err
}

// ListRuntime returns the runtime samples whose period starts in
// [from, to), oldest first.
func (r *MetricsRepository) ListRuntime(ctx context.Context, from, to time.Time) ([]metrics.RuntimeRow, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT period_start, instance, heap_bytes, total_bytes, goroutines,
		    gc_pause_p99_ms, db_open, db_in_use, db_wait_count, db_wait_ms
		FROM runtime_metrics
		WHERE period_start >= $1 AND period_start < $2
		ORDER BY period_start, instance`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []metrics.RuntimeRow
	for rows.Next() {
		var m metrics.RuntimeRow
		err := rows.Scan(&m.PeriodStart, &m.Instance, &m.HeapBytes, &m.TotalBytes, &m.Goroutines,
			&m.GCPauseP99Ms, &m.DBOpen, &m.DBInUse, &m.DBWaitCount, &m.DBWaitMs)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RollUp sums the minute rows of each whole hour before before into one
// hourly row per route, method and status class (instance ”), adding
// buckets element-wise. It starts after the newest hour already rolled up,
// and leaves an hour that has its rows alone, so instances can run it side
// by side and it can run again.
func (r *MetricsRepository) RollUp(ctx context.Context, before time.Time) error {
	// Whole hours only: rolling up part of an hour would leave the rest out
	// for good, since an hour that has rows is never rolled up again.
	before = before.UTC().Truncate(time.Hour)
	_, err := r.DB.ExecContext(ctx, `WITH minutes AS (
		    SELECT date_trunc('hour', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour,
		        route, method, status_class, requests, duration_ms_sum, bytes_sum, buckets
		    FROM request_metrics
		    WHERE resolution = 'minute'
		      AND period_start < $1
		      AND period_start >= coalesce(
		          (SELECT max(period_start) + interval '1 hour' FROM request_metrics WHERE resolution = 'hour'),
		          '-infinity')
		), totals AS (
		    SELECT hour, route, method, status_class,
		        sum(requests)::int AS requests, sum(duration_ms_sum) AS duration_ms_sum, sum(bytes_sum)::bigint AS bytes_sum
		    FROM minutes
		    GROUP BY hour, route, method, status_class
		), bucket_sums AS (
		    SELECT hour, route, method, status_class, u.i, sum(u.n)::int AS n
		    FROM minutes, unnest(minutes.buckets) WITH ORDINALITY AS u(n, i)
		    GROUP BY hour, route, method, status_class, u.i
		), bucket_arrays AS (
		    SELECT hour, route, method, status_class, array_agg(n ORDER BY i) AS buckets
		    FROM bucket_sums
		    GROUP BY hour, route, method, status_class
		)
		INSERT INTO request_metrics
		    (period_start, resolution, instance, route, method, status_class,
		     requests, duration_ms_sum, bytes_sum, buckets)
		SELECT t.hour, 'hour', '', t.route, t.method, t.status_class,
		    t.requests, t.duration_ms_sum, t.bytes_sum, b.buckets
		FROM totals t
		JOIN bucket_arrays b USING (hour, route, method, status_class)
		ON CONFLICT DO NOTHING`, before)
	return err
}

// DeleteBefore deletes minute request rows that started before minutes,
// hourly ones before hours, and runtime rows before runtime.
func (r *MetricsRepository) DeleteBefore(ctx context.Context, minutes, hours, runtime time.Time) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM request_metrics
		WHERE (resolution = 'minute' AND period_start < $1)
		   OR (resolution = 'hour' AND period_start < $2)`, minutes, hours)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `DELETE FROM runtime_metrics WHERE period_start < $1`, runtime)
	return err
}
