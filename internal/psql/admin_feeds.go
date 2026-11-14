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
// joined to sites s.
var adminFeedRowColumns = qualifiedFeedColumns + `, s.host, ` + adminSubscribers("f")

// scanAdminFeedRow scans a row selected with adminFeedRowColumns, then
// extra.
func scanAdminFeedRow(row interface{ Scan(...any) error }, extra ...any) (rss.AdminFeedRow, error) {
	var r rss.AdminFeedRow
	var host string
	var subscribers int
	feed, err := scanFeed(scanTail{row: row, tail: append([]any{&host, &subscribers}, extra...)})
	if err != nil {
		return rss.AdminFeedRow{}, err
	}
	r.Feed, r.SiteHost, r.Subscribers = feed, host, subscribers
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
		FROM feeds f JOIN sites s ON s.id = f.site_id
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
		FROM feeds f JOIN sites s ON s.id = f.site_id
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
		FROM feeds f JOIN sites s ON s.id = f.site_id
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
