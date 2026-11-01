package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/catalog"
	"github.com/grodier/rss/internal/rss"
)

// CatalogRepository stores the site catalog (catalog_sites) and reads it
// for Browse.
type CatalogRepository struct {
	DB *sql.DB
}

func NewCatalogRepository(db *sql.DB) *CatalogRepository {
	return &CatalogRepository{DB: db}
}

// Replace replaces the stored catalog with entries, then, in the same
// transaction, queues a lookup, behind readers' (priority 1), for each entry
// whose site key has no lookups row, and re-queues, with the catalog's address, each entry's lookup that
// failed or found no feeds and finished over retryAfter ago. A lookup that
// found a site (even if a later run failed), or is pending or running, is
// left as it is, so a deploy doesn't fetch sites already found.
func (r *CatalogRepository) Replace(ctx context.Context, entries []catalog.Entry, retryAfter time.Duration) (catalog.Result, error) {
	var res catalog.Result
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	// Two runs at once replace the catalog one after the other; reads
	// (Browse, Search) aren't blocked.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE catalog_sites IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_sites`); err != nil {
		return res, err
	}

	keys := make([]string, len(entries))
	urls := make([]string, len(entries))
	topics := make([]string, len(entries))
	positions := make([]int64, len(entries))
	browse := make([]bool, len(entries))
	for i, e := range entries {
		keys[i], urls[i], topics[i], positions[i], browse[i] = e.SiteKey, e.URL, e.Topic, int64(e.Position), e.Browse
	}
	stmt := `INSERT INTO catalog_sites (site_key, url, topic, position, browse)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::int[], $5::bool[])`
	if _, err := tx.ExecContext(ctx, stmt, pq.Array(keys), pq.Array(urls), pq.Array(topics), pq.Array(positions), pq.Array(browse)); err != nil {
		return res, err
	}

	// The catalog's lookups have priority 1, so the lookup workers take
	// readers' first. xmax is 0 on a row this statement inserted, and set on
	// one it updated.
	stmt = `INSERT INTO lookups (site_key, url, priority)
		SELECT site_key, url, 1 FROM catalog_sites ORDER BY position
		ON CONFLICT (site_key) DO UPDATE SET
			status = 'pending', url = EXCLUDED.url, error = '', priority = 1,
			attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
		WHERE lookups.site_id IS NULL AND lookups.status IN ('done', 'failed')
		  AND lookups.finished_at < now() - make_interval(secs => $1)
		RETURNING site_key, xmax = 0`
	rows, err := tx.QueryContext(ctx, stmt, retryAfter.Seconds())
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

	res.Missing, err = queryStrings(ctx, tx, `SELECT c.site_key FROM catalog_sites c
		JOIN lookups l ON l.site_key = c.site_key
		WHERE l.site_id IS NULL AND l.status IN ('done', 'failed')
		ORDER BY c.position`)
	if err != nil {
		return res, err
	}

	if err := tx.Commit(); err != nil {
		return catalog.Result{}, err
	}
	return res, nil
}

// queryStrings returns the single text column of stmt's rows.
func queryStrings(ctx context.Context, q querier, stmt string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Browse returns the topics of the catalog's Browse list (browse.txt) in file
// order, each with its sites in file order. A site is the one its catalog entry's lookup found, so it's
// right even when the address redirected to another host; entries whose
// lookup hasn't found a site (yet) are left out, and so are topics with
// none.
func (r *CatalogRepository) Browse(ctx context.Context) ([]rss.CatalogTopic, error) {
	stmt := `SELECT c.topic, s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at
		FROM catalog_sites c
		JOIN lookups l ON l.site_key = c.site_key
		JOIN sites s ON s.id = l.site_id
		WHERE c.browse
		ORDER BY c.position`

	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []rss.CatalogTopic
	for rows.Next() {
		var topic string
		var s rss.Site
		if err := rows.Scan(&topic, &s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		// A topic's entries are consecutive in the file (catalog.Parse
		// rejects a topic heading that appears twice).
		if len(topics) == 0 || topics[len(topics)-1].Name != topic {
			topics = append(topics, rss.CatalogTopic{Name: topic})
		}
		t := &topics[len(topics)-1]
		t.Sites = append(t.Sites, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return topics, nil
}
