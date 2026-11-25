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
			(SELECT min(j.created_at) FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE a.feed_id = f.id),
			f.categories
		FROM feeds f JOIN sites s ON s.id = f.site_id` + adminFeedSection + `
		WHERE f.id = $1`

	var feed rss.AdminFeed
	var newest, oldestJob sql.NullTime
	var categories []string
	site := &feed.Site
	row, err := scanAdminFeedRow(r.DB.QueryRowContext(ctx, stmt, id),
		&site.ID, &site.URL, &site.Title, &site.Description, &site.CreatedAt, &site.UpdatedAt,
		&feed.Articles, &newest, &feed.EnrichJobs, &oldestJob, pq.Array(&categories))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.AdminFeed{}, rss.ErrNoRecord
	}
	if err != nil {
		return rss.AdminFeed{}, err
	}
	feed.AdminFeedRow = row
	feed.Categories = categories
	site.Host = row.SiteHost
	feed.NewestArticleAt, feed.OldestEnrichJob = newest.Time, oldestJob.Time
	return feed, nil
}

// ResetBackoff makes feed id due now with no failures counted, so the
// refresh runner fetches it next as if it had never failed, recorded as
// feed.reset_backoff by by. rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) ResetBackoff(ctx context.Context, id string, by rss.AuditActor) error {
	return r.updateRow(ctx, "feed", id, by, "feed.reset_backoff",
		`SELECT consecutive_failures, next_fetch_at FROM feeds WHERE id = $1 FOR UPDATE`,
		`UPDATE feeds SET consecutive_failures = 0, next_fetch_at = now() WHERE id = $1
			RETURNING consecutive_failures, next_fetch_at`,
		"consecutive_failures", "next_fetch_at")
}

// ClearGone makes the gone feed id fetchable and due now, recorded as
// feed.clear_gone by by. A feed that isn't gone is left alone, with no
// record. rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) ClearGone(ctx context.Context, id string, by rss.AuditActor) error {
	return r.updateRow(ctx, "feed", id, by, "feed.clear_gone",
		`SELECT gone_at, next_fetch_at FROM feeds WHERE id = $1 FOR UPDATE`,
		`UPDATE feeds SET gone_at = NULL, next_fetch_at = now() WHERE id = $1 AND gone_at IS NOT NULL
			RETURNING gone_at, next_fetch_at`,
		"gone_at", "next_fetch_at")
}

// updateRow runs an action on the targetType (feed or site) id in one
// transaction: it locks the row and reads fields with read, changes them
// with update, which returns them, and records action by by with their
// values before and after. When update changes no row, there's nothing to
// record.
func (r *AdminFeedRepository) updateRow(ctx context.Context, targetType, id string, by rss.AuditActor, action, read, update string, fields ...string) error {
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
	err = recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: action, TargetType: targetType, TargetID: id, Before: before, After: after})
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

// DiscoverSections returns Discover's sections in their default order.
func (r *AdminFeedRepository) DiscoverSections(ctx context.Context) ([]rss.Section, error) {
	return listSections(ctx, r.DB)
}

// SetDiscoverSection puts feed id in Discover's catalog under the section
// sectionID, or takes it out with "", recorded as feed.discover by by with
// the section's ID and name before and after (empty for none), so the
// entry still reads after a rename. Setting the section it already has
// records nothing. Putting a skipped feed in the catalog unskips it,
// recorded as feed.discover_unskip. rss.ErrUnknownSection for a section
// that doesn't exist, rss.ErrFeedGone to add a gone feed (taking one out
// is fine), rss.ErrNoRecord if the feed doesn't exist.
func (r *AdminFeedRepository) SetDiscoverSection(ctx context.Context, id, sectionID string, by rss.AuditActor) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setDiscoverSection(ctx, tx, id, sectionID, by); err != nil {
		return err
	}
	return tx.Commit()
}

// setDiscoverSection is SetDiscoverSection in tx, which the picking page's
// Save (SavePicks) runs once per feed.
func setDiscoverSection(ctx context.Context, tx *sql.Tx, id, sectionID string, by rss.AuditActor) error {
	// Locking the feed orders this after another change to its section.
	var gone bool
	var before rss.Section
	err := tx.QueryRowContext(ctx, `SELECT f.gone_at IS NOT NULL, COALESCE(fs.id::text, ''), COALESCE(fs.name, '')
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
		if err == nil {
			// A feed in the catalog is decided, so it's no longer skipped.
			err = unskip(ctx, tx, id, by)
			if errors.Is(err, rss.ErrNoRecord) {
				err = nil
			}
		}
	}
	if err != nil {
		return err
	}
	return recordAudit(ctx, tx, rss.AuditEntry{AuditActor: by, Action: "feed.discover", TargetType: "feed", TargetID: id,
		Before: map[string]any{"section_id": before.ID, "section": before.Name},
		After:  map[string]any{"section_id": after.ID, "section": after.Name}})
}
