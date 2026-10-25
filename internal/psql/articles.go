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
	"id", "feed_id", "external_id", "url", "canonical_url", "image_url", "media_type", "title", "summary", "content",
	"excerpt", "page_image_url", "page_title", "page_description", "published_at", "published_date_only", "timeline_at", "created_at", "updated_at",
}

var (
	// articleColumns is articleColumnNames as a select list.
	articleColumns = strings.Join(articleColumnNames, ", ")
	// qualifiedArticleColumns prefixes each column with the articles alias a,
	// for queries that join tables with clashing column names.
	qualifiedArticleColumns = "a." + strings.Join(articleColumnNames, ", a.")
	// listArticleColumns and qualifiedListArticleColumns are the select lists
	// for articles shown in a list (see articleListColumns).
	listArticleColumns          = articleListColumns("")
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
	var publishedAt sql.NullTime
	err := row.Scan(&a.ID, &a.FeedID, &a.ExternalID, &a.URL, &a.CanonicalURL, &a.ImageURL, &a.MediaType, &a.Title, &a.Summary, &a.Content, &a.Excerpt, &a.PageImageURL, &a.PageTitle, &a.PageDescription, &publishedAt, &a.PublishedDateOnly, &a.TimelineAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return rss.Article{}, err
	}
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

// ListByFeed returns up to limit of feedID's articles, newest first by
// published date (else created date) then ID, starting after before (from
// the start if before is zero). Summary and Content are left empty.
func (r *ArticleRepository) ListByFeed(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
	stmt := `SELECT ` + listArticleColumns + `
		FROM articles
		WHERE feed_id = $1
			AND ($2::timestamptz IS NULL OR (COALESCE(published_at, created_at), id) < ($2, $3::uuid))
		ORDER BY COALESCE(published_at, created_at) DESC, id DESC
		LIMIT $4`

	at, id := cursorArgs(before)
	rows, err := r.DB.QueryContext(ctx, stmt, feedID, at, id, limit)
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
// to that filter shows (see timelineWhere), ordered by timeline_at then ID,
// newest first, starting after before (from the start if before is zero).
//
// Copies of one article (same non-empty canonical_url) in several of those
// feeds are one entry: the earliest copy to arrive (smallest timeline_at,
// then ID), at that copy's position, with AlsoIn listing the other feeds
// that have a copy, ordered by title. Feed titles are the user's names for
// their subscriptions where they've set one.
//
// Summary and Content are left empty.
func (r *ArticleRepository) ListTimeline(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
	at, id := cursorArgs(before)
	args := []any{userID, at, id, limit}

	// Articles with no canonical URL key on their own ID, so they're never
	// collapsed. Copies collapse after filtering, so a left-out feed's copy
	// never takes an entry's place.
	stmt := `WITH mine AS (
			SELECT ` + qualifiedListArticleColumns + `,
				CASE WHEN a.canonical_url = '' THEN a.id::text ELSE a.canonical_url END AS dedup_key
			FROM subscriptions sub
			JOIN articles a ON a.feed_id = sub.feed_id
			WHERE sub.user_id = $1` + timelineWhere(filter, &args) + `
		), firsts AS (
			SELECT DISTINCT ON (dedup_key) *
			FROM mine
			ORDER BY dedup_key, timeline_at, id
		)
		SELECT ` + qualifiedArticleColumns + `, ` + qualifiedFeedColumnsTitled(subscriptionTitle) + `
		FROM firsts a
		JOIN feeds f ON f.id = a.feed_id
		JOIN subscriptions sub ON sub.user_id = $1 AND sub.feed_id = a.feed_id
		WHERE ($2::timestamptz IS NULL OR (a.timeline_at, a.id) < ($2, $3::uuid))
		ORDER BY a.timeline_at DESC, a.id DESC
		LIMIT $4`

	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []rss.ArticleWithFeed{}
	for rows.Next() {
		var item rss.ArticleWithFeed
		// scanArticle and scanFeed each take a Scan func and read their own
		// columns, so nest them: the feed's destinations go after the article's.
		article, err := scanArticle(scanFunc(func(articleDests ...any) error {
			var feedErr error
			item.Feed, feedErr = scanFeed(scanFunc(func(feedDests ...any) error {
				return rows.Scan(append(articleDests, feedDests...)...)
			}))
			return feedErr
		}))
		if err != nil {
			return nil, err
		}
		item.Article = article
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.fillAlsoIn(ctx, userID, items); err != nil {
		return nil, err
	}

	return items, nil
}

// timelineWhere returns the SQL conditions on sub (subscriptions) and a
// (articles) for filter, appending its parameters to args. Each condition
// starts with AND, so the result follows another condition; it's "" for the
// zero filter.
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
// sub can be NULL (a LEFT JOIN, as in ListStarred: a star from a feed the
// user no longer subscribes to). Then Show conditions fail (the feed has no
// tags and isn't chosen) and Leave out conditions pass.
func timelineWhere(filter rss.TimelineFilter, args *[]any) string {
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
				WHERE st.user_id = sub.user_id AND st.feed_id = sub.feed_id AND st.tag_id = ANY(` + tagIDs + `))`
	}

	var where strings.Builder
	if filter.OnlyIncluded {
		where.WriteString(`
				AND COALESCE(sub.feed_id = ANY(` + ids(filter.IncludeFeedIDs) + `) OR ` + hasTag(ids(filter.IncludeTagIDs)) + `, false)`)
	}
	if len(filter.ExcludeFeedIDs) > 0 {
		where.WriteString(`
				AND NOT COALESCE(sub.feed_id = ANY(` + ids(filter.ExcludeFeedIDs) + `), false)`)
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
				AND EXISTS (SELECT 1 FROM feeds rate WHERE rate.id = a.feed_id AND rate.posts_per_week <= $%d)`, len(*args))
	}
	if filter.MediaOnly {
		where.WriteString(`
				AND a.media_type <> ''`)
	}
	const text = `concat_ws(' ', a.title, a.page_title, a.excerpt)`
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

// timelineAt returns each article's timeline position, in the order of
// articles, for a save at now of a feed whose previous successful fetch
// was prevFetched (zero for the feed's first save).
//
// News (undated, or published since prevFetched - backlogSlack) is placed
// at now, newest-published first with undated items ahead of dated ones,
// each 1 µs below the one before. Backlog is placed at its published date,
// clamped to now. Undated backlog (a first fetch of an undated feed) is
// treated like news.
func timelineAt(articles []rss.Article, prevFetched, now time.Time) []time.Time {
	cutoff := prevFetched.Add(-backlogSlack)
	positions := make([]time.Time, len(articles))

	var news []int
	for i, a := range articles {
		backlog := !a.PublishedAt.IsZero() && (prevFetched.IsZero() || a.PublishedAt.Before(cutoff))
		if backlog {
			positions[i] = a.PublishedAt
			if positions[i].After(now) {
				positions[i] = now
			}
			continue
		}
		news = append(news, i)
	}

	// Undated items count as published now, so they sort ahead of dated ones.
	published := func(i int) time.Time {
		if t := articles[i].PublishedAt; !t.IsZero() {
			return t
		}
		return now
	}
	sort.SliceStable(news, func(x, y int) bool {
		return published(news[x]).After(published(news[y]))
	})
	for n, i := range news {
		positions[i] = now.Add(-time.Duration(n) * time.Microsecond)
	}

	return positions
}

// enrichFirstSave is how many articles of a feed's first save, by latest
// timeline position, may get an enrichment job.
const enrichFirstSave = 20

// Priorities of enrichment jobs; lower goes first.
const (
	enrichPriorityNew     = 0 // new arrivals
	enrichPriorityBacklog = 1 // a feed's first save
)

// needsEnrichment reports whether an article should have its web page
// fetched: it has a URL and lacks an image, a title or an excerpt.
func needsEnrichment(a rss.Article) bool {
	return a.URL != "" && (a.ImageURL == "" || a.Title == "" || a.Excerpt == "")
}

// enrichCandidates returns which of articles may get an enrichment job when
// newly inserted, and at what priority. On a feed's first save (prevFetched
// zero) only the enrichFirstSave articles with the latest positions may, at
// enrichPriorityBacklog; on later saves every article may, at
// enrichPriorityNew.
func enrichCandidates(articles []rss.Article, positions []time.Time, prevFetched time.Time) (eligible []bool, priority int) {
	eligible = make([]bool, len(articles))
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
	for _, i := range order[:min(len(order), enrichFirstSave)] {
		eligible[i] = true
	}
	return eligible, enrichPriorityBacklog
}

// saveArticles upserts articles for feedID by (feed_id, external_id) and
// records a successful fetch of the feed: sets last_fetched_at and
// last_attempt_at to now() and clears last_error and consecutive_failures.
// A new article's timeline_at is set on insert (see timelineAt) and never
// changes. A newly inserted article that needsEnrichment and is among
// enrichCandidates gets an enrich_jobs row. It locks the feed row so
// concurrent saves of a feed are ordered. Callers dedupe external IDs.
func saveArticles(ctx context.Context, q querier, feedID string, articles []rss.Article) (rss.FetchResult, error) {
	var prevFetched sql.NullTime
	var now time.Time
	if err := q.QueryRowContext(ctx, `SELECT last_fetched_at, now() FROM feeds WHERE id = $1 FOR UPDATE`, feedID).Scan(&prevFetched, &now); err != nil {
		return rss.FetchResult{}, err
	}
	positions := timelineAt(articles, prevFetched.Time, now)
	enrichable, priority := enrichCandidates(articles, positions, prevFetched.Time)

	// The conflict's WHERE skips unchanged articles, so they return no row.
	// An existing published_at is kept: some feeds bump the date on every edit.
	// published_date_only follows the kept published_at.
	// timeline_at is deliberately not updated.
	stmt := `INSERT INTO articles (feed_id, external_id, url, canonical_url, image_url, media_type, title, summary, content, excerpt, published_at, published_date_only, timeline_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (feed_id, external_id) DO UPDATE SET
			url = EXCLUDED.url,
			canonical_url = EXCLUDED.canonical_url,
			image_url = EXCLUDED.image_url,
			media_type = EXCLUDED.media_type,
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			content = EXCLUDED.content,
			excerpt = EXCLUDED.excerpt,
			published_at = COALESCE(articles.published_at, EXCLUDED.published_at),
			published_date_only = CASE WHEN articles.published_at IS NULL
				THEN EXCLUDED.published_date_only ELSE articles.published_date_only END,
			updated_at = now()
		WHERE (articles.url, articles.image_url, articles.media_type, articles.title, articles.summary, articles.content, articles.excerpt)
				IS DISTINCT FROM (EXCLUDED.url, EXCLUDED.image_url, EXCLUDED.media_type, EXCLUDED.title, EXCLUDED.summary, EXCLUDED.content, EXCLUDED.excerpt)
			OR (articles.published_at IS NULL AND EXCLUDED.published_at IS NOT NULL)
		RETURNING id, (xmax = 0) AS inserted`
	enrichStmt := `INSERT INTO enrich_jobs (article_id, priority) VALUES ($1, $2) ON CONFLICT DO NOTHING`

	var res rss.FetchResult
	for i, a := range articles {
		publishedAt := sql.NullTime{Time: a.PublishedAt, Valid: !a.PublishedAt.IsZero()}

		var (
			id       string
			inserted bool
		)
		err := q.QueryRowContext(ctx, stmt, feedID, a.ExternalID, a.URL, a.CanonicalURL, a.ImageURL, a.MediaType, a.Title, a.Summary, a.Content, a.Excerpt, publishedAt, a.PublishedDateOnly, positions[i]).Scan(&id, &inserted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Unchanged.
		case err != nil:
			return rss.FetchResult{}, err
		case inserted:
			res.New++
			if enrichable[i] && needsEnrichment(a) {
				if _, err := q.ExecContext(ctx, enrichStmt, id, priority); err != nil {
					return rss.FetchResult{}, err
				}
			}
		default:
			res.Updated++
		}
	}

	// posts_per_week is recomputed after the inserts so it counts them.
	feedStmt := `UPDATE feeds SET last_fetched_at = now(), last_attempt_at = now(),
			last_error = '', consecutive_failures = 0, posts_per_week = ` + postsPerWeek + `
		WHERE id = $1`
	if _, err := q.ExecContext(ctx, feedStmt, feedID); err != nil {
		return rss.FetchResult{}, err
	}

	return res, nil
}
