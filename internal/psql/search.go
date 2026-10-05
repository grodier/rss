package psql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/rss"
)

type SearchRepository struct {
	DB *sql.DB
}

func NewSearchRepository(db *sql.DB) *SearchRepository {
	return &SearchRepository{DB: db}
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern escapes LIKE wildcards in q and wraps it for a substring match.
func likePattern(q string) string {
	return "%" + likeEscaper.Replace(q) + "%"
}

// Search matches q (case-insensitive substring) against site title and host
// and feed title and URL. It returns at most limit sites, each with all of its
// feeds.
func (r *SearchRepository) Search(ctx context.Context, q string, limit int) ([]rss.SiteWithFeeds, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	stmt := `SELECT s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at
		FROM sites s
		WHERE s.title ILIKE $1 ESCAPE '\' OR s.host ILIKE $1 ESCAPE '\'
		   OR EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id
		              AND (f.title ILIKE $1 ESCAPE '\' OR f.url ILIKE $1 ESCAPE '\'))
		ORDER BY (s.host = lower($2)) DESC, lower(s.title), s.host
		LIMIT $3`

	rows, err := r.DB.QueryContext(ctx, stmt, likePattern(q), q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []rss.SiteWithFeeds
	var ids []string
	index := map[string]int{}
	for rows.Next() {
		var s rss.Site
		if err := rows.Scan(&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		index[s.ID] = len(results)
		ids = append(ids, s.ID)
		results = append(results, rss.SiteWithFeeds{Site: s})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	feedStmt := `SELECT id, site_id, url, site_url, title, description, created_at
		FROM feeds
		WHERE site_id = ANY($1)
		ORDER BY site_id, lower(title), url`

	frows, err := r.DB.QueryContext(ctx, feedStmt, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer frows.Close()

	for frows.Next() {
		var f rss.Feed
		if err := frows.Scan(&f.ID, &f.SiteID, &f.Url, &f.SiteUrl, &f.Title, &f.Description, &f.CreatedAt); err != nil {
			return nil, err
		}
		i := index[f.SiteID]
		results[i].Feeds = append(results[i].Feeds, f)
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
