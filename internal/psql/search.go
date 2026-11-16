package psql

import (
	"context"
	"database/sql"
	"fmt"
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

// prefixPattern escapes LIKE wildcards in q for a match at the start.
func prefixPattern(q string) string {
	return likeEscaper.Replace(q) + "%"
}

// wordPattern escapes LIKE wildcards in q for a match at the start of a
// word: after a space, in a text with a space put in front of it.
func wordPattern(q string) string {
	return "% " + likeEscaper.Replace(q) + "%"
}

// matchOrder is an ORDER BY's terms ranking name by how well it matches a
// query: all of it ($n), its start ($n+1, a prefixPattern), the start of a
// word in it ($n+2, a wordPattern), then the rest. It is the order of
// server.matchRank, so a search's best matches are within its limit.
func matchOrder(name string, n int) string {
	return fmt.Sprintf(`lower(%[1]s) = lower($%[2]d) DESC,
		%[1]s ILIKE $%[3]d ESCAPE '\' DESC,
		(' ' || %[1]s) ILIKE $%[4]d ESCAPE '\' DESC`, name, n, n+1, n+2)
}

// Search matches q (case-insensitive substring) against site title, host and
// description and feed title, URL and description, and siteKey (discovery.SiteKey of q when q is a web
// address, else "") against site host exactly; that site comes first, then
// the sites whose name (title, else host) matches best (matchOrder), then by
// title. It returns at most limit sites, each with all of its feeds.
func (r *SearchRepository) Search(ctx context.Context, q, siteKey string, limit int) ([]rss.SiteWithFeeds, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	stmt := `SELECT s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at
		FROM sites s
		WHERE s.host = $2 OR s.title ILIKE $1 ESCAPE '\' OR s.host ILIKE $1 ESCAPE '\'
		   OR s.description ILIKE $1 ESCAPE '\'
		   OR EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id
		              AND (f.title ILIKE $1 ESCAPE '\' OR f.url ILIKE $1 ESCAPE '\'
		                   OR f.description ILIKE $1 ESCAPE '\'))
		ORDER BY (s.host = $2) DESC, ` + matchOrder(`COALESCE(NULLIF(s.title, ''), s.host)`, 4) + `,
			lower(s.title), s.host
		LIMIT $3`

	rows, err := r.DB.QueryContext(ctx, stmt, likePattern(q), siteKey, limit, q, prefixPattern(q), wordPattern(q))
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
