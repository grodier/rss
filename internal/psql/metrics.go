package psql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/lib/pq"
)

// MetricsRepository stores www's request, background job and runtime metrics
// (request_metrics, job_metrics, runtime_metrics).
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

// SaveJobs saves minute rows. A row whose period, instance, runner and
// outcome are already there adds to it, as SaveRequests does.
func (r *MetricsRepository) SaveJobs(ctx context.Context, rows []metrics.JobRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO job_metrics
		    (period_start, resolution, instance, runner, outcome, jobs, duration_ms_sum, buckets)
		VALUES ($1, 'minute', $2, $3, $4, $5, $6, $7)
		ON CONFLICT (resolution, period_start, instance, runner, outcome) DO UPDATE SET
		    jobs = job_metrics.jobs + EXCLUDED.jobs,
		    duration_ms_sum = job_metrics.duration_ms_sum + EXCLUDED.duration_ms_sum,
		    buckets = ARRAY(
		        SELECT coalesce(a, 0) + coalesce(b, 0)
		        FROM unnest(job_metrics.buckets, EXCLUDED.buckets) WITH ORDINALITY AS u(a, b, i)
		        ORDER BY i
		    )`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		_, err := stmt.ExecContext(ctx, row.PeriodStart, row.Instance, row.Runner, row.Outcome,
			row.Jobs, row.DurationMsSum, pq.Array(row.Buckets))
		if err != nil {
			return fmt.Errorf("save job metrics for %s %s: %w", row.Runner, row.Outcome, err)
		}
	}
	return tx.Commit()
}

// ListJobs returns the job rows of resolution ("minute" or "hour") whose
// period starts in [from, to), oldest first.
func (r *MetricsRepository) ListJobs(ctx context.Context, resolution string, from, to time.Time) ([]metrics.JobRow, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT period_start, instance, runner, outcome, jobs, duration_ms_sum, buckets
		FROM job_metrics
		WHERE resolution = $1 AND period_start >= $2 AND period_start < $3
		ORDER BY period_start, instance, runner, outcome`, resolution, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []metrics.JobRow
	for rows.Next() {
		var m metrics.JobRow
		err := rows.Scan(&m.PeriodStart, &m.Instance, &m.Runner, &m.Outcome, &m.Jobs, &m.DurationMsSum, pq.Array(&m.Buckets))
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

// RollUp sums the minute rows of each whole hour before before into hourly
// rows (instance ”), adding buckets element-wise: one per route, method and
// status class in request_metrics, and one per runner and outcome in
// job_metrics. In each table it starts after the newest hour already rolled
// up, and leaves an hour that has its rows alone, so instances can run it
// side by side and it can run again.
func (r *MetricsRepository) RollUp(ctx context.Context, before time.Time) error {
	// Whole hours only: rolling up part of an hour would leave the rest out
	// for good, since an hour that has rows is never rolled up again.
	before = before.UTC().Truncate(time.Hour)
	if err := r.rollUpRequests(ctx, before); err != nil {
		return fmt.Errorf("roll up request metrics: %w", err)
	}
	if err := r.rollUpJobs(ctx, before); err != nil {
		return fmt.Errorf("roll up job metrics: %w", err)
	}
	return nil
}

func (r *MetricsRepository) rollUpRequests(ctx context.Context, before time.Time) error {
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

func (r *MetricsRepository) rollUpJobs(ctx context.Context, before time.Time) error {
	_, err := r.DB.ExecContext(ctx, `WITH minutes AS (
		    SELECT date_trunc('hour', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour,
		        runner, outcome, jobs, duration_ms_sum, buckets
		    FROM job_metrics
		    WHERE resolution = 'minute'
		      AND period_start < $1
		      AND period_start >= coalesce(
		          (SELECT max(period_start) + interval '1 hour' FROM job_metrics WHERE resolution = 'hour'),
		          '-infinity')
		), totals AS (
		    SELECT hour, runner, outcome, sum(jobs)::int AS jobs, sum(duration_ms_sum) AS duration_ms_sum
		    FROM minutes
		    GROUP BY hour, runner, outcome
		), bucket_sums AS (
		    SELECT hour, runner, outcome, u.i, sum(u.n)::int AS n
		    FROM minutes, unnest(minutes.buckets) WITH ORDINALITY AS u(n, i)
		    GROUP BY hour, runner, outcome, u.i
		), bucket_arrays AS (
		    SELECT hour, runner, outcome, array_agg(n ORDER BY i) AS buckets
		    FROM bucket_sums
		    GROUP BY hour, runner, outcome
		)
		INSERT INTO job_metrics
		    (period_start, resolution, instance, runner, outcome, jobs, duration_ms_sum, buckets)
		SELECT t.hour, 'hour', '', t.runner, t.outcome, t.jobs, t.duration_ms_sum, b.buckets
		FROM totals t
		JOIN bucket_arrays b USING (hour, runner, outcome)
		ON CONFLICT DO NOTHING`, before)
	return err
}

// DeleteBefore deletes minute request and job rows that started before
// minutes, hourly ones before hours, and runtime rows before runtime.
func (r *MetricsRepository) DeleteBefore(ctx context.Context, minutes, hours, runtime time.Time) error {
	for _, table := range []string{"request_metrics", "job_metrics"} {
		_, err := r.DB.ExecContext(ctx, `DELETE FROM `+table+`
			WHERE (resolution = 'minute' AND period_start < $1)
			   OR (resolution = 'hour' AND period_start < $2)`, minutes, hours)
		if err != nil {
			return err
		}
	}
	_, err := r.DB.ExecContext(ctx, `DELETE FROM runtime_metrics WHERE period_start < $1`, runtime)
	return err
}
