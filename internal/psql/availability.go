package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
	"github.com/lib/pq"
)

// AvailabilityRepository stores the admin's checks of www's /healthcheck
// (availability_checks).
type AvailabilityRepository struct {
	DB *sql.DB
}

func NewAvailabilityRepository(db *sql.DB) *AvailabilityRepository {
	return &AvailabilityRepository{DB: db}
}

// MaxCheckText is the most bytes of a check's status and version that Record
// keeps: the answer is www's, but a misconfigured URL could reach anything.
const MaxCheckText = 100

// Record stores c, with its status and version cut to MaxCheckText bytes.
func (r *AvailabilityRepository) Record(ctx context.Context, c rss.AvailabilityCheck) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO availability_checks (checked_at, ok, status, latency_ms, version)
		VALUES ($1, $2, $3, $4, $5)`,
		c.CheckedAt, c.OK, textcut.Bytes(c.Status, MaxCheckText), c.LatencyMs, textcut.Bytes(c.Version, MaxCheckText))
	return err
}

// Uptime counts the checks made, and those that were ok, in the 24 hours,
// 7 days, 30 days and 90 days before now.
func (r *AvailabilityRepository) Uptime(ctx context.Context, now time.Time) (rss.Uptime, error) {
	var u rss.Uptime
	err := r.DB.QueryRowContext(ctx, `SELECT
		    count(*) FILTER (WHERE checked_at > $1::timestamptz - interval '24 hours'),
		    count(*) FILTER (WHERE ok AND checked_at > $1::timestamptz - interval '24 hours'),
		    count(*) FILTER (WHERE checked_at > $1::timestamptz - interval '7 days'),
		    count(*) FILTER (WHERE ok AND checked_at > $1::timestamptz - interval '7 days'),
		    count(*) FILTER (WHERE checked_at > $1::timestamptz - interval '30 days'),
		    count(*) FILTER (WHERE ok AND checked_at > $1::timestamptz - interval '30 days'),
		    count(*),
		    count(*) FILTER (WHERE ok)
		FROM availability_checks
		WHERE checked_at > $1::timestamptz - interval '90 days' AND checked_at <= $1`, now).
		Scan(&u.Day.Checks, &u.Day.OK, &u.Week.Checks, &u.Week.OK, &u.Month.Checks, &u.Month.OK, &u.Quarter.Checks, &u.Quarter.OK)
	return u, err
}

// Incidents returns the incidents with a failed check since since, newest
// first, at most limit, each from its first failed check, even one before
// since.
func (r *AvailabilityRepository) Incidents(ctx context.Context, since time.Time, limit int) ([]rss.Incident, error) {
	// Gaps and islands: each ok check starts a new span, so a span's failed
	// checks are one incident, ended by the next ok check. Missing minutes
	// start nothing, so they don't split one. Reading from the last ok check
	// at or before since gives an incident under way at since its real
	// start, and leaves out one that ended by then.
	rows, err := r.DB.QueryContext(ctx, `SELECT min(checked_at), max(checked_at), min(next_ok), count(*),
		    array_agg(DISTINCT status ORDER BY status)
		FROM (
		    SELECT checked_at, ok, status,
		        count(*) FILTER (WHERE ok) OVER (ORDER BY checked_at) AS span,
		        min(checked_at) FILTER (WHERE ok)
		            OVER (ORDER BY checked_at ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS next_ok
		    FROM availability_checks
		    WHERE checked_at >= coalesce(
		        (SELECT max(checked_at) FROM availability_checks WHERE ok AND checked_at <= $1), $1)
		) c
		WHERE NOT ok
		GROUP BY span
		ORDER BY 1 DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rss.Incident
	for rows.Next() {
		var in rss.Incident
		var ended sql.NullTime
		if err := rows.Scan(&in.StartedAt, &in.LastFailedAt, &ended, &in.Checks, pq.Array(&in.Statuses)); err != nil {
			return nil, err
		}
		in.EndedAt = ended.Time
		out = append(out, in)
	}
	return out, rows.Err()
}

// LatestVersion returns the newest check that reported a version;
// rss.ErrNoRecord when none has.
func (r *AvailabilityRepository) LatestVersion(ctx context.Context) (rss.AvailabilityCheck, error) {
	var c rss.AvailabilityCheck
	var latency sql.NullFloat64
	err := r.DB.QueryRowContext(ctx, `SELECT checked_at, ok, status, latency_ms, version
		FROM availability_checks
		WHERE version <> ''
		ORDER BY checked_at DESC
		LIMIT 1`).Scan(&c.CheckedAt, &c.OK, &c.Status, &latency, &c.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return c, rss.ErrNoRecord
	}
	if latency.Valid {
		c.LatencyMs = &latency.Float64
	}
	return c, err
}

// VersionsIn returns, for each span, the version the last check in it
// reported; "" for a span with no check that reported one. The service page
// labels a deploy with the version www reported while it was live, since
// Render's records of image-backed deploys carry no commit.
func (r *AvailabilityRepository) VersionsIn(ctx context.Context, spans []rss.Span) ([]string, error) {
	versions := make([]string, len(spans))
	if len(spans) == 0 {
		return versions, nil
	}
	froms := make([]string, len(spans))
	tos := make([]string, len(spans))
	for i, s := range spans {
		froms[i], tos[i] = s.From.UTC().Format(time.RFC3339Nano), s.To.UTC().Format(time.RFC3339Nano)
	}
	// One index scan per span, backwards from its end on the primary key.
	rows, err := r.DB.QueryContext(ctx, `SELECT s.i, c.version
		FROM unnest($1::timestamptz[], $2::timestamptz[]) WITH ORDINALITY AS s(from_at, to_at, i)
		CROSS JOIN LATERAL (
		    SELECT version FROM availability_checks
		    WHERE checked_at >= s.from_at AND checked_at < s.to_at AND version <> ''
		    ORDER BY checked_at DESC
		    LIMIT 1
		) c`, pq.Array(froms), pq.Array(tos))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var v string
		if err := rows.Scan(&i, &v); err != nil {
			return nil, err
		}
		versions[i-1] = v
	}
	return versions, rows.Err()
}

// DeleteBefore deletes the checks made before before.
func (r *AvailabilityRepository) DeleteBefore(ctx context.Context, before time.Time) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM availability_checks WHERE checked_at < $1`, before)
	return err
}
