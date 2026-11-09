package psql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// RUMRepository stores the real-user beacons (rum_views) and their daily
// rollups (rum_daily).
type RUMRepository struct {
	DB *sql.DB
}

func NewRUMRepository(db *sql.DB) *RUMRepository {
	return &RUMRepository{DB: db}
}

// Record stores v. A zero ReceivedAt is the database's now().
func (r *RUMRepository) Record(ctx context.Context, v rss.RUMView) error {
	var receivedAt any
	if !v.ReceivedAt.IsZero() {
		receivedAt = v.ReceivedAt
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO rum_views
		    (received_at, route, nav_type, device, browser, browser_major, installed,
		     connection, protocol, referrer_host,
		     ttfb_ms, fcp_ms, lcp_ms, inp_ms, cls, dcl_ms, load_ms, server_ms, transfer_bytes,
		     lcp_element, inp_target)
		VALUES (coalesce($1, now()), $2, $3, $4, $5, $6, $7, $8, $9, $10,
		    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		receivedAt, v.Route, v.NavType, v.Device, v.Browser, v.BrowserMajor, v.Installed,
		v.Connection, v.Protocol, v.ReferrerHost,
		v.TTFBMs, v.FCPMs, v.LCPMs, v.INPMs, v.CLS, v.DCLMs, v.LoadMs, v.ServerMs, v.TransferBytes,
		v.LCPElement, v.INPTarget)
	return err
}

// rumColumns are the rum_views columns rolled up, by rum_daily metric.
var rumColumns = []struct{ metric, column string }{
	{"ttfb", "ttfb_ms"},
	{"fcp", "fcp_ms"},
	{"lcp", "lcp_ms"},
	{"inp", "inp_ms"},
	{"cls", "cls"},
	{"server", "server_ms"},
}

// rollUpRUMQuery counts one metric's views (the column %[1]s) received in
// [$1, $2) ($1 NULL for every one before $2) into a rum_daily row per UTC
// day, route, device and browser, with views per bucket of $4 (the metric's
// metrics.RUMBounds): a view goes in the bucket of the first bound it isn't
// above, or the last. The bounds are real, like the columns, so a value on a
// bound (CLS 0.1) isn't taken for one above it.
const rollUpRUMQuery = `WITH views AS (
	    SELECT (received_at AT TIME ZONE 'UTC')::date AS day, route, device, browser,
	        (SELECT count(*) FROM unnest($4::real[]) AS bound WHERE %[1]s > bound)::int AS bucket
	    FROM rum_views
	    WHERE %[1]s IS NOT NULL
	      AND received_at < $2
	      AND ($1::timestamptz IS NULL OR received_at >= $1)
	), counts AS (
	    SELECT day, route, device, browser, bucket, count(*)::int AS n
	    FROM views
	    GROUP BY day, route, device, browser, bucket
	), totals AS (
	    SELECT day, route, device, browser, sum(n)::int AS views
	    FROM counts
	    GROUP BY day, route, device, browser
	)
	INSERT INTO rum_daily (day, route, device, browser, metric, views, buckets)
	SELECT t.day, t.route, t.device, t.browser, $3, t.views,
	    ARRAY(
	        SELECT coalesce(c.n, 0)
	        FROM generate_series(0, cardinality($4::real[])) AS i
	        LEFT JOIN counts c
	            ON (c.day, c.route, c.device, c.browser, c.bucket) = (t.day, t.route, t.device, t.browser, i)
	        ORDER BY i)
	FROM totals t
	ON CONFLICT DO NOTHING`

// RollUp counts the views of each whole UTC day before before into one
// rum_daily row per day, route, device, browser and metric, for each metric
// a view reported. It starts after the newest day already rolled up, and
// leaves a day that has its rows alone, so instances can run it side by side
// and it can run again.
func (r *RUMRepository) RollUp(ctx context.Context, before time.Time) error {
	// Whole days only: rolling up part of a day would leave the rest out for
	// good, since a day that has rows is never rolled up again.
	before = before.UTC().Truncate(24 * time.Hour)
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Once for every metric: after the first one's rows, the newest day
	// would be the one being rolled up.
	var from sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT (max(day) + 1)::timestamp AT TIME ZONE 'UTC' FROM rum_daily`).Scan(&from)
	if err != nil {
		return err
	}
	var fromArg any
	if from.Valid {
		fromArg = from.Time
	}
	for _, c := range rumColumns {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(rollUpRUMQuery, c.column),
			fromArg, before, c.metric, pq.Array(metrics.RUMBounds[c.metric]))
		if err != nil {
			return fmt.Errorf("roll up %s: %w", c.metric, err)
		}
	}
	return tx.Commit()
}

// Trim deletes every view but the newest keep, by ID, which follows the
// order they were stored in.
func (r *RUMRepository) Trim(ctx context.Context, keep int) error {
	// The keep-th newest ID, found on the primary key's index; with keep
	// views or fewer, there's none, and nothing is deleted.
	_, err := r.DB.ExecContext(ctx, `DELETE FROM rum_views
		WHERE id < (SELECT id FROM rum_views ORDER BY id DESC OFFSET $1 LIMIT 1)`, keep-1)
	return err
}

// DeleteBefore deletes the views received before views, and the daily rows
// of the UTC days before daily's.
func (r *RUMRepository) DeleteBefore(ctx context.Context, views, daily time.Time) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM rum_views WHERE received_at < $1`, views)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `DELETE FROM rum_daily WHERE day < ($1::timestamptz AT TIME ZONE 'UTC')::date`, daily)
	return err
}
