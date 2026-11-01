package psql

import (
	"context"
	"database/sql"

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

// Replace replaces the stored catalog with entries, then queues a lookup for
// each entry whose site key has no lookups row yet, in one transaction. A
// site key's existing lookup, whatever its status, is left as it is, so a
// restart doesn't fetch sites already looked up. It returns how many lookups
// it queued.
func (r *CatalogRepository) Replace(ctx context.Context, entries []catalog.Entry) (int, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Two instances starting at once replace the catalog one after the
	// other; reads (Browse, Search) aren't blocked.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE catalog_sites IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_sites`); err != nil {
		return 0, err
	}

	keys := make([]string, len(entries))
	urls := make([]string, len(entries))
	topics := make([]string, len(entries))
	positions := make([]int64, len(entries))
	for i, e := range entries {
		keys[i], urls[i], topics[i], positions[i] = e.SiteKey, e.URL, e.Topic, int64(e.Position)
	}
	stmt := `INSERT INTO catalog_sites (site_key, url, topic, position)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::int[])`
	if _, err := tx.ExecContext(ctx, stmt, pq.Array(keys), pq.Array(urls), pq.Array(topics), pq.Array(positions)); err != nil {
		return 0, err
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO lookups (site_key, url)
		SELECT site_key, url FROM catalog_sites ORDER BY position
		ON CONFLICT (site_key) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	queued, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(queued), nil
}

// Browse returns the catalog's topics in file order, each with its sites in
// file order. A site is the one its catalog entry's lookup found, so it's
// right even when the address redirected to another host; entries whose
// lookup hasn't found a site (yet) are left out, and so are topics with
// none.
func (r *CatalogRepository) Browse(ctx context.Context) ([]rss.CatalogTopic, error) {
	stmt := `SELECT c.topic, s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at
		FROM catalog_sites c
		JOIN lookups l ON l.site_key = c.site_key
		JOIN sites s ON s.id = l.site_id
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
