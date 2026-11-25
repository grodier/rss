package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
	"github.com/lib/pq"
)

// adminSeedFrom is the seed list's entries ss joined to their section
// hint sec, their site key's lookup l and, for one that found a site, that site s with its feeds and
// catalog feeds counted in fc, in one grouped pass over the feeds of the
// list's sites rather than two counts per row.
const adminSeedFrom = `
	FROM seed_sites ss
	LEFT JOIN discover_sections sec ON sec.id = ss.section_id
	LEFT JOIN lookups l ON l.site_key = ss.site_key
	LEFT JOIN sites s ON s.id = l.site_id
	LEFT JOIN (
		SELECT f.site_id, count(*) AS feeds, count(d.feed_id) AS in_discover
		FROM feeds f LEFT JOIN discover_feeds d ON d.feed_id = f.id
		WHERE f.site_id IN (SELECT sl.site_id FROM seed_sites sss JOIN lookups sl ON sl.site_key = sss.site_key)
		GROUP BY f.site_id
	) fc ON fc.site_id = s.id`

// adminSeedNotFound is the condition that lookup l failed or finished
// without finding a site: the Not found filter's entries, which
// RetrySeedLookups looks up again. A lookup that found a site before and
// failed since keeps its site_id, and is one.
const adminSeedNotFound = `(l.status = 'failed' OR (l.status = 'done' AND l.site_id IS NULL))`

// adminSeedFilters are the seed list's filters' conditions on adminSeedFrom.
var adminSeedFilters = map[string]string{
	rss.AdminSeedAll:           `true`,
	rss.AdminSeedNotFound:      adminSeedNotFound,
	rss.AdminSeedWaiting:       `(l.id IS NULL OR l.status IN ('pending', 'running'))`,
	rss.AdminSeedNotInDiscover: `(l.status = 'done' AND l.site_id IS NOT NULL AND COALESCE(fc.in_discover, 0) = 0)`,
}

// CountSeed counts the seed list's entries each filter keeps, by filter,
// in one statement.
func (r *AdminFeedRepository) CountSeed(ctx context.Context) (map[string]int, error) {
	keys := slices.Sorted(maps.Keys(adminSeedFilters))
	cols := make([]string, len(keys))
	for i, k := range keys {
		cols[i] = `count(*) FILTER (WHERE ` + adminSeedFilters[k] + `)`
	}
	stmt := `SELECT ` + strings.Join(cols, ", ") + adminSeedFrom

	counts := make([]int, len(keys))
	dest := make([]any, len(keys))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := r.DB.QueryRowContext(ctx, stmt).Scan(dest...); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(keys))
	for i, k := range keys {
		out[k] = counts[i]
	}
	return out, nil
}

// SeedList returns a page of the seed list's entries q's filter keeps,
// newest first (ties by site key, descending), starting after the entry
// with site key q.After, each with its site key's lookup, the site that
// lookup found, that site's feeds and how many of them are in Discover's
// catalog. An entry whose site key has no lookup has a Lookup with ID "".
// An unknown filter is an error.
func (r *AdminFeedRepository) SeedList(ctx context.Context, q rss.AdminSeedQuery) ([]rss.AdminSeedEntry, error) {
	filter, ok := adminSeedFilters[q.Filter]
	if !ok {
		return nil, fmt.Errorf("unknown seed filter %q", q.Filter)
	}
	args := []any{q.Limit}
	where := []string{filter}
	if q.After != "" {
		args = append(args, q.After)
		where = append(where, fmt.Sprintf(`(ss.added_at, ss.site_key) < (SELECT c.added_at, c.site_key FROM seed_sites c WHERE c.site_key = $%d)`, len(args)))
	}
	stmt := `SELECT ss.site_key, ss.url, COALESCE(sec.name, ''), ss.source, ss.added_at, COALESCE(l.id::text, ''), COALESCE(l.url, ''), COALESCE(l.status, ''),
			COALESCE(l.site_id::text, ''), COALESCE(l.error, ''), COALESCE(l.attempts, 0),
			l.requested_at, l.started_at, l.finished_at, COALESCE(s.host, ''),
			COALESCE(fc.feeds, 0), COALESCE(fc.in_discover, 0)` + adminSeedFrom + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ss.added_at DESC, ss.site_key DESC
		LIMIT $1`
	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminSeedEntry
	for rows.Next() {
		var e rss.AdminSeedEntry
		l := &e.Lookup
		var requested, started, finished sql.NullTime
		if err := rows.Scan(&e.SiteKey, &e.URL, &e.SectionName, &e.Source, &e.AddedAt, &l.ID, &l.URL, &l.Status, &l.SiteID, &l.Error, &l.Attempts,
			&requested, &started, &finished, &e.SiteHost, &e.Feeds, &e.InDiscover); err != nil {
			return nil, err
		}
		if l.ID != "" {
			l.SiteKey = e.SiteKey
		}
		l.RequestedAt, l.StartedAt, l.FinishedAt = requested.Time, started.Time, finished.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueueSeedLookup queues a lookup of the seed list's entry with siteKey,
// of its own address, at a reader's priority, as CheckForFeeds does for a
// site, however recently it last ran, and returns it; a lookup already
// pending or running is returned as it is. It's recorded as seed.lookup by
// by, with the entry's address and the lookup's ID and status. A site key
// that isn't on the list is rss.ErrNoRecord.
func (r *AdminFeedRepository) QueueSeedLookup(ctx context.Context, siteKey string, by rss.AuditActor) (rss.Lookup, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.Lookup{}, err
	}
	defer tx.Rollback()

	var u string
	err = tx.QueryRowContext(ctx, `SELECT url FROM seed_sites WHERE site_key = $1`, siteKey).Scan(&u)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Lookup{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.Lookup{}, err
	}
	// No time-to-live, so a finished lookup always runs again.
	l, err := requestLookup(ctx, tx, siteKey, u, isHomePage(u), 0, 0)
	if err != nil {
		return rss.Lookup{}, err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "seed.lookup", TargetType: "seed", TargetID: siteKey,
		After: map[string]any{"url": u, "lookup_id": l.ID, "status": l.Status}})
	if err != nil {
		return rss.Lookup{}, err
	}
	return l, tx.Commit()
}

// SeedOnList returns which of siteKeys are on the seed list, for an
// upload's preview.
func (r *AdminFeedRepository) SeedOnList(ctx context.Context, siteKeys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(siteKeys) == 0 {
		return out, nil
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT site_key FROM seed_sites WHERE site_key = ANY($1)`, pq.Array(siteKeys))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}

// AddSeedSites adds the upload's lines that can be added (seed.LineAdd;
// the others are skipped) to the seed list, with their section hints and
// source, added by by, and queues a lookup of each one added at the seed's
// priority (1, behind readers'): a new one, or a re-queue of one that
// failed or found no feeds, however long ago it ran; a lookup that found a
// site, or is pending or running, is left as it is. A site already on the
// list, perhaps added since the preview, isn't added again or counted, and
// a section deleted since is no hint. It's recorded as one seed.add by by,
// with the source, the count and the site keys added, and returns how many
// were added; adding none records nothing. All of it is one transaction.
func (r *AdminFeedRepository) AddSeedSites(ctx context.Context, lines []seed.Line, source string, by rss.AuditActor) (int, error) {
	var keys, urls, sections []string
	var homePages []bool
	for _, l := range lines {
		if l.Outcome != seed.LineAdd {
			continue
		}
		keys, urls, sections = append(keys, l.SiteKey), append(urls, l.URL), append(sections, l.SectionID)
		homePages = append(homePages, isHomePage(l.URL))
	}
	if len(keys) == 0 {
		return 0, nil
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt := `WITH input AS (
			SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $6::bool[]) WITH ORDINALITY AS t(site_key, url, section_id, home_page, n)
		), added AS (
			INSERT INTO seed_sites (site_key, url, section_id, source, added_by)
			SELECT i.site_key, i.url, ds.id, $4, NULLIF($5, '')::uuid
			FROM input i LEFT JOIN discover_sections ds ON ds.id::text = i.section_id
			ORDER BY i.n
			ON CONFLICT (site_key) DO NOTHING
			RETURNING site_key, url
		), queued AS (
			INSERT INTO lookups (site_key, url, home_page, priority)
			SELECT a.site_key, a.url, i.home_page, 1 FROM added a JOIN input i ON i.site_key = a.site_key
			ON CONFLICT (site_key) DO UPDATE SET
				status = 'pending', url = EXCLUDED.url, home_page = EXCLUDED.home_page, error = '', priority = 1,
				attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
			WHERE lookups.status = 'failed' OR (lookups.status = 'done' AND lookups.site_id IS NULL)
		)
		SELECT a.site_key FROM added a JOIN input i ON i.site_key = a.site_key ORDER BY i.n`
	rows, err := tx.QueryContext(ctx, stmt, pq.Array(keys), pq.Array(urls), pq.Array(sections), source, by.AdminID, pq.Array(homePages))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var added []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return 0, err
		}
		added = append(added, key)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(added) == 0 {
		return 0, nil
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "seed.add", TargetType: "seed",
		After: map[string]any{"source": source, "count": len(added), "site_keys": added}})
	if err != nil {
		return 0, err
	}
	return len(added), tx.Commit()
}

// RetrySeedLookups re-queues, with each entry's own address and at the
// seed's priority (1, behind readers'), the lookup of every seed list entry
// that failed or finished without finding a site (the Not found filter's),
// leaving pending, running and found ones alone, and returns how many. It's
// recorded as seed.retry by by, with the count; when there was none to
// retry, nothing is recorded.
func (r *AdminFeedRepository) RetrySeedLookups(ctx context.Context, by rss.AuditActor) (int, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Whether an entry's address is its site's home page is read in Go
	// (discovery.IsHomePage), so the entries are locked and read first.
	rows, err := tx.QueryContext(ctx, `SELECT ss.site_key, ss.url
		FROM seed_sites ss JOIN lookups l ON l.site_key = ss.site_key
		WHERE `+adminSeedNotFound+`
		FOR UPDATE OF l`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var keys, urls []string
	var homePages []bool
	for rows.Next() {
		var key, u string
		if err := rows.Scan(&key, &u); err != nil {
			return 0, err
		}
		keys, urls, homePages = append(keys, key), append(urls, u), append(homePages, isHomePage(u))
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}

	stmt := `WITH retried AS (
			UPDATE lookups l SET status = 'pending', url = i.url, home_page = i.home_page, error = '', priority = 1,
				attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
			FROM unnest($1::text[], $2::text[], $3::bool[]) AS i(site_key, url, home_page)
			WHERE l.site_key = i.site_key AND ` + adminSeedNotFound + `
			RETURNING 1)
		SELECT count(*) FROM retried`
	var n int
	if err := tx.QueryRowContext(ctx, stmt, pq.Array(keys), pq.Array(urls), pq.Array(homePages)).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "seed.retry", TargetType: "seed",
		After: map[string]any{"count": n}})
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
