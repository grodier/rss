package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/grodier/rss/internal/rss"
)

// maxErrorLen caps the error messages stored by LookupRepository.Fail and
// FeedRepository.RecordFailure, in bytes.
const maxErrorLen = 1000

const lookupColumns = `id, site_key, url, status, site_id, error, attempts, requested_at, started_at, finished_at`

type LookupRepository struct {
	DB *sql.DB
}

func NewLookupRepository(db *sql.DB) *LookupRepository {
	return &LookupRepository{DB: db}
}

// Request returns the lookup for siteKey, creating it as pending if it
// doesn't exist, or resetting it to pending if it is done and older than
// doneTTL or failed and older than failedTTL. A pending/running or still-fresh
// lookup is returned unchanged. A reset keeps site_id, so the site page keeps
// working while the re-check runs.
func (r *LookupRepository) Request(ctx context.Context, siteKey, url string, doneTTL, failedTTL time.Duration) (rss.Lookup, error) {
	stmt := `INSERT INTO lookups (site_key, url) VALUES ($1, $2)
		ON CONFLICT (site_key) DO UPDATE SET
			status = 'pending', url = EXCLUDED.url, error = '',
			attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
		WHERE (lookups.status = 'done' AND lookups.finished_at < now() - make_interval(secs => $3))
			OR (lookups.status = 'failed' AND lookups.finished_at < now() - make_interval(secs => $4))
		RETURNING ` + lookupColumns

	l, err := scanLookup(r.DB.QueryRowContext(ctx, stmt, siteKey, url, doneTTL.Seconds(), failedTTL.Seconds()))
	if errors.Is(err, rss.ErrNoRecord) {
		// The row exists and is still in progress or fresh.
		return r.GetBySiteKey(ctx, siteKey)
	}
	return l, err
}

func (r *LookupRepository) GetByID(ctx context.Context, id string) (rss.Lookup, error) {
	stmt := `SELECT ` + lookupColumns + ` FROM lookups WHERE id = $1`
	return scanLookup(r.DB.QueryRowContext(ctx, stmt, id))
}

func (r *LookupRepository) GetBySiteKey(ctx context.Context, siteKey string) (rss.Lookup, error) {
	stmt := `SELECT ` + lookupColumns + ` FROM lookups WHERE site_key = $1`
	return scanLookup(r.DB.QueryRowContext(ctx, stmt, siteKey))
}

// ClaimNext marks the oldest pending lookup running and returns it.
// rss.ErrNoRecord when the queue is empty.
func (r *LookupRepository) ClaimNext(ctx context.Context) (rss.Lookup, error) {
	stmt := `UPDATE lookups SET status = 'running', started_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT id FROM lookups WHERE status = 'pending'
			ORDER BY requested_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING ` + lookupColumns

	return scanLookup(r.DB.QueryRowContext(ctx, stmt))
}

// Finish marks a lookup done. siteID is "" when no feeds were found; an
// existing site_id is then kept rather than orphaning the site page.
func (r *LookupRepository) Finish(ctx context.Context, id, siteID string) error {
	stmt := `UPDATE lookups SET status = 'done', error = '', finished_at = now(),
			site_id = COALESCE(NULLIF($2, '')::uuid, site_id)
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, siteID)
}

// Fail marks a lookup failed with an internal error message, truncated to
// maxErrorLen bytes.
func (r *LookupRepository) Fail(ctx context.Context, id, msg string) error {
	stmt := `UPDATE lookups SET status = 'failed', error = $2, finished_at = now()
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, truncateUTF8(msg, maxErrorLen))
}

// ResetStale puts running lookups started more than olderThan ago back to
// pending (worker crashed), or marks them failed once attempts >= maxAttempts.
// Returns how many rows changed.
func (r *LookupRepository) ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error) {
	stmt := `UPDATE lookups SET
			status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
			error = CASE WHEN attempts >= $2 THEN 'stale: gave up after too many attempts' ELSE error END,
			finished_at = CASE WHEN attempts >= $2 THEN now() ELSE NULL END,
			started_at = CASE WHEN attempts >= $2 THEN started_at ELSE NULL END
		WHERE status = 'running' AND started_at < now() - make_interval(secs => $1)`

	res, err := r.DB.ExecContext(ctx, stmt, olderThan.Seconds(), maxAttempts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// execOne runs stmt and returns rss.ErrNoRecord if it changed no rows.
func execOne(ctx context.Context, q querier, stmt string, args ...any) error {
	res, err := q.ExecContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return rss.ErrNoRecord
	}
	return nil
}

func scanLookup(row *sql.Row) (rss.Lookup, error) {
	var (
		l          rss.Lookup
		siteID     sql.NullString
		startedAt  sql.NullTime
		finishedAt sql.NullTime
	)
	err := row.Scan(&l.ID, &l.SiteKey, &l.URL, &l.Status, &siteID, &l.Error, &l.Attempts, &l.RequestedAt, &startedAt, &finishedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Lookup{}, rss.ErrNoRecord
		}
		return rss.Lookup{}, err
	}
	l.SiteID = siteID.String
	l.StartedAt = startedAt.Time
	l.FinishedAt = finishedAt.Time
	return l, nil
}

// truncateUTF8 shortens s to at most n bytes without splitting a rune, so the
// result stays valid UTF-8 for Postgres.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
