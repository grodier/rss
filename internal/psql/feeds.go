package psql

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
)

// feedColumnNames are the columns scanFeed reads, in order.
var feedColumnNames = []string{
	"id", "site_id", "url", "site_url", "title", "description", "last_fetched_at",
	"last_attempt_at", "last_error", "consecutive_failures", "next_fetch_at", "etag", "last_modified", "gone_at",
	"refresh_requested_at", "posts_per_week", "created_at",
}

var (
	// feedColumns is feedColumnNames as a select list.
	feedColumns = strings.Join(feedColumnNames, ", ")
	// qualifiedFeedColumns prefixes each column with the feeds alias f, for
	// queries that join tables with clashing column names.
	qualifiedFeedColumns = "f." + strings.Join(feedColumnNames, ", f.")
)

// subscriptionTitle is the reader's name for the feed, else its title;
// for queries that join subscriptions as sub.
const subscriptionTitle = `COALESCE(NULLIF(sub.title, ''), f.title)`

// qualifiedFeedColumnsTitled is qualifiedFeedColumns with f.title replaced
// by title, an SQL expression: for queries that show a reader's name for
// the feed in its place.
func qualifiedFeedColumnsTitled(title string) string {
	cols := make([]string, len(feedColumnNames))
	for i, name := range feedColumnNames {
		if name == "title" {
			cols[i] = title
		} else {
			cols[i] = "f." + name
		}
	}
	return strings.Join(cols, ", ")
}

// postsPerWeek is feed $1's articles published in the last 30 days, per
// week, over the time its articles cover: from its oldest article to now,
// at least 7 and at most 30 days, so a feed we've just found isn't counted
// as quiet. A feed with no articles gets 0 (LEAST ignores the NULL oldest
// date). Both parts are served by articles_feed_sort_idx.
const postsPerWeek = `(SELECT count(*) * 7.0 / GREATEST(7, LEAST(30,
		EXTRACT(EPOCH FROM now() - (SELECT min(COALESCE(published_at, created_at)) FROM articles WHERE feed_id = $1)) / 86400))
	FROM articles
	WHERE feed_id = $1 AND COALESCE(published_at, created_at) > now() - interval '30 days')`

// scanFeed scans a row selected with feedColumns.
func scanFeed(row interface{ Scan(...any) error }) (rss.Feed, error) {
	var feed rss.Feed
	var lastFetched, lastAttempt, goneAt, refreshRequested sql.NullTime
	err := row.Scan(&feed.ID, &feed.SiteID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &lastFetched,
		&lastAttempt, &feed.LastError, &feed.ConsecutiveFailures, &feed.NextFetch,
		&feed.ETag, &feed.LastModified, &goneAt, &refreshRequested, &feed.PostsPerWeek, &feed.CreatedAt)
	if err != nil {
		return rss.Feed{}, err
	}
	feed.LastFetched = lastFetched.Time
	feed.LastAttempt = lastAttempt.Time
	feed.GoneAt = goneAt.Time
	feed.RefreshRequestedAt = refreshRequested.Time
	return feed, nil
}

type FeedRepository struct {
	DB *sql.DB
}

func NewFeedRepository(db *sql.DB) *FeedRepository {
	return &FeedRepository{DB: db}
}

// Upsert inserts a feed or updates its metadata by URL and returns its ID.
// An existing site_id is kept (a feed belongs to the first site it was
// discovered from). next_fetch_at is set to f.NextFetch, or now() for a new
// feed when f.NextFetch is zero. It clears gone_at: the caller just parsed a
// feed at that URL, so it's back.
func (r *FeedRepository) Upsert(ctx context.Context, f rss.Feed) (string, error) {
	return upsertFeed(ctx, r.DB, f)
}

func upsertFeed(ctx context.Context, q querier, f rss.Feed) (string, error) {
	// The caller just fetched the feed, so a conflict takes its next_fetch_at
	// and clears gone_at.
	stmt := `INSERT INTO feeds (url, site_url, title, description, site_id, next_fetch_at)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()))
		ON CONFLICT (url) DO UPDATE SET
			site_url = EXCLUDED.site_url,
			title = COALESCE(NULLIF(EXCLUDED.title, ''), feeds.title),
			description = COALESCE(NULLIF(EXCLUDED.description, ''), feeds.description),
			next_fetch_at = EXCLUDED.next_fetch_at,
			gone_at = NULL
		RETURNING id`

	nextFetch := sql.NullTime{Time: f.NextFetch, Valid: !f.NextFetch.IsZero()}
	var id string
	if err := q.QueryRowContext(ctx, stmt, f.Url, f.SiteUrl, f.Title, f.Description, f.SiteID, nextFetch).Scan(&id); err != nil {
		return "", err
	}

	return id, nil
}

// SaveFetch records a successful fetch of feed f.ID in one transaction:
// updates the feed's title, description and site URL (keeping the current
// value when the new one is empty), overwrites etag and last_modified with
// f.ETag and f.LastModified, saves articles and records the successful
// attempt (see saveArticles), then sets next_fetch_at to catchUp if the
// result is Behind, else to f.NextFetch, either no sooner than
// rss.UnfollowedRefreshInterval from now if the feed has no subscribers (see
// scheduledNext). It returns rss.ErrNoRecord if the
// feed doesn't exist, and an error if f.NextFetch or catchUp is zero.
func (r *FeedRepository) SaveFetch(ctx context.Context, f rss.Feed, articles []rss.Article, catchUp time.Time) (rss.FetchResult, error) {
	// A zero time would make the feed due immediately, silently.
	if f.NextFetch.IsZero() || catchUp.IsZero() {
		return rss.FetchResult{}, errors.New("psql: SaveFetch: NextFetch or catchUp not set")
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.FetchResult{}, err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `UPDATE feeds SET
			title = COALESCE(NULLIF($2, ''), title),
			description = COALESCE(NULLIF($3, ''), description),
			site_url = COALESCE(NULLIF($4, ''), site_url),
			etag = $5,
			last_modified = $6,
			refresh_requested_at = NULL
		WHERE id = $1`

	result, err := tx.ExecContext(ctx, stmt, f.ID, f.Title, f.Description, f.SiteUrl, f.ETag, f.LastModified)
	if err != nil {
		return rss.FetchResult{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return rss.FetchResult{}, err
	}
	if n == 0 {
		return rss.FetchResult{}, rss.ErrNoRecord
	}

	res, err := saveArticles(ctx, tx, f.ID, articles)
	if err != nil {
		return rss.FetchResult{}, err
	}

	next := f.NextFetch
	if res.Behind {
		next = catchUp
	}
	if _, err := tx.ExecContext(ctx, `UPDATE feeds SET next_fetch_at = `+scheduledNext("$2")+` WHERE id = $1`, f.ID, next); err != nil {
		return rss.FetchResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return rss.FetchResult{}, err
	}

	return res, nil
}

// RecordFailure records a failed fetch of feed id: sets last_attempt_at,
// last_error (msg truncated to maxErrorLen bytes), increments
// consecutive_failures and sets next_fetch_at to next (see scheduledNext).
// rss.ErrNoRecord if the feed doesn't exist.
func (r *FeedRepository) RecordFailure(ctx context.Context, id, msg string, next time.Time) error {
	stmt := `UPDATE feeds SET last_attempt_at = now(), last_error = $2,
			consecutive_failures = consecutive_failures + 1, next_fetch_at = ` + scheduledNext("$3") + `,
			refresh_requested_at = NULL
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, textcut.Bytes(msg, maxErrorLen), next)
}

// RecordNotModified records a 304 for feed id: a successful attempt with no
// changes (last_fetched_at, last_attempt_at, clears last_error and
// consecutive_failures) and next_fetch_at = next (see scheduledNext). It recomputes posts_per_week
// so the 30-day window keeps sliding for feeds that stopped changing.
// rss.ErrNoRecord if missing.
func (r *FeedRepository) RecordNotModified(ctx context.Context, id string, next time.Time) error {
	stmt := `UPDATE feeds SET last_fetched_at = now(), last_attempt_at = now(),
			last_error = '', consecutive_failures = 0, next_fetch_at = ` + scheduledNext("$2") + `,
			refresh_requested_at = NULL, posts_per_week = ` + postsPerWeek + `
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id, next)
}

// MarkGone records that feed id answered 410 Gone: sets gone_at and
// last_attempt_at to now() and last_error to "status 410". Gone feeds are
// never claimed by ClaimDue. rss.ErrNoRecord if the feed doesn't exist.
func (r *FeedRepository) MarkGone(ctx context.Context, id string) error {
	stmt := `UPDATE feeds SET gone_at = now(), last_attempt_at = now(), last_error = 'status 410',
			refresh_requested_at = NULL
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, id)
}

// scheduledNext returns the SQL for the next_fetch_at to store, in an UPDATE
// of feeds, for a feed whose fetch outcome scheduled param: param if the feed
// has a subscriber, else no sooner than rss.UnfollowedRefreshInterval from
// now. Feeds nobody follows are only fetched for Discover's catalog or a
// refresh request, and about once a day is fresh enough for both (#632).
func scheduledNext(param string) string {
	return `CASE WHEN EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = feeds.id) THEN ` + param +
		` ELSE GREATEST(` + param + `, now() + interval '` + strconv.Itoa(int(rss.UnfollowedRefreshInterval.Seconds())) + ` seconds') END`
}

// feedDue is true for a feed ClaimDue may claim: due, not gone, and
// subscribed, requested or in Discover's catalog (discover_feeds). The
// admin's Overview counts the refresh queue with it
// (AdminOverviewRepository.Queues).
const feedDue = `next_fetch_at <= now() AND gone_at IS NULL
	AND (refresh_requested_at IS NOT NULL
	     OR EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = feeds.id)
	     OR EXISTS (SELECT 1 FROM discover_feeds d WHERE d.feed_id = feeds.id))`

// ClaimDue claims a due feed (next_fetch_at <= now()) that has a subscriber,
// a pending refresh request (see RequestRefresh) or a place in Discover's
// catalog, by moving its
// next_fetch_at to now() + lease, and returns it. Requested feeds come first,
// since someone is waiting on the page; then the feed that has been due
// longest. If the claimer never records an outcome, the feed is due again when
// the lease runs out (a request is only cleared when an outcome is recorded).
// Gone feeds (see MarkGone) are skipped. rss.ErrNoRecord when no feed is due.
func (r *FeedRepository) ClaimDue(ctx context.Context, lease time.Duration) (rss.Feed, error) {
	stmt := `UPDATE feeds SET next_fetch_at = now() + make_interval(secs => $1)
		WHERE id = (
			SELECT id FROM feeds
			WHERE ` + feedDue + `
			ORDER BY refresh_requested_at IS NULL, next_fetch_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING ` + feedColumns

	feed, err := scanFeed(r.DB.QueryRowContext(ctx, stmt, lease.Seconds()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Feed{}, rss.ErrNoRecord
		}
		return rss.Feed{}, err
	}
	return feed, nil
}

// RequestRefresh asks background refresh to fetch feed id soon, if it's due
// (next_fetch_at <= now()), not gone and not already requested. Otherwise, or
// if the feed doesn't exist, it does nothing and returns nil.
func (r *FeedRepository) RequestRefresh(ctx context.Context, id string) error {
	stmt := `UPDATE feeds SET refresh_requested_at = now()
		WHERE id = $1 AND refresh_requested_at IS NULL
		  AND gone_at IS NULL AND next_fetch_at <= now()`

	_, err := r.DB.ExecContext(ctx, stmt, id)
	return err
}

// SiteFeedFacts returns what a site's page shows about each of siteID's
// feeds beyond the feed itself, by feed ID: its section in Discover's
// catalog, if it's there, and its newest article.
//
// One statement for the site's feeds, each with an index probe of
// articles_feed_sort_idx for its newest article.
func (r *FeedRepository) SiteFeedFacts(ctx context.Context, siteID string) (map[string]rss.FeedFacts, error) {
	stmt := `SELECT f.id, sec.id, sec.slug, sec.name, ` + latestColumns + `
		FROM feeds f
		LEFT JOIN discover_feeds d ON d.feed_id = f.id
		LEFT JOIN discover_sections sec ON sec.id = d.section_id
		LEFT JOIN LATERAL (` + latestArticles("f.id", "1") + `) l ON true
		WHERE f.site_id = $1`

	rows, err := r.DB.QueryContext(ctx, stmt, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := map[string]rss.FeedFacts{}
	for rows.Next() {
		var (
			feedID               string
			secID, slug, secName sql.NullString
			l                    latestArticle
		)
		if err := rows.Scan(append([]any{&feedID, &secID, &slug, &secName}, l.dests()...)...); err != nil {
			return nil, err
		}
		var ff rss.FeedFacts
		if secID.Valid {
			ff.Section = rss.Section{ID: secID.String, Slug: slug.String, Name: secName.String}
		}
		ff.Latest, _ = l.article(feedID)
		facts[feedID] = ff
	}
	return facts, rows.Err()
}

// ListBySite returns a site's feeds ordered by title, then URL.
func (r *FeedRepository) ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error) {
	stmt := `SELECT ` + feedColumns + `
		FROM feeds
		WHERE site_id = $1
		ORDER BY title, url`

	rows, err := r.DB.QueryContext(ctx, stmt, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []rss.Feed
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return feeds, nil
}

func (r *FeedRepository) GetByID(ctx context.Context, id string) (rss.Feed, error) {
	stmt := `SELECT ` + feedColumns + `
		FROM feeds
		WHERE id = $1`

	feed, err := scanFeed(r.DB.QueryRowContext(ctx, stmt, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Feed{}, rss.ErrNoRecord
		} else {
			return rss.Feed{}, err
		}
	}

	return feed, nil
}

// GetByURL returns the feed whose url is exactly url (a CanonicalFeedURL),
// or rss.ErrNoRecord.
func (r *FeedRepository) GetByURL(ctx context.Context, url string) (rss.Feed, error) {
	stmt := `SELECT ` + feedColumns + `
		FROM feeds
		WHERE url = $1`

	feed, err := scanFeed(r.DB.QueryRowContext(ctx, stmt, url))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Feed{}, rss.ErrNoRecord
	}
	return feed, err
}
