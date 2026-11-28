package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// articleColumnNames are the columns scanArticle reads, in order.
var articleColumnNames = []string{
	"id", "feed_id", "external_id", "url", "canonical_url", "image_url", "media_type", "authors", "title", "summary", "content",
	"excerpt", "page_image_url", "page_title", "page_description", "image_failed_at", "published_at", "published_date_only", "timeline_at", "backlog", "created_at", "updated_at",
}

var (
	// articleColumns is articleColumnNames as a select list.
	articleColumns = strings.Join(articleColumnNames, ", ")
	// qualifiedArticleColumns prefixes each column with the articles alias a,
	// for queries that join tables with clashing column names.
	qualifiedArticleColumns = "a." + strings.Join(articleColumnNames, ", a.")
	// qualifiedListArticleColumns is the select list for articles shown in
	// a list (see articleListColumns).
	qualifiedListArticleColumns = articleListColumns("a.")
)

// articleListColumns returns the select list for articles shown in a list,
// in scanArticle's order, each column prefixed with prefix ("" or "a."):
// summary and content are selected as empty strings because lists show the
// excerpt and article HTML can be large.
func articleListColumns(prefix string) string {
	cols := make([]string, len(articleColumnNames))
	for i, name := range articleColumnNames {
		switch name {
		case "summary", "content":
			cols[i] = "'' AS " + name
		default:
			cols[i] = prefix + name
		}
	}
	return strings.Join(cols, ", ")
}

// scanArticle scans a row selected with articleColumns.
func scanArticle(row interface{ Scan(...any) error }) (rss.Article, error) {
	var a rss.Article
	var imageFailedAt, publishedAt sql.NullTime
	err := row.Scan(&a.ID, &a.FeedID, &a.ExternalID, &a.URL, &a.CanonicalURL, &a.ImageURL, &a.MediaType, pq.Array(&a.Authors), &a.Title, &a.Summary, &a.Content, &a.Excerpt, &a.PageImageURL, &a.PageTitle, &a.PageDescription, &imageFailedAt, &publishedAt, &a.PublishedDateOnly, &a.TimelineAt, &a.Backlog, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return rss.Article{}, err
	}
	a.ImageFailedAt = imageFailedAt.Time
	a.PublishedAt = publishedAt.Time
	return a, nil
}

type ArticleRepository struct {
	DB *sql.DB
}

func NewArticleRepository(db *sql.DB) *ArticleRepository {
	return &ArticleRepository{DB: db}
}

// GetByID returns the article with id, or rss.ErrNoRecord.
func (r *ArticleRepository) GetByID(ctx context.Context, id string) (rss.Article, error) {
	stmt := `SELECT ` + articleColumns + `
		FROM articles
		WHERE id = $1`

	a, err := scanArticle(r.DB.QueryRowContext(ctx, stmt, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Article{}, rss.ErrNoRecord
		}
		return rss.Article{}, err
	}
	return a, nil
}

// GetVisible returns article id as reader userID sees it: rss.ErrNoRecord
// if there's no such article or it's in another reader's newsletter feed
// (see FeedRepository.GetVisible). Every reader route that takes an article
// ID uses it, never GetByID.
func (r *ArticleRepository) GetVisible(ctx context.Context, userID, id string) (rss.Article, error) {
	stmt := `SELECT ` + qualifiedArticleColumns + `
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE a.id = $1 AND ` + feedVisible

	a, err := scanArticle(r.DB.QueryRowContext(ctx, stmt, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Article{}, rss.ErrNoRecord
	}
	return a, err
}

// ListByFeed returns up to limit of feedID's articles, newest first by
// published date (else created date) then ID, starting after before (from
// the start if before is zero). Summary and Content are left empty.
func (r *ArticleRepository) ListByFeed(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
	return r.ListByFeeds(ctx, []string{feedID}, before, limit)
}

// ListByFeeds is ListByFeed over the articles of all of feedIDs together,
// as a site's page shows them.
//
// For each feed, its newest limit articles before the cursor from
// articles_feed_sort_idx, then merged, keeping limit: the feeds bound the
// work, not their articles.
func (r *ArticleRepository) ListByFeeds(ctx context.Context, feedIDs []string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
	stmt := `SELECT ` + qualifiedListArticleColumns + `
		FROM unnest($1::uuid[]) AS f(id)
		CROSS JOIN LATERAL (
			SELECT ` + qualifiedListArticleColumns + `
			FROM articles a
			WHERE a.feed_id = f.id
				AND ($2::timestamptz IS NULL OR (COALESCE(a.published_at, a.created_at), a.id) < ($2, $3::uuid))
			ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
			LIMIT $4
		) a
		ORDER BY COALESCE(a.published_at, a.created_at) DESC, a.id DESC
		LIMIT $4`

	at, id := cursorArgs(before)
	rows, err := r.DB.QueryContext(ctx, stmt, pq.Array(feedIDs), at, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := []rss.Article{}
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return articles, nil
}

// ListTimeline returns up to limit entries from the feeds userID subscribes
// to that filter shows (see timelineWhere), starting after before (from the
// start if before is zero).
//
// Entries are one list ordered by Position then ID, newest first: each at
// the reader's own position (their timeline_entries row) when they have
// one, else at the article's timeline_at. A page merges two queries: the
// reader's rows (entriesStmt) and the articles without a row
// (timelineStmt). When the rows fill the page, the second is bounded below
// by the last of them, since nothing below it can be on the page: that keeps
// each feed's scan a short range, so the reader's first pages are cheap.
// Rows of feeds the reader no longer follows aren't shown.
//
// Copies of one article (same non-empty canonical_url) in several of those
// feeds are one entry: the earliest copy (smallest position, then ID, each
// copy at the reader's row's position if it has one, else its timeline_at),
// with AlsoIn listing the other feeds that have a copy, ordered by title.
// Feed titles are the user's names for their subscriptions where they've
// set one.
//
// Summary and Content are left empty.
func (r *ArticleRepository) ListTimeline(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
	at, id := cursorArgs(before)

	args := []any{userID, at, id, limit}
	items, err := r.queryTimeline(ctx, entriesStmt(filter, &args), args, true)
	if err != nil {
		return nil, err
	}

	var above rss.ArticleCursor
	if len(items) >= limit {
		last := items[len(items)-1]
		above = rss.ArticleCursor{At: last.Position, ID: last.Article.ID}
	}
	args = []any{userID, at, id, limit}
	rest, err := r.queryTimeline(ctx, timelineStmt(filter, false, above, &args), args, false)
	if err != nil {
		return nil, err
	}

	// UUIDs' lowercase strings sort as Postgres sorts uuids.
	items = append(items, rest...)
	slices.SortStableFunc(items, func(x, y rss.ArticleWithFeed) int {
		if c := y.Position.Compare(x.Position); c != 0 {
			return c
		}
		return strings.Compare(y.Article.ID, x.Article.ID)
	})
	items = items[:min(len(items), limit)]

	if err := r.fillAlsoIn(ctx, userID, items); err != nil {
		return nil, err
	}

	return items, nil
}

// queryTimeline runs one of ListTimeline's queries: with position, its rows
// end with the entry's position (entriesStmt); without, the entry is at the
// article's timeline_at (timelineStmt).
func (r *ArticleRepository) queryTimeline(ctx context.Context, stmt string, args []any, position bool) ([]rss.ArticleWithFeed, error) {
	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []rss.ArticleWithFeed{}
	for rows.Next() {
		var item rss.ArticleWithFeed
		var extra []any
		if position {
			extra = append(extra, &item.Position)
		}
		if item.Article, item.Feed, err = scanArticleWithFeed(rows, extra...); err != nil {
			return nil, err
		}
		if !position {
			item.Position = item.Article.TimelineAt
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// entriesStmt returns the query for the reader's own rows in ListTimeline,
// appending filter's parameters to args, which starts with the user ID, the
// cursor's time and ID, and the limit. It selects the row's position after
// the feed's columns. Joining subscriptions leaves out rows of feeds the
// reader no longer follows.
func entriesStmt(filter rss.TimelineFilter, args *[]any) string {
	return `SELECT ` + qualifiedListArticleColumns + `, ` + listFeedColumns(false) + `, e.timeline_at
		FROM timeline_entries e
		JOIN subscriptions sub ON sub.user_id = e.user_id AND sub.feed_id = e.feed_id
		JOIN articles a ON a.id = e.article_id
		JOIN feeds f ON f.id = e.feed_id
		WHERE e.user_id = $1
			AND ($2::timestamptz IS NULL OR (e.timeline_at, e.article_id) < ($2, $3::uuid))` +
		timelineWhere(filter, args, "sub", "a", "sub.feed_id") + `
			AND NOT (a.has_copies AND ` + earlierCopy(filter, false, "e.timeline_at", args) + `)
		ORDER BY e.timeline_at DESC, e.article_id DESC
		LIMIT $4`
}

// ListShared returns the newest limit articles with a URL of ownerID's
// timeline with filter, for its shared feed: like ListTimeline (or
// ListStarred when filter.StarredOnly, with StarredAt set), but at shared
// positions (the owner's rows are ignored), with each feed's own title,
// never the owner's name for it, and without AlsoIn.
// Summary and Content are left empty.
func (r *ArticleRepository) ListShared(ctx context.Context, ownerID string, filter rss.TimelineFilter, limit int) ([]rss.SharedArticle, error) {
	at, id := cursorArgs(rss.ArticleCursor{})
	args := []any{ownerID, at, id, limit}

	var stmt string
	if filter.StarredOnly {
		stmt = starredStmt(filter, true, &args)
	} else {
		stmt = timelineStmt(filter, true, rss.ArticleCursor{}, &args)
	}
	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []rss.SharedArticle{}
	for rows.Next() {
		var item rss.SharedArticle
		var extra []any
		if filter.StarredOnly {
			extra = append(extra, &item.StarredAt)
		}
		if item.Article, item.Feed, err = scanArticleWithFeed(rows, extra...); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// timelineStmt returns the query for the articles without a row of the
// reader's in ListTimeline (for ListShared, with shared: every article),
// appending filter's parameters to args, which starts with the user ID, the
// cursor's time and ID, and the limit. Entries are at their timeline_at.
// When above isn't zero, only articles after it are read. With shared, feeds
// have their own titles, articles without a URL are left out, and the
// reader's rows are ignored.
func timelineStmt(filter rss.TimelineFilter, shared bool, above rss.ArticleCursor, args *[]any) string {
	// For each subscription, read the feed's newest limit entries before the
	// cursor from articles_feed_timeline_idx, then merge them and keep limit.
	// Only rows flagged has_copies at ingest are checked for an earlier copy;
	// articles with no canonical URL never are.
	var where strings.Builder
	if !shared {
		// Leave out the articles the reader has a row for with NOT IN on
		// $1 rather than a NOT EXISTS on sub.user_id: uncorrelated, it's a
		// hashed SubPlan, which reads the reader's article IDs once per
		// statement (at most about rss.MaxEntries plus a visit's arrivals,
		// since MarkSeen trims them) and checks each scanned article in
		// memory instead of with an index lookup. article_id is NOT NULL,
		// so NOT IN has no NULL surprises.
		where.WriteString(`
				AND a.id NOT IN (SELECT e.article_id FROM timeline_entries e WHERE e.user_id = $1)`)
	}
	if !above.IsZero() {
		// The scalar bound is what keeps the scan a short range on the
		// index; the row comparison alone doesn't.
		*args = append(*args, above.At, above.ID)
		fmt.Fprintf(&where, `
				AND a.timeline_at >= $%[1]d AND (a.timeline_at, a.id) > ($%[1]d, $%[2]d::uuid)`, len(*args)-1, len(*args))
	}
	where.WriteString(timelineWhere(filter, args, "sub", "a", "sub.feed_id") + sharedWhere(shared, "a"))
	subs := ""
	if shared {
		subs = sharedSubscriptions
	}
	return `SELECT ` + qualifiedArticleColumns + `, ` + listFeedColumns(shared) + `
		FROM subscriptions sub
		CROSS JOIN LATERAL (
			SELECT ` + qualifiedListArticleColumns + `
			FROM articles a
			WHERE a.feed_id = sub.feed_id
				AND ($2::timestamptz IS NULL OR (a.timeline_at, a.id) < ($2, $3::uuid))` + where.String() + `
				AND NOT (a.has_copies AND ` + earlierCopy(filter, shared, "a.timeline_at", args) + `)
			ORDER BY a.timeline_at DESC, a.id DESC
			LIMIT $4
		) a
		JOIN feeds f ON f.id = a.feed_id
		WHERE sub.user_id = $1` + subs + `
		ORDER BY a.timeline_at DESC, a.id DESC
		LIMIT $4`
}

// earlierCopy returns the condition that the entry of the article a (under
// the subscription sub), at position, has an earlier copy: an article with
// the same canonical URL, at a smaller position then ID, in one of the
// user's feeds that filter shows. Each copy is at the reader's row's
// position if it has one, else its timeline_at; with shared, rows are
// ignored. Copies collapse after filtering, so a left-out feed's copy never
// takes an entry's place. It appends filter's parameters to args.
func earlierCopy(filter rss.TimelineFilter, shared bool, position string, args *[]any) string {
	// A row is never below its article's timeline_at, so a copy whose
	// timeline_at isn't earlier isn't earlier at its row either. Checking
	// that first, on the copy alone, leaves out a itself and later copies
	// before the joins, so only copies that may be earlier are looked up.
	rows, raised := "", ""
	if !shared {
		rows = `
					LEFT JOIN timeline_entries eb ON eb.user_id = sub.user_id AND eb.article_id = b.id`
		raised = `
						AND (COALESCE(eb.timeline_at, b.timeline_at), b.id) < (` + position + `, a.id)`
	}
	return `EXISTS (
					SELECT 1
					FROM articles b
					JOIN subscriptions sub2 ON sub2.user_id = sub.user_id AND sub2.feed_id = b.feed_id` + rows + `
					WHERE b.canonical_url = a.canonical_url AND b.canonical_url <> ''
						AND (b.timeline_at, b.id) < (` + position + `, a.id)` + raised +
		timelineWhere(filter, args, "sub2", "b", "sub2.feed_id") + sharedWhere(shared, "b") + sharedCopy(shared, "b") + `
				)`
}

// listFeedColumns returns the feed columns of a timeline query: with the
// reader's names for their subscriptions, or the feeds' own titles when
// shared.
func listFeedColumns(shared bool) string {
	if shared {
		return qualifiedFeedColumns
	}
	return qualifiedFeedColumnsTitled(subscriptionTitle)
}

// sharedWhere returns the condition a shared timeline adds on the articles
// alias a: only articles with a link. "" unless shared. A shared timeline
// also leaves out newsletter feeds, which are private to their owner even on
// their own shared timeline: sharedSubscriptions for timelineStmt, the feed
// alias in starredStmt, and sharedCopy for earlierCopy.
func sharedWhere(shared bool, a string) string {
	if !shared {
		return ""
	}
	return `
				AND ` + a + `.url <> ''`
}

// sharedSubscriptions is the condition a shared timeline adds on the
// subscriptions alias sub, outside timelineStmt's per-subscription LATERAL:
// no newsletter feed. Filtering there keeps each feed's walk of
// articles_feed_timeline_idx under its LIMIT; a condition on the articles
// inside it doesn't (#846).
const sharedSubscriptions = `
		  AND NOT EXISTS (SELECT 1 FROM feeds o WHERE o.id = sub.feed_id AND o.owner_id IS NOT NULL)`

// sharedCopy returns the condition earlierCopy adds on the copy alias b for
// a shared timeline: never a newsletter's copy, so one never collapses a
// public article away. "" unless shared. It's checked only for articles
// flagged has_copies.
func sharedCopy(shared bool, b string) string {
	if !shared {
		return ""
	}
	return `
						AND NOT EXISTS (SELECT 1 FROM feeds o WHERE o.id = ` + b + `.feed_id AND o.owner_id IS NOT NULL)`
}

// scanArticleWithFeed scans a row of an article's columns (as scanArticle),
// then its feed's (as scanFeed), then extra.
func scanArticleWithFeed(rows *sql.Rows, extra ...any) (rss.Article, rss.Feed, error) {
	var feed rss.Feed
	// scanArticle and scanFeed each take a Scan func and read their own
	// columns, so nest them: the feed's destinations go after the article's.
	article, err := scanArticle(scanFunc(func(articleDests ...any) error {
		var feedErr error
		feed, feedErr = scanFeed(scanFunc(func(feedDests ...any) error {
			dests := append(articleDests, feedDests...)
			return rows.Scan(append(dests, extra...)...)
		}))
		return feedErr
	}))
	if err != nil {
		return rss.Article{}, rss.Feed{}, err
	}
	return article, feed, nil
}

// timelineWhere returns the SQL conditions on the aliases sub
// (subscriptions) and a (articles) for filter, appending its parameters to
// args. Each condition starts with AND, so the result follows another
// condition; it's "" for the zero filter.
//
// Show: with OnlyIncluded, only the feeds in IncludeFeedIDs or with a tag in
// IncludeTagIDs. Leave out: never the feeds in ExcludeFeedIDs or with a tag
// in ExcludeTagIDs, even when included. Only show: with MaxPostsPerWeek,
// only feeds whose posts_per_week is at most that, so a feed that gets busier
// takes its older articles with it; with MediaOnly, only articles with an
// audio or video enclosure (a non-empty media_type); with IncludeWords, only
// articles whose title, page title or excerpt mention one of them; with
// ExcludeWords, never articles that mention one of them (see wordsPattern).
//
// feed is the expression of the feed ID whose posting rate counts: the
// article's feed. Where sub is never NULL, passing sub's feed_id (the same
// feed) lets Postgres check the rate once per subscription instead of per
// article.
//
// sub can be NULL (a LEFT JOIN, as in ListStarred: a star from a feed the
// user no longer subscribes to). Then Show conditions fail (the feed has no
// tags and isn't chosen) and Leave out conditions pass; feed must then be
// a's feed_id.
func timelineWhere(filter rss.TimelineFilter, args *[]any, sub, a, feed string) string {
	ids := func(s []string) string {
		if s == nil {
			// NULL would make = ANY and NOT (= ANY) both NULL.
			s = []string{}
		}
		*args = append(*args, pq.Array(s))
		return fmt.Sprintf("$%d::uuid[]", len(*args))
	}
	hasTag := func(tagIDs string) string {
		return `EXISTS (
				SELECT 1 FROM subscription_tags st
				WHERE st.user_id = ` + sub + `.user_id AND st.feed_id = ` + sub + `.feed_id AND st.tag_id = ANY(` + tagIDs + `))`
	}

	var where strings.Builder
	if filter.OnlyIncluded {
		where.WriteString(`
				AND COALESCE(` + sub + `.feed_id = ANY(` + ids(filter.IncludeFeedIDs) + `) OR ` + hasTag(ids(filter.IncludeTagIDs)) + `, false)`)
	}
	if len(filter.ExcludeFeedIDs) > 0 {
		where.WriteString(`
				AND NOT COALESCE(` + sub + `.feed_id = ANY(` + ids(filter.ExcludeFeedIDs) + `), false)`)
	}
	if len(filter.ExcludeTagIDs) > 0 {
		where.WriteString(`
				AND NOT ` + hasTag(ids(filter.ExcludeTagIDs)))
	}
	if filter.MaxPostsPerWeek > 0 {
		// A subquery rather than a join, so timelines without the condition
		// (Home among them) don't read feeds.
		*args = append(*args, filter.MaxPostsPerWeek)
		fmt.Fprintf(&where, `
				AND EXISTS (SELECT 1 FROM feeds rate WHERE rate.id = %s AND rate.posts_per_week <= $%d)`, feed, len(*args))
	}
	if filter.MediaOnly {
		where.WriteString(`
				AND ` + a + `.media_type <> ''`)
	}
	text := fmt.Sprintf(`concat_ws(' ', %[1]s.title, %[1]s.page_title, %[1]s.excerpt)`, a)
	if len(filter.IncludeWords) > 0 {
		*args = append(*args, wordsPattern(filter.IncludeWords))
		fmt.Fprintf(&where, `
				AND %s ~* $%d`, text, len(*args))
	}
	if len(filter.ExcludeWords) > 0 {
		*args = append(*args, wordsPattern(filter.ExcludeWords))
		fmt.Fprintf(&where, `
				AND NOT (%s ~* $%d)`, text, len(*args))
	}
	return where.String()
}

// fillAlsoIn sets AlsoIn on each item with a canonical URL to the other
// feeds userID subscribes to that have a copy of it, ordered by title (the
// user's name for the feed where they've set one).
func (r *ArticleRepository) fillAlsoIn(ctx context.Context, userID string, items []rss.ArticleWithFeed) error {
	byURL := map[string]*rss.ArticleWithFeed{}
	var urls []string
	for i := range items {
		if u := items[i].Article.CanonicalURL; u != "" {
			byURL[u] = &items[i]
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil
	}

	stmt := `SELECT a.canonical_url, ` + qualifiedFeedColumnsTitled(subscriptionTitle) + `
		FROM subscriptions sub
		JOIN articles a ON a.feed_id = sub.feed_id
		JOIN feeds f ON f.id = a.feed_id
		WHERE sub.user_id = $1 AND a.canonical_url = ANY($2)
		ORDER BY lower(COALESCE(NULLIF(sub.title, ''), NULLIF(f.title, ''), f.url)), f.id`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, pq.Array(urls))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var canonicalURL string
		feed, err := scanFeed(scanFunc(func(feedDests ...any) error {
			return rows.Scan(append([]any{&canonicalURL}, feedDests...)...)
		}))
		if err != nil {
			return err
		}
		item := byURL[canonicalURL]
		// A feed can carry several copies itself; list it once, and never
		// the entry's own feed.
		if item == nil || feed.ID == item.Feed.ID || slices.ContainsFunc(item.AlsoIn, func(f rss.Feed) bool { return f.ID == feed.ID }) {
			continue
		}
		item.AlsoIn = append(item.AlsoIn, feed)
	}
	return rows.Err()
}

// cursorArgs returns before's time and ID as query arguments, both NULL for
// the zero cursor.
func cursorArgs(before rss.ArticleCursor) (sql.NullTime, sql.NullString) {
	if before.IsZero() {
		return sql.NullTime{}, sql.NullString{}
	}
	return sql.NullTime{Time: before.At, Valid: true}, sql.NullString{String: before.ID, Valid: true}
}

// backlogSlack is how far before a feed's previous successful fetch an
// article's published date must be for it to count as backlog.
const backlogSlack = 24 * time.Hour

// staleFeedAfter is how long a feed must go without a fetch attempt for its
// next save to be placed as a first save: nobody was fetching it (no
// subscribers, or gone), since a followed feed is attempted at least once a
// day, failing or not (ingest.MaxRetryDelay).
const staleFeedAfter = 48 * time.Hour

// undatedBacklogStep spaces out the undated articles of a feed's first save:
// the newest goes this long before the save, and none are further apart (see
// placeUndated).
const undatedBacklogStep = 24 * time.Hour

// timelineAt returns each article's timeline position, in the order of
// articles, for a save at now of a feed whose previous successful fetch
// was prevFetched (zero for the feed's first save).
//
// Backlog (every dated item of a first save, and items of later saves
// published before prevFetched - backlogSlack) is placed at its published
// date, clamped to now. A first save's undated items are backlog too, spaced
// out below the items before them (see placeUndated).
//
// The rest of a later save is news, placed no lower than a floor: the later
// of prevFetched and now - rss.NewsWindow (see placeNews).
func timelineAt(articles []rss.Article, prevFetched, now time.Time) []time.Time {
	positions := make([]time.Time, len(articles))

	var news []int
	for i, a := range articles {
		if isBacklog(a, prevFetched) && !a.PublishedAt.IsZero() {
			positions[i] = a.PublishedAt
			if positions[i].After(now) {
				positions[i] = now
			}
			continue
		}
		if prevFetched.IsZero() {
			continue // undated on a first save: placeUndated, below
		}
		news = append(news, i)
	}
	if prevFetched.IsZero() {
		placeUndated(articles, positions, now)
	} else {
		placeNews(articles, news, positions, prevFetched, now)
	}
	return positions
}

// raisePositions returns a reader's positions for articles whose shared
// positions are positions, given seenUpTo, the newest position the reader
// has had on screen (zero for none): a position after seenUpTo is kept, and
// those at or before it are spread evenly, newest first, between seenUpTo
// and the lowest kept one (else now), at least 1 µs apart and never at or
// before seenUpTo. So nothing a reader gets lands among what they've seen,
// and what's spread keeps its order.
//
// Where there's less than 1 µs of room per article, the lowest are clamped
// to 1 µs after seenUpTo (timelines break ties by ID).
func raisePositions(positions []time.Time, seenUpTo, now time.Time) []time.Time {
	raised := slices.Clone(positions)
	if seenUpTo.IsZero() {
		return raised
	}

	var spread []int
	lowest := now // the lowest kept position
	for i, p := range positions {
		if p.After(seenUpTo) {
			if p.Before(lowest) {
				lowest = p
			}
			continue
		}
		spread = append(spread, i)
	}
	if len(spread) == 0 {
		return raised
	}

	sort.SliceStable(spread, func(x, y int) bool {
		return positions[spread[x]].After(positions[spread[y]])
	})
	// Postgres keeps microseconds, so whole steps keep the stored positions
	// the ones computed here.
	step := max((lowest.Sub(seenUpTo) / time.Duration(len(spread)+1)).Truncate(time.Microsecond), time.Microsecond)
	least := seenUpTo.Add(time.Microsecond)
	for k, i := range spread {
		raised[i] = lowest.Add(-time.Duration(k+1) * step)
		if raised[i].Before(least) {
			raised[i] = least
		}
	}
	return raised
}

// isBacklog reports whether timelineAt places a as backlog, for a save of a
// feed whose previous successful fetch was prevFetched: every item of a first
// save (prevFetched zero), dated or not, and later items published before
// prevFetched - backlogSlack. A later save's backlog gets no reader positions
// (see saveArticles); it's stored with the article for debug mode.
func isBacklog(a rss.Article, prevFetched time.Time) bool {
	return prevFetched.IsZero() || (!a.PublishedAt.IsZero() && a.PublishedAt.Before(prevFetched.Add(-backlogSlack)))
}

// placeNews sets the positions of a later save's news, the articles at the
// indexes news (in document order). Undated news goes at now, then 1 µs
// apart in document order. News dated after the floor (the later of
// prevFetched and now - rss.NewsWindow) goes at its published date, but no
// higher than just below the undated news. News dated at or below the floor
// (an aggregator's submission dates, a day-only date, a feed that was down)
// is spread evenly, newest-published first, between the floor and the
// lowest news placed by date (else just below the undated news, else now),
// at least 1 µs apart, so it's neither buried below what the reader saw
// nor stacked in a block.
func placeNews(articles []rss.Article, news []int, positions []time.Time, prevFetched, now time.Time) {
	floor := now.Add(-rss.NewsWindow)
	if prevFetched.After(floor) {
		floor = prevFetched
	}

	ceiling := now // the highest a dated article goes
	var undated int
	for _, i := range news {
		if articles[i].PublishedAt.IsZero() {
			positions[i] = now.Add(-time.Duration(undated) * time.Microsecond)
			undated++
			ceiling = now.Add(-time.Duration(undated) * time.Microsecond)
		}
	}

	var spread []int
	lowest := ceiling // the lowest position of news placed by date
	for _, i := range news {
		published := articles[i].PublishedAt
		switch {
		case published.IsZero():
		case published.After(floor):
			positions[i] = published
			if published.After(ceiling) {
				positions[i] = ceiling
			}
			if positions[i].Before(lowest) {
				lowest = positions[i]
			}
		default:
			spread = append(spread, i)
		}
	}

	sort.SliceStable(spread, func(x, y int) bool {
		return articles[spread[x]].PublishedAt.After(articles[spread[y]].PublishedAt)
	})
	step := max(lowest.Sub(floor)/time.Duration(len(spread)+1), time.Microsecond)
	for k, i := range spread {
		positions[i] = lowest.Add(-time.Duration(k+1) * step)
	}
}

// placeUndated sets the positions of a first save's undated articles, given
// those of its dated ones. Each run of undated articles in document order
// goes below the article before it and below earlier undated ones, one step
// apart; a run that would reach the dated article after it is spread evenly
// between the two instead. Either way, none goes later than
// undatedBacklogStep before now. The step is the median gap between
// consecutive dated articles, at most undatedBacklogStep (undatedBacklogStep
// without one). So an undated feed's items go a day apart from a day back,
// and a mixed feed's follow its pace, never on top and never in a block.
func placeUndated(articles []rss.Article, positions []time.Time, now time.Time) {
	dated := func(i int) bool { return !articles[i].PublishedAt.IsZero() }

	var gaps []time.Duration
	prev := -1
	for i := range articles {
		if !dated(i) {
			continue
		}
		if prev >= 0 {
			if gap := positions[prev].Sub(positions[i]); gap > 0 {
				gaps = append(gaps, gap)
			}
		}
		prev = i
	}
	step := undatedBacklogStep
	if len(gaps) > 0 {
		slices.Sort(gaps)
		step = min(gaps[(len(gaps)-1)/2], undatedBacklogStep)
	}

	newest := now.Add(-undatedBacklogStep)
	above := now // what the next undated article goes below
	for i := 0; i < len(articles); {
		if dated(i) {
			i++
			continue
		}
		j := i
		for j < len(articles) && !dated(j) {
			j++
		}
		if i > 0 && positions[i-1].Before(above) {
			above = positions[i-1]
		}
		n := time.Duration(j - i)
		first, gap := above.Add(-step), step
		if first.After(newest) {
			first = newest
		}
		if j < len(articles) {
			if lo := positions[j]; !lo.After(first) && !first.Add(-(n-1)*step).After(lo) {
				// Stepping would reach the dated article after the run.
				gap = above.Sub(lo) / (n + 1)
				first = above.Add(-gap)
				if first.After(newest) {
					gap = newest.Sub(lo) / n
					first = newest
				}
				gap = max(gap, time.Microsecond)
			}
		}
		for k := i; k < j; k++ {
			positions[k] = first.Add(-time.Duration(k-i) * gap)
		}
		above = positions[j-1]
		i = j
	}
}

// needsEnrichment reports whether an article should have its web page
// fetched: it has a URL and lacks an image, a title or an excerpt.
// queueEnrichBacklog says the same in SQL: keep the two in step.
func needsEnrichment(a rss.Article) bool {
	return a.URL != "" && (a.ImageURL == "" || a.Title == "" || a.Excerpt == "")
}

// ReportImageFailed records that a reader's browser couldn't load article
// id's feed image (image_url), and queues the article's web page for
// enrichment, at enrichPriorityNew, so the page's image can stand in
// (rss.Article.CardImageFallbackURL): an article with a feed image is never
// queued when it's saved (needsEnrichment). Only the first report counts:
// it sets image_failed_at, which a changed image_url clears
// (saveArticles). The job is queued only when the article has a URL, has
// never been enriched (its page_* columns are empty, as for a job given
// up) and its feed is wanted (enrichWanted); a feed becoming wanted later
// queues its reported articles with its backlog (queueEnrichBacklog). An
// article without a feed image, or reported before, is left as it is.
// rss.ErrNoRecord if there's no article id.
func (r *ArticleRepository) ReportImageFailed(ctx context.Context, id string) error {
	stmt := `WITH reported AS (
			UPDATE articles SET image_failed_at = now()
			WHERE id = $1 AND image_url <> '' AND image_failed_at IS NULL
			RETURNING id, feed_id, url, page_image_url, page_title, page_description
		), queued AS (
			INSERT INTO enrich_jobs (article_id, priority)
			SELECT id, $2 FROM reported
			WHERE url <> '' AND page_image_url = '' AND page_title = '' AND page_description = ''
				AND ` + enrichWanted("reported.feed_id") + `
			ON CONFLICT (article_id) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM articles WHERE id = $1)`
	var exists bool
	if err := r.DB.QueryRowContext(ctx, stmt, id, enrichPriorityNew).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return rss.ErrNoRecord
	}
	return nil
}

// enrichCandidates returns which of articles may get an enrichment job when
// newly inserted, and at what priority. None may unless the feed is wanted
// (enrichWanted): jobs exist only for wanted feeds (#708). A wanted feed's
// save placed as a first save (prevFetched zero: never fetched, or not
// attempted for staleFeedAfter, such as a feed followed before its first
// fetch) may queue only the enrichFirstWanted articles with the latest
// positions, at enrichPriorityBacklog, like a feed becoming wanted; its
// later saves every article, at enrichPriorityNew.
func enrichCandidates(articles []rss.Article, positions []time.Time, prevFetched time.Time, wanted bool) (eligible []bool, priority int) {
	eligible = make([]bool, len(articles))
	if !wanted {
		return eligible, enrichPriorityNew
	}
	if !prevFetched.IsZero() {
		for i := range eligible {
			eligible[i] = true
		}
		return eligible, enrichPriorityNew
	}

	order := make([]int, len(articles))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		return positions[order[x]].After(positions[order[y]])
	})
	for _, i := range order[:min(len(order), enrichFirstWanted)] {
		eligible[i] = true
	}
	return eligible, enrichPriorityBacklog
}

// saveArticles upserts articles for feedID by (feed_id, external_id) and
// records a successful fetch of the feed: sets last_fetched_at and
// last_attempt_at to now() and clears last_error and consecutive_failures.
// A new article's timeline_at and backlog are set on insert (see timelineAt
// and isBacklog) and never change. A newly inserted article that
// needsEnrichment and is among enrichCandidates (only for a wanted feed:
// followed or in Discover's catalog) gets an enrich_jobs row; a feed
// becoming wanted gets its backlog queued elsewhere (queueEnrichBacklog).
// Every article sharing a
// non-empty canonical_url with an inserted or updated one, that one
// included, gets has_copies (never cleared). It locks the feed row so
// concurrent saves of a feed are ordered.
//
// Each follower who has looked in the last rss.NewsWindow (their
// timeline_marks.seen_at) gets their own position, a timeline_entries row,
// for each newly inserted article that is news or positioned within
// rss.NewsWindow of now (a first save's latest posts, for followers who
// subscribed before the feed was fetched): its timeline_at raised above
// what they've seen (see writeReaderPositions). Updated articles never get
// one: a position is set once.
//
// A save of a feed with no fetch attempt in the last staleFeedAfter (or none
// ever) is placed as a first save: prevFetched is taken as zero for
// timelineAt, isBacklog and enrichCandidates, since nobody was fetching the
// feed and when its new items appeared is unknown. The result is Behind on
// any other later save of two or more articles, all inserted.
// Callers dedupe external IDs.
func saveArticles(ctx context.Context, q querier, feedID string, articles []rss.Article) (rss.FetchResult, error) {
	var prevFetched, prevAttempt sql.NullTime
	var now time.Time
	var wanted bool
	if err := q.QueryRowContext(ctx, `SELECT last_fetched_at, last_attempt_at, now(),
			`+enrichWanted("feeds.id")+`
		FROM feeds WHERE id = $1 FOR UPDATE`, feedID).Scan(&prevFetched, &prevAttempt, &now, &wanted); err != nil {
		return rss.FetchResult{}, err
	}
	if !prevAttempt.Valid || !prevAttempt.Time.After(now.Add(-staleFeedAfter)) {
		prevFetched = sql.NullTime{}
	}
	positions := timelineAt(articles, prevFetched.Time, now)
	enrichable, priority := enrichCandidates(articles, positions, prevFetched.Time, wanted)

	// The conflict's WHERE skips unchanged articles, so they return no row.
	// An existing published_at is kept: some feeds bump the date on every edit.
	// published_date_only follows the kept published_at.
	// timeline_at and backlog are deliberately not updated.
	stmt := `INSERT INTO articles (feed_id, external_id, url, canonical_url, image_url, media_type, authors, title, summary, content, excerpt, published_at, published_date_only, timeline_at, backlog)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7::text[], '{}'), $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (feed_id, external_id) DO UPDATE SET
			url = EXCLUDED.url,
			canonical_url = EXCLUDED.canonical_url,
			image_url = EXCLUDED.image_url,
			image_failed_at = CASE WHEN articles.image_url = EXCLUDED.image_url
				THEN articles.image_failed_at END,
			media_type = EXCLUDED.media_type,
			authors = EXCLUDED.authors,
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			content = EXCLUDED.content,
			excerpt = EXCLUDED.excerpt,
			published_at = COALESCE(articles.published_at, EXCLUDED.published_at),
			published_date_only = CASE WHEN articles.published_at IS NULL
				THEN EXCLUDED.published_date_only ELSE articles.published_date_only END,
			updated_at = now()
		WHERE (articles.url, articles.image_url, articles.media_type, articles.authors, articles.title, articles.summary, articles.content, articles.excerpt)
				IS DISTINCT FROM (EXCLUDED.url, EXCLUDED.image_url, EXCLUDED.media_type, EXCLUDED.authors, EXCLUDED.title, EXCLUDED.summary, EXCLUDED.content, EXCLUDED.excerpt)
			OR (articles.published_at IS NULL AND EXCLUDED.published_at IS NOT NULL)
		RETURNING id, (xmax = 0) AS inserted`
	enrichStmt := `INSERT INTO enrich_jobs (article_id, priority) VALUES ($1, $2) ON CONFLICT DO NOTHING`

	var res rss.FetchResult
	var saved []string // the non-empty canonical URLs of inserted or updated articles
	// The inserted articles followers may get their own positions for (see
	// writeReaderPositions), and their shared positions.
	var arrivedIDs []string
	var arrivedAt []time.Time
	recent := now.Add(-rss.NewsWindow)
	for i, a := range articles {
		publishedAt := sql.NullTime{Time: a.PublishedAt, Valid: !a.PublishedAt.IsZero()}

		var (
			id       string
			inserted bool
		)
		err := q.QueryRowContext(ctx, stmt, feedID, a.ExternalID, a.URL, a.CanonicalURL, a.ImageURL, a.MediaType, pq.Array(a.Authors), a.Title, a.Summary, a.Content, a.Excerpt, publishedAt, a.PublishedDateOnly, positions[i], isBacklog(a, prevFetched.Time)).Scan(&id, &inserted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Unchanged.
		case err != nil:
			return rss.FetchResult{}, err
		case inserted:
			res.New++
			// Only a first save's backlog can be recent: a later save's is
			// over a day old.
			if !isBacklog(a, prevFetched.Time) || positions[i].After(recent) {
				arrivedIDs = append(arrivedIDs, id)
				arrivedAt = append(arrivedAt, positions[i])
			}
			if enrichable[i] && needsEnrichment(a) {
				if _, err := q.ExecContext(ctx, enrichStmt, id, priority); err != nil {
					return rss.FetchResult{}, err
				}
			}
		default:
			res.Updated++
		}
		if err == nil && a.CanonicalURL != "" {
			saved = append(saved, a.CanonicalURL)
		}
	}

	// An update can change an article's canonical_url, so updated rows count
	// too. This flags the existing copies as well as the saved article.
	if len(saved) > 0 {
		copiesStmt := `UPDATE articles a SET has_copies = true
			WHERE a.canonical_url = ANY($1) AND a.canonical_url <> '' AND NOT a.has_copies
				AND EXISTS (SELECT 1 FROM articles b WHERE b.canonical_url = a.canonical_url AND b.canonical_url <> '' AND b.id <> a.id)`
		if _, err := q.ExecContext(ctx, copiesStmt, pq.Array(saved)); err != nil {
			return rss.FetchResult{}, err
		}
	}

	if err := writeReaderPositions(ctx, q, feedID, arrivedIDs, arrivedAt, now); err != nil {
		return rss.FetchResult{}, err
	}

	// posts_per_week is recomputed after the inserts so it counts them.
	feedStmt := `UPDATE feeds SET last_fetched_at = now(), last_attempt_at = now(),
			last_error = '', consecutive_failures = 0, posts_per_week = ` + postsPerWeek + `
		WHERE id = $1`
	if _, err := q.ExecContext(ctx, feedStmt, feedID); err != nil {
		return rss.FetchResult{}, err
	}

	res.Behind = prevFetched.Valid && len(articles) >= 2 && res.New == len(articles)
	return res, nil
}

// writeReaderPositions gives each follower of feedID who has looked in the
// last rss.NewsWindow their own positions for the articles articleIDs, whose
// shared positions are positions: raisePositions of them against the
// follower's seen_up_to. A follower away longer than that needs none: their
// seen_up_to is below every news position's floor, so their positions would
// be the shared ones. Followers without a mark get none and read shared
// positions.
func writeReaderPositions(ctx context.Context, q querier, feedID string, articleIDs []string, positions []time.Time, now time.Time) error {
	if len(articleIDs) == 0 {
		return nil
	}

	stmt := `SELECT s.user_id, m.seen_up_to
		FROM subscriptions s
		JOIN timeline_marks m ON m.user_id = s.user_id
		WHERE s.feed_id = $1 AND m.seen_at > now() - make_interval(secs => $2)`

	rows, err := q.QueryContext(ctx, stmt, feedID, int(rss.NewsWindow.Seconds()))
	if err != nil {
		return err
	}
	defer rows.Close()

	var entries timelineEntries
	raised := map[int64][]time.Time{} // by seen_up_to, in µs
	for rows.Next() {
		var userID string
		var seenUpTo time.Time
		if err := rows.Scan(&userID, &seenUpTo); err != nil {
			return err
		}
		key := seenUpTo.UnixMicro()
		if _, ok := raised[key]; !ok {
			raised[key] = raisePositions(positions, seenUpTo, now)
		}
		entries.add(userID, articleIDs, raised[key])
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	return entries.insert(ctx, q, feedID)
}

// timelineEntries are timeline_entries rows of one feed to insert, as
// columns.
type timelineEntries struct {
	userIDs, articleIDs, positions []string
}

// add adds userID's positions for articleIDs.
func (e *timelineEntries) add(userID string, articleIDs []string, positions []time.Time) {
	for i, id := range articleIDs {
		e.userIDs = append(e.userIDs, userID)
		e.articleIDs = append(e.articleIDs, id)
		e.positions = append(e.positions, positions[i].Format(time.RFC3339Nano))
	}
}

// insert inserts the rows, of feedID's articles, in one statement. A row
// already there is kept as it is: a position is set once.
func (e *timelineEntries) insert(ctx context.Context, q querier, feedID string) error {
	if len(e.userIDs) == 0 {
		return nil
	}
	stmt := `INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at)
		SELECT x.user_id, x.article_id, $1, x.timeline_at
		FROM unnest($2::uuid[], $3::uuid[], $4::timestamptz[]) AS x (user_id, article_id, timeline_at)
		ON CONFLICT DO NOTHING`
	_, err := q.ExecContext(ctx, stmt, feedID, pq.Array(e.userIDs), pq.Array(e.articleIDs), pq.Array(e.positions))
	return err
}
