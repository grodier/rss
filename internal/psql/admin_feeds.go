package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
	"github.com/lib/pq"
)

// AdminFeedRepository reads the admin's feeds and sites pages and makes
// their fixes to fetching, each recorded in the audit log with who made it.
// Subscribers are counts, never who.
type AdminFeedRepository struct {
	DB *sql.DB
}

func NewAdminFeedRepository(db *sql.DB) *AdminFeedRepository {
	return &AdminFeedRepository{DB: db}
}

// adminSubscribers counts the subscribers of the feed aliased alias.
func adminSubscribers(alias string) string {
	return `(SELECT count(*) FROM subscriptions sub WHERE sub.feed_id = ` + alias + `.id)`
}

// adminFeedFilters are the list's filters' conditions on feeds f.
var adminFeedFilters = map[string]string{
	rss.AdminFeedsAll:          `true`,
	rss.AdminFeedsFailing:      fmt.Sprintf(`f.consecutive_failures >= %d`, rss.AdminFailingAfter),
	rss.AdminFeedsGone:         `f.gone_at IS NOT NULL`,
	rss.AdminFeedsNeverFetched: `f.last_fetched_at IS NULL`,
	// A feed never fetched counts from when it was found, so one found a
	// minute ago isn't stale yet.
	rss.AdminFeedsStale: fmt.Sprintf(`f.gone_at IS NULL
		AND EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.feed_id = f.id)
		AND COALESCE(f.last_fetched_at, f.created_at) < now() - make_interval(secs => %d)`, int(rss.AdminStaleAfter.Seconds())),
	rss.AdminFeedsNoSubscribers: `NOT EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.feed_id = f.id)`,
}

// adminFeedSort is one of the list's orders: its key on the feed aliased
// alias, and whether the largest key comes first. Rows with the same key
// are ordered by ID in the same direction, so the order is total and a
// page's last row is its cursor.
type adminFeedSort struct {
	key  func(alias string) string
	desc bool
}

var adminFeedSorts = map[string]adminFeedSort{
	rss.AdminFeedsBySubscribers: {key: adminSubscribers, desc: true},
	rss.AdminFeedsByPosts:       {key: func(a string) string { return a + `.posts_per_week` }, desc: true},
	rss.AdminFeedsByFailures:    {key: func(a string) string { return a + `.consecutive_failures` }, desc: true},
	// Never fetched sorts as oldest.
	rss.AdminFeedsByLastSuccess: {key: func(a string) string { return `COALESCE(` + a + `.last_fetched_at, '-infinity')` }},
}

// adminFeedRowColumns are the columns scanAdminFeedRow reads, for feeds f
// joined to sites s: the feed, its site's host, its subscribers and its
// section in Discover's catalog (empty strings when it isn't in it).
var adminFeedRowColumns = qualifiedFeedColumns + `, s.host, ` + adminSubscribers("f") + `,
	COALESCE(fs.id::text, ''), COALESCE(fs.slug, ''), COALESCE(fs.name, '')`

// adminFeedSection joins feeds f to their Discover section as fs, for
// adminFeedRowColumns.
const adminFeedSection = `
	LEFT JOIN discover_feeds fd ON fd.feed_id = f.id
	LEFT JOIN discover_sections fs ON fs.id = fd.section_id`

// scanAdminFeedRow scans a row selected with adminFeedRowColumns, then
// extra.
func scanAdminFeedRow(row interface{ Scan(...any) error }, extra ...any) (rss.AdminFeedRow, error) {
	var r rss.AdminFeedRow
	sec := &r.Section
	feed, err := scanFeed(scanTail{row: row, tail: append([]any{&r.SiteHost, &r.Subscribers, &sec.ID, &sec.Slug, &sec.Name}, extra...)})
	if err != nil {
		return rss.AdminFeedRow{}, err
	}
	r.Feed = feed
	return r, nil
}

// scanTail lets scanFeed read a row with more columns after the feed's:
// it scans those into tail.
type scanTail struct {
	row  interface{ Scan(...any) error }
	tail []any
}

func (s scanTail) Scan(dest ...any) error {
	return s.row.Scan(append(dest, s.tail...)...)
}

// adminFeedSearch is the condition that feeds f of sites s match q (any
// case, in the title, URL or site's host) with q's pattern as argument n.
func adminFeedSearch(n int) string {
	return fmt.Sprintf(`(f.title ILIKE $%[1]d ESCAPE '\' OR f.url ILIKE $%[1]d ESCAPE '\' OR s.host ILIKE $%[1]d ESCAPE '\')`, n)
}

// CountFeeds counts the feeds each filter keeps whose title, URL or site's
// host contains q (any case; "" for every feed), by filter, in one pass over
// the feeds (see "The admin's feed list" in docs/backlog.md).
func (r *AdminFeedRepository) CountFeeds(ctx context.Context, q string) (map[string]int, error) {
	keys := slices.Sorted(maps.Keys(adminFeedFilters))
	cols := make([]string, len(keys))
	for i, k := range keys {
		cols[i] = `count(*) FILTER (WHERE ` + adminFeedFilters[k] + `)`
	}
	stmt := `SELECT ` + strings.Join(cols, ", ") + `
		FROM feeds f JOIN sites s ON s.id = f.site_id
		WHERE $1 = '' OR ` + adminFeedSearch(2)

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

// ListFeeds returns a page of the feeds q's filter keeps whose title, URL
// or site's host contains q.Q (any case), in q's order, starting after the
// feed q.After. An unknown filter or order is an error. Every filter but
// Gone reads all the feeds (see "The admin's feed list" in
// docs/backlog.md).
func (r *AdminFeedRepository) ListFeeds(ctx context.Context, q rss.AdminFeedQuery) ([]rss.AdminFeedRow, error) {
	filter, ok := adminFeedFilters[q.Filter]
	if !ok {
		return nil, fmt.Errorf("unknown feed filter %q", q.Filter)
	}
	order, ok := adminFeedSorts[q.Sort]
	if !ok {
		return nil, fmt.Errorf("unknown feed order %q", q.Sort)
	}

	args := []any{q.Limit}
	where := []string{filter}
	if q.Q != "" {
		args = append(args, likePattern(q.Q))
		where = append(where, adminFeedSearch(len(args)))
	}
	dir, cmp := "", ">"
	if order.desc {
		dir, cmp = " DESC", "<"
	}
	if q.After != "" {
		args = append(args, q.After)
		where = append(where, fmt.Sprintf(`(%s, f.id) %s (SELECT %s, c.id FROM feeds c WHERE c.id = $%d)`,
			order.key("f"), cmp, order.key("c"), len(args)))
	}
	stmt := `SELECT ` + adminFeedRowColumns + `
		FROM feeds f JOIN sites s ON s.id = f.site_id` + adminFeedSection + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order.key("f") + dir + `, f.id` + dir + `
		LIMIT $1`

	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminFeedRow
	for rows.Next() {
		row, err := scanAdminFeedRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Feed returns feed id's page, or rss.ErrNoRecord.
func (r *AdminFeedRepository) Feed(ctx context.Context, id string) (rss.AdminFeed, error) {
	stmt := `SELECT ` + adminFeedRowColumns + `,
			s.id, s.url, s.title, s.description, s.created_at, s.updated_at,
			(SELECT count(*) FROM articles a WHERE a.feed_id = f.id),
			(SELECT max(COALESCE(a.published_at, a.created_at)) FROM articles a WHERE a.feed_id = f.id),
			(SELECT count(*) FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE a.feed_id = f.id),
			(SELECT min(j.created_at) FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE a.feed_id = f.id)
		FROM feeds f JOIN sites s ON s.id = f.site_id` + adminFeedSection + `
		WHERE f.id = $1`

	var feed rss.AdminFeed
	var newest, oldestJob sql.NullTime
	site := &feed.Site
	row, err := scanAdminFeedRow(r.DB.QueryRowContext(ctx, stmt, id),
		&site.ID, &site.URL, &site.Title, &site.Description, &site.CreatedAt, &site.UpdatedAt,
		&feed.Articles, &newest, &feed.EnrichJobs, &oldestJob)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.AdminFeed{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.AdminFeed{}, err
	}
	feed.AdminFeedRow = row
	site.Host = row.SiteHost
	feed.NewestArticleAt, feed.OldestEnrichJob = newest.Time, oldestJob.Time
	return feed, nil
}

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

// ResetBackoff makes feed id due now with no failures counted, so the
// refresh runner fetches it next as if it had never failed, recorded as
// feed.reset_backoff by by. rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) ResetBackoff(ctx context.Context, id string, by rss.AuditActor) error {
	return r.updateFeed(ctx, id, by, "feed.reset_backoff",
		`SELECT consecutive_failures, next_fetch_at FROM feeds WHERE id = $1 FOR UPDATE`,
		`UPDATE feeds SET consecutive_failures = 0, next_fetch_at = now() WHERE id = $1
			RETURNING consecutive_failures, next_fetch_at`,
		"consecutive_failures", "next_fetch_at")
}

// ClearGone makes the gone feed id fetchable and due now, recorded as
// feed.clear_gone by by. A feed that isn't gone is left alone, with no
// record. rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) ClearGone(ctx context.Context, id string, by rss.AuditActor) error {
	return r.updateFeed(ctx, id, by, "feed.clear_gone",
		`SELECT gone_at, next_fetch_at FROM feeds WHERE id = $1 FOR UPDATE`,
		`UPDATE feeds SET gone_at = NULL, next_fetch_at = now() WHERE id = $1 AND gone_at IS NOT NULL
			RETURNING gone_at, next_fetch_at`,
		"gone_at", "next_fetch_at")
}

// updateFeed runs an action on feed id in one transaction: it locks the
// feed and reads fields with read, changes them with update, which returns
// them, and records action by by with their values before and after. When
// update changes no row, there's nothing to record.
func (r *AdminFeedRepository) updateFeed(ctx context.Context, id string, by rss.AuditActor, action, read, update string, fields ...string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	before, err := scanFields(tx.QueryRowContext(ctx, read, id), fields)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.ErrNoRecord
	}
	if err != nil {
		return err
	}
	after, err := scanFields(tx.QueryRowContext(ctx, update, id), fields)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: action, TargetType: "feed", TargetID: id, Before: before, After: after})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// scanFields scans a row of fields, each an integer or a timestamp, into a
// map by name for the audit log: NULL is nil.
func scanFields(row *sql.Row, fields []string) (map[string]any, error) {
	vals := make([]any, len(fields))
	for i := range vals {
		vals[i] = new(any)
	}
	if err := row.Scan(vals...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(fields))
	for i, f := range fields {
		out[f] = *vals[i].(*any)
	}
	return out, nil
}

// CheckIcon makes site id's icon due to be checked now, recorded as
// site.check_icon by by. rss.ErrNoRecord if the site doesn't exist.
func (r *AdminFeedRepository) CheckIcon(ctx context.Context, id string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var before, after time.Time
	err = tx.QueryRowContext(ctx, `SELECT icon_next_check_at FROM sites WHERE id = $1 FOR UPDATE`, id).Scan(&before)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.ErrNoRecord
	}
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE sites SET icon_next_check_at = now() WHERE id = $1 RETURNING icon_next_check_at`, id).Scan(&after); err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "site.check_icon", TargetType: "site", TargetID: id,
		Before: map[string]any{"icon_next_check_at": before}, After: map[string]any{"icon_next_check_at": after}})
	if err != nil {
		return err
	}
	return tx.Commit()
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

// DiscoverSections returns Discover's sections in their default order.
func (r *AdminFeedRepository) DiscoverSections(ctx context.Context) ([]rss.Section, error) {
	return listSections(ctx, r.DB)
}

// SetDiscoverSection puts feed id in Discover's catalog under the section
// sectionID, or takes it out with "", recorded as feed.discover by by with
// the section's ID and name before and after (empty for none), so the
// entry still reads after a rename. Setting the section it already has
// records nothing. rss.ErrUnknownSection for a section that doesn't exist,
// rss.ErrFeedGone to add a gone feed (taking one out is fine),
// rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) SetDiscoverSection(ctx context.Context, id, sectionID string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Locking the feed orders this after another change to its section.
	var gone bool
	var before rss.Section
	err = tx.QueryRowContext(ctx, `SELECT f.gone_at IS NOT NULL, COALESCE(fs.id::text, ''), COALESCE(fs.name, '')
		FROM feeds f`+adminFeedSection+`
		WHERE f.id = $1 FOR UPDATE OF f`, id).Scan(&gone, &before.ID, &before.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.ErrNoRecord
	}
	if err != nil {
		return err
	}
	var after rss.Section
	if sectionID != "" {
		// Compared as text, so an ID that isn't a UUID is unknown too.
		err = tx.QueryRowContext(ctx, `SELECT id, name FROM discover_sections WHERE id::text = $1`, sectionID).Scan(&after.ID, &after.Name)
		if errors.Is(err, sql.ErrNoRows) {
			return rss.ErrUnknownSection
		}
		if err != nil {
			return err
		}
	}
	switch {
	case after.ID == before.ID:
		return nil
	case after.ID != "" && gone:
		return rss.ErrFeedGone
	case after.ID == "":
		_, err = tx.ExecContext(ctx, `DELETE FROM discover_feeds WHERE feed_id = $1`, id)
		if err == nil {
			// Leaving the catalog makes a feed nobody follows unwanted (#708).
			err = dropUnwantedEnrichJobs(ctx, tx, []string{id})
		}
	default:
		_, err = tx.ExecContext(ctx, `INSERT INTO discover_feeds (feed_id, section_id) VALUES ($1, $2)
			ON CONFLICT (feed_id) DO UPDATE SET section_id = EXCLUDED.section_id`, id, after.ID)
		if err == nil && before.ID == "" {
			// Entering the catalog makes a feed nobody follows wanted: queue
			// its enrichment backlog (#708).
			var followed bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions WHERE feed_id = $1)`, id).Scan(&followed)
			if err == nil && !followed {
				err = queueEnrichBacklog(ctx, tx, id)
			}
		}
	}
	if err != nil {
		return err
	}
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "feed.discover", TargetType: "feed", TargetID: id,
		Before: map[string]any{"section_id": before.ID, "section": before.Name},
		After:  map[string]any{"section_id": after.ID, "section": after.Name}})
	if err != nil {
		return err
	}
	return tx.Commit()
}

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
	l, err := requestLookup(ctx, tx, siteKey, u, false, 0, 0)
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
	for _, l := range lines {
		if l.Outcome != seed.LineAdd {
			continue
		}
		keys, urls, sections = append(keys, l.SiteKey), append(urls, l.URL), append(sections, l.SectionID)
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
			SELECT * FROM unnest($1::text[], $2::text[], $3::text[]) WITH ORDINALITY AS t(site_key, url, section_id, n)
		), added AS (
			INSERT INTO seed_sites (site_key, url, section_id, source, added_by)
			SELECT i.site_key, i.url, ds.id, $4, NULLIF($5, '')::uuid
			FROM input i LEFT JOIN discover_sections ds ON ds.id::text = i.section_id
			ORDER BY i.n
			ON CONFLICT (site_key) DO NOTHING
			RETURNING site_key, url
		), queued AS (
			INSERT INTO lookups (site_key, url, priority)
			SELECT site_key, url, 1 FROM added
			ON CONFLICT (site_key) DO UPDATE SET
				status = 'pending', url = EXCLUDED.url, error = '', priority = 1,
				attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
			WHERE lookups.status = 'failed' OR (lookups.status = 'done' AND lookups.site_id IS NULL)
		)
		SELECT a.site_key FROM added a JOIN input i ON i.site_key = a.site_key ORDER BY i.n`
	rows, err := tx.QueryContext(ctx, stmt, pq.Array(keys), pq.Array(urls), pq.Array(sections), source, by.AdminID)
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

	stmt := `WITH retried AS (
			UPDATE lookups l SET status = 'pending', url = ss.url, error = '', priority = 1,
				attempts = 0, requested_at = now(), started_at = NULL, finished_at = NULL
			FROM seed_sites ss
			WHERE l.site_key = ss.site_key AND ` + adminSeedNotFound + `
			RETURNING 1)
		SELECT count(*) FROM retried`
	var n int
	if err := tx.QueryRowContext(ctx, stmt).Scan(&n); err != nil {
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

// Catalog returns Discover's sections in their default order, each with
// its catalog feeds by title, empty sections included, and gone feeds,
// which Discover hides, too, so the admin can take them out. The catalog is
// small, edited by hand, so each feed's latest article and subscribers are
// counted per row.
func (r *AdminFeedRepository) Catalog(ctx context.Context) ([]rss.AdminCatalogSection, error) {
	stmt := `SELECT ds.id, ds.slug, ds.name, COALESCE(f.id::text, ''), COALESCE(f.title, ''), COALESCE(f.url, ''),
			COALESCE(s.id::text, ''), COALESCE(s.host, ''), COALESCE(f.posts_per_week, 0),
			(SELECT max(COALESCE(a.published_at, a.created_at)) FROM articles a WHERE a.feed_id = f.id),
			CASE WHEN f.id IS NULL THEN 0 ELSE ` + adminSubscribers("f") + ` END, f.gone_at
		FROM discover_sections ds
		LEFT JOIN discover_feeds d ON d.section_id = ds.id
		LEFT JOIN feeds f ON f.id = d.feed_id
		LEFT JOIN sites s ON s.id = f.site_id
		ORDER BY ds.position, ds.name, lower(f.title), f.url`
	rows, err := r.DB.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rss.AdminCatalogSection
	for rows.Next() {
		var sec rss.Section
		var f rss.AdminCatalogFeed
		var latest, gone sql.NullTime
		if err := rows.Scan(&sec.ID, &sec.Slug, &sec.Name, &f.ID, &f.Title, &f.URL,
			&f.SiteID, &f.SiteHost, &f.PostsPerWeek, &latest, &f.Subscribers, &gone); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].ID != sec.ID {
			out = append(out, rss.AdminCatalogSection{Section: sec})
		}
		if f.ID != "" {
			f.LatestPost, f.GoneAt = latest.Time, gone.Time
			last := &out[len(out)-1]
			last.Feeds = append(last.Feeds, f)
		}
	}
	return out, rows.Err()
}
