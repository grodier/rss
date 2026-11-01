package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/seed"
)

// SeedRepository queues the lookups of the seed list (internal/seed).
type SeedRepository struct {
	DB *sql.DB
}

func NewSeedRepository(db *sql.DB) *SeedRepository {
	return &SeedRepository{DB: db}
}

// Queue queues a lookup of each entry, at priority 1 so the lookup workers
// take readers' lookups first, if its site key has no lookups row, and
// re-queues, with the entry's address, each entry's lookup that failed or
// found no feeds and finished over retryAfter ago. A lookup that found a site
// (even if a later run failed), or is pending or running, is left as it is,
// so a deploy doesn't fetch sites already found. It also returns the entries
// whose lookups failed or found no feeds too recently to retry.
func (r *SeedRepository) Queue(ctx context.Context, entries []seed.Entry, retryAfter time.Duration) (seed.Result, error) {
	var res seed.Result
	keys := make([]string, len(entries))
	urls := make([]string, len(entries))
	for i, e := range entries {
		keys[i], urls[i] = e.SiteKey, e.URL
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	// xmax is 0 on a row this statement inserted, and set on one it updated.
	stmt := `INSERT INTO lookups (site_key, url, priority)
		SELECT site_key, url, 1
		FROM unnest($1::text[], $2::text[]) WITH ORDINALITY AS t(site_key, url, ord)
		ORDER BY ord
		ON CONFLICT (site_key) DO UPDATE SET
			status = 'pending', url = EXCLUDED.url, error = '', priority = 1,
			attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
		WHERE lookups.site_id IS NULL AND lookups.status IN ('done', 'failed')
		  AND lookups.finished_at < now() - make_interval(secs => $3)
		RETURNING site_key, xmax = 0`
	rows, err := tx.QueryContext(ctx, stmt, pq.Array(keys), pq.Array(urls), retryAfter.Seconds())
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var key string
		var inserted bool
		if err := rows.Scan(&key, &inserted); err != nil {
			rows.Close()
			return res, err
		}
		if inserted {
			res.Queued++
		} else {
			res.Retried = append(res.Retried, key)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT l.site_key
		FROM unnest($1::text[]) WITH ORDINALITY AS t(site_key, ord)
		JOIN lookups l ON l.site_key = t.site_key
		WHERE l.site_id IS NULL AND l.status IN ('done', 'failed')
		ORDER BY t.ord`, pq.Array(keys))
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return res, err
		}
		res.Missing = append(res.Missing, key)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	if err := tx.Commit(); err != nil {
		return seed.Result{}, err
	}
	return res, nil
}
