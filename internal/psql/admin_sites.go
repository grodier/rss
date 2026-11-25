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
)

// Site returns site id's page, or rss.ErrNoRecord: its icon's bookkeeping,
// its lookup (by its host, the lookups' site key) and its feeds by title.
func (r *AdminFeedRepository) Site(ctx context.Context, id string) (rss.AdminSite, error) {
	var out rss.AdminSite
	var err error
	if out.Site, err = NewSiteRepository(r.DB).GetByID(ctx, id); err != nil {
		return rss.AdminSite{}, err
	}
	if out.Icon, err = NewDebugRepository(r.DB).SiteDebug(ctx, id); err != nil {
		return rss.AdminSite{}, err
	}
	out.Lookup, err = NewLookupRepository(r.DB).GetBySiteKey(ctx, out.Site.Host)
	if err != nil && !errors.Is(err, rss.ErrNoRecord) {
		return rss.AdminSite{}, err
	}

	stmt := `SELECT ` + adminFeedRowColumns + `
		FROM feeds f JOIN sites s ON s.id = f.site_id` + adminFeedSection + `
		WHERE f.site_id = $1
		ORDER BY f.title, f.url`
	rows, err := r.DB.QueryContext(ctx, stmt, id)
	if err != nil {
		return rss.AdminSite{}, err
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scanAdminFeedRow(rows)
		if err != nil {
			return rss.AdminSite{}, err
		}
		out.Feeds = append(out.Feeds, row)
	}
	return out, rows.Err()
}

// adminSiteFeeds counts the feeds of the site aliased alias.
func adminSiteFeeds(alias string) string {
	return `(SELECT count(*) FROM feeds sf WHERE sf.site_id = ` + alias + `.id)`
}

// adminSiteSubscribers counts the readers who subscribe to any feed of the
// site aliased alias, each once.
func adminSiteSubscribers(alias string) string {
	return `(SELECT count(DISTINCT ssub.user_id) FROM feeds sf
		JOIN subscriptions ssub ON ssub.feed_id = sf.id WHERE sf.site_id = ` + alias + `.id)`
}

// adminSiteFilters are the site list's filters' conditions on sites s. A
// site's lookup is the one keyed by its host, as Site reads it.
var adminSiteFilters = map[string]string{
	rss.AdminSitesAll:          `true`,
	rss.AdminSitesNoIcon:       `NOT EXISTS (SELECT 1 FROM site_icons ni WHERE ni.site_id = s.id)`,
	rss.AdminSitesLookupFailed: `EXISTS (SELECT 1 FROM lookups fl WHERE fl.site_key = s.host AND fl.status = 'failed')`,
	rss.AdminSitesNoSubscribers: `NOT EXISTS (SELECT 1 FROM feeds sf
		JOIN subscriptions ssub ON ssub.feed_id = sf.id WHERE sf.site_id = s.id)`,
}

// adminSiteSorts are the site list's orders, as adminFeedSorts are the feed
// list's: ties go by ID in the same direction.
var adminSiteSorts = map[string]adminFeedSort{
	rss.AdminSitesBySubscribers: {key: adminSiteSubscribers, desc: true},
	rss.AdminSitesByFeeds:       {key: adminSiteFeeds, desc: true},
	rss.AdminSitesByHost:        {key: func(a string) string { return a + `.host` }},
	rss.AdminSitesByFound:       {key: func(a string) string { return a + `.created_at` }, desc: true},
}

// adminSiteSearch is the condition that sites s match q (any case, in the
// host or title) with q's pattern as argument n.
func adminSiteSearch(n int) string {
	return fmt.Sprintf(`(s.host ILIKE $%[1]d ESCAPE '\' OR s.title ILIKE $%[1]d ESCAPE '\')`, n)
}

// CountSites counts the sites each filter keeps whose host or title
// contains q (any case; "" for every site), by filter, in one pass over the
// sites (see "The admin's site list" in docs/backlog.md).
func (r *AdminFeedRepository) CountSites(ctx context.Context, q string) (map[string]int, error) {
	keys := slices.Sorted(maps.Keys(adminSiteFilters))
	cols := make([]string, len(keys))
	for i, k := range keys {
		cols[i] = `count(*) FILTER (WHERE ` + adminSiteFilters[k] + `)`
	}
	stmt := `SELECT ` + strings.Join(cols, ", ") + `
		FROM sites s
		WHERE $1 = '' OR ` + adminSiteSearch(2)

	counts := make([]int, len(keys))
	dest := make([]any, len(keys))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := r.DB.QueryRowContext(ctx, stmt, q, likePattern(q)).Scan(dest...); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(keys))
	for i, k := range keys {
		out[k] = counts[i]
	}
	return out, nil
}

// ListSites returns a page of the sites q's filter keeps whose host or
// title contains q.Q (any case), in q's order, starting after the site
// q.After, each with its feeds and subscribers counted, its icon (without
// its data) and its lookup. An unknown filter or order is an error. Every
// view reads all the sites (see "The admin's site list" in
// docs/backlog.md).
func (r *AdminFeedRepository) ListSites(ctx context.Context, q rss.AdminSiteQuery) ([]rss.AdminSiteRow, error) {
	filter, ok := adminSiteFilters[q.Filter]
	if !ok {
		return nil, fmt.Errorf("unknown site filter %q", q.Filter)
	}
	order, ok := adminSiteSorts[q.Sort]
	if !ok {
		return nil, fmt.Errorf("unknown site order %q", q.Sort)
	}

	args := []any{q.Limit}
	where := []string{filter}
	if q.Q != "" {
		args = append(args, likePattern(q.Q))
		where = append(where, adminSiteSearch(len(args)))
	}
	dir, cmp := "", ">"
	if order.desc {
		dir, cmp = " DESC", "<"
	}
	if q.After != "" {
		args = append(args, q.After)
		where = append(where, fmt.Sprintf(`(%s, s.id) %s (SELECT %s, c.id FROM sites c WHERE c.id = $%d)`,
			order.key("s"), cmp, order.key("c"), len(args)))
	}
	stmt := `SELECT s.id, s.host, s.url, s.title, s.description, s.created_at, s.updated_at,
			` + adminSiteFeeds("s") + `, ` + adminSiteSubscribers("s") + `,
			i.site_id, i.source_url, i.content_type, i.width, i.height, i.hash, i.etag, i.last_modified, i.fetched_at,
			l.id, l.site_key, l.url, l.status, l.site_id, l.error, l.attempts, l.requested_at, l.started_at, l.finished_at
		FROM sites s
		LEFT JOIN site_icons i ON i.site_id = s.id
		LEFT JOIN lookups l ON l.site_key = s.host
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order.key("s") + dir + `, s.id` + dir + `
		LIMIT $1`

	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminSiteRow
	for rows.Next() {
		var row rss.AdminSiteRow
		s := &row.Site
		var (
			iconSite, source, contentType, hash, etag, lastModified sql.NullString
			width, height                                           sql.NullInt64
			fetched                                                 sql.NullTime
			lookupID, siteKey, lookupURL, status, lookupSite, lerr  sql.NullString
			attempts                                                sql.NullInt64
			requested, started, finished                            sql.NullTime
		)
		if err := rows.Scan(&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt,
			&row.Feeds, &row.Subscribers,
			&iconSite, &source, &contentType, &width, &height, &hash, &etag, &lastModified, &fetched,
			&lookupID, &siteKey, &lookupURL, &status, &lookupSite, &lerr, &attempts, &requested, &started, &finished); err != nil {
			return nil, err
		}
		if iconSite.Valid {
			row.Icon = rss.SiteIcon{
				SiteID: iconSite.String, SourceURL: source.String, ContentType: contentType.String,
				Width: int(width.Int64), Height: int(height.Int64), Hash: hash.String,
				ETag: etag.String, LastModified: lastModified.String, FetchedAt: fetched.Time,
			}
		}
		if lookupID.Valid {
			row.Lookup = rss.Lookup{
				ID: lookupID.String, SiteKey: siteKey.String, URL: lookupURL.String,
				Status: rss.LookupStatus(status.String), SiteID: lookupSite.String, Error: lerr.String,
				Attempts: int(attempts.Int64), RequestedAt: requested.Time, StartedAt: started.Time, FinishedAt: finished.Time,
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SiteIcon returns site id's stored icon with its image, or
// rss.ErrNoRecord when it has none, for the admin's own copy of the
// reader's icon route (the admin shows only its own images).
func (r *AdminFeedRepository) SiteIcon(ctx context.Context, id string) (rss.SiteIcon, error) {
	return NewSiteRepository(r.DB).GetIcon(ctx, id)
}

// CheckIcon makes site id's icon due to be checked now, recorded as
// site.check_icon by by. It also clears icon_checked_at, so the site counts
// as never checked and the icon runner takes it whether or not it has a
// subscribed feed (siteIconDue); the runner sets it again when it records
// the check. rss.ErrNoRecord if the site doesn't exist.
func (r *AdminFeedRepository) CheckIcon(ctx context.Context, id string, by rss.AuditActor) error {
	return r.updateRow(ctx, "site", id, by, "site.check_icon",
		`SELECT icon_checked_at, icon_next_check_at FROM sites WHERE id = $1 FOR UPDATE`,
		`UPDATE sites SET icon_checked_at = NULL, icon_next_check_at = now() WHERE id = $1
			RETURNING icon_checked_at, icon_next_check_at`,
		"icon_checked_at", "icon_next_check_at")
}

// CheckForFeeds queues a lookup of site id's home page at a reader's
// priority, as a reader's lookup does (LookupRepository.Request), however
// recently it last ran, and returns it; a lookup already pending or running
// is returned as it is. It's recorded as site.lookup by by, with the
// lookup's ID and status. rss.ErrNoRecord if the site doesn't exist.
func (r *AdminFeedRepository) CheckForFeeds(ctx context.Context, id string, by rss.AuditActor) (rss.Lookup, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.Lookup{}, err
	}
	defer tx.Rollback()

	var host, url string
	err = tx.QueryRowContext(ctx, `SELECT host, url FROM sites WHERE id = $1`, id).Scan(&host, &url)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Lookup{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.Lookup{}, err
	}
	// No time-to-live, so a finished lookup always runs again.
	l, err := requestLookup(ctx, tx, host, url, true, 0, 0)
	if err != nil {
		return rss.Lookup{}, err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "site.lookup", TargetType: "site", TargetID: id,
		After: map[string]any{"lookup_id": l.ID, "status": l.Status}})
	if err != nil {
		return rss.Lookup{}, err
	}
	return l, tx.Commit()
}
