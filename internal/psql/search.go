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

// ownName is the SQL for a site's own name, for grouping (Search): its
// title without the platform's suffix, the last " - ", " – ", " — " or
// " | " and what follows it ("Alice – Medium" -> "Alice"), since a profile
// on a platform is often titled after both. Any last segment goes, not only
// a platform's name: a path-scoped site's title comes from its platform's
// page, whose last segment is the platform, and a list of platforms would
// miss the fediverse's many. Untitled, it's "".
const ownName = `regexp_replace(s.title, '\s+[-–—|]\s+[^-–—|]*$', '')`

// Search matches q (case-insensitive substring) against site title, host and
// description and feed title, URL and description, and siteKey (discovery.SiteKey of q when q is a web
// address, else "") against site host exactly; that site comes first, then
// the sites whose name (title, else host) matches best (matchOrder), then by
// title. It returns at most limit sites, each with all of its feeds.
//
// Sites on a shared host (a key with a path, "bsky.app/profile/alice")
// that matched only through their key, handle, platform or feeds are
// grouped by host and counted instead (#739), so searching a platform's
// name doesn't list every profile on it: a site is grouped unless it is
// siteKey or its own name (ownName) matches q at its start or a word's
// start. A host with one such site keeps it as a site: there are no groups
// of one. Groups come largest first. With host set, only the sites whose
// key's host is host come back, none grouped.
func (r *SearchRepository) Search(ctx context.Context, q, siteKey, host string, limit int) ([]rss.SiteWithFeeds, []rss.SiteGroup, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil, nil
	}

	// matched is every matching site, with the host of its key and whether
	// it would be grouped; grouped the hosts with more than one of those.
	// The sites (kind 0) are numbered in their order, then the groups
	// (kind 1) follow.
	stmt := `WITH matched AS (
			SELECT s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at,
				split_part(s.host, '/', 1) AS key_host,
				$7 = '' AND position('/' in s.host) > 0 AND s.host <> $2
					AND NOT (` + ownName + ` ILIKE $5 ESCAPE '\' OR (' ' || ` + ownName + `) ILIKE $6 ESCAPE '\') AS groupable
			FROM sites s
			WHERE (s.host = $2 OR s.title ILIKE $1 ESCAPE '\' OR s.host ILIKE $1 ESCAPE '\'
			   OR s.description ILIKE $1 ESCAPE '\'
			   OR EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id
			              AND (f.title ILIKE $1 ESCAPE '\' OR f.url ILIKE $1 ESCAPE '\'
			                   OR f.description ILIKE $1 ESCAPE '\')))
			  AND ($7 = '' OR split_part(s.host, '/', 1) = $7)
		), grouped AS (
			SELECT key_host, count(*) AS n FROM matched WHERE groupable
			GROUP BY key_host HAVING count(*) > 1
		)
		SELECT * FROM (
			SELECT 0 AS kind, row_number() OVER (ORDER BY (m.host = $2) DESC, ` + matchOrder(`COALESCE(NULLIF(m.title, ''), m.host)`, 4) + `,
					lower(m.title), m.host) AS pos,
				m.id::text, m.host, m.url, m.title, m.description, m.created_at, m.updated_at, 0::bigint AS n
			FROM matched m
			WHERE NOT (m.groupable AND m.key_host IN (SELECT key_host FROM grouped))
			ORDER BY pos
			LIMIT $3
		) sites
		UNION ALL
		SELECT 1, row_number() OVER (ORDER BY n DESC, key_host), '', key_host, '', '', '', now(), now(), n
		FROM grouped
		ORDER BY kind, pos`

	rows, err := r.DB.QueryContext(ctx, stmt, likePattern(q), siteKey, limit, q, prefixPattern(q), wordPattern(q), host)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var results []rss.SiteWithFeeds
	var groups []rss.SiteGroup
	var ids []string
	index := map[string]int{}
	for rows.Next() {
		var kind, pos, n int
		var s rss.Site
		if err := rows.Scan(&kind, &pos, &s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt, &n); err != nil {
			return nil, nil, err
		}
		if kind == 1 {
			groups = append(groups, rss.SiteGroup{Host: s.Host, Count: n})
			continue
		}
		index[s.ID] = len(results)
		ids = append(ids, s.ID)
		results = append(results, rss.SiteWithFeeds{Site: s})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return nil, groups, nil
	}

	feedStmt := `SELECT id, site_id, url, site_url, title, description, created_at
		FROM feeds
		WHERE site_id = ANY($1)
		ORDER BY site_id, lower(title), url`

	frows, err := r.DB.QueryContext(ctx, feedStmt, pq.Array(ids))
	if err != nil {
		return nil, nil, err
	}
	defer frows.Close()

	for frows.Next() {
		var f rss.Feed
		if err := frows.Scan(&f.ID, &f.SiteID, &f.Url, &f.SiteUrl, &f.Title, &f.Description, &f.CreatedAt); err != nil {
			return nil, nil, err
		}
		i := index[f.SiteID]
		results[i].Feeds = append(results[i].Feeds, f)
	}
	if err := frows.Err(); err != nil {
		return nil, nil, err
	}

	return results, groups, nil
}
