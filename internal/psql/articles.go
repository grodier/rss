package psql

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// articleColumnNames are the columns scanArticle reads, in order.
var articleColumnNames = []string{
	"id", "feed_id", "external_id", "url", "canonical_url", "image_url", "title", "summary", "content", "excerpt",
	"published_at", "timeline_at", "created_at", "updated_at",
}

var (
	// articleColumns is articleColumnNames as a select list.
	articleColumns = strings.Join(articleColumnNames, ", ")
	// qualifiedArticleColumns prefixes each column with the articles alias a,
	// for queries that join tables with clashing column names.
	qualifiedArticleColumns = "a." + strings.Join(articleColumnNames, ", a.")
)

// scanArticle scans a row selected with articleColumns.
func scanArticle(row interface{ Scan(...any) error }) (rss.Article, error) {
	var a rss.Article
	var publishedAt sql.NullTime
	err := row.Scan(&a.ID, &a.FeedID, &a.ExternalID, &a.URL, &a.CanonicalURL, &a.ImageURL, &a.Title, &a.Summary, &a.Content, &a.Excerpt, &publishedAt, &a.TimelineAt, &a.CreatedAt, &a.UpdatedAt)
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

// ListByFeed returns a feed's newest articles first (by published date, else
// when we first saw them), at most limit.
func (r *ArticleRepository) ListByFeed(ctx context.Context, feedID string, limit int) ([]rss.Article, error) {
	stmt := `SELECT ` + articleColumns + `
		FROM articles
		WHERE feed_id = $1
		ORDER BY COALESCE(published_at, created_at) DESC, id
		LIMIT $2`

	rows, err := r.DB.QueryContext(ctx, stmt, feedID, limit)
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

// ListTimeline returns up to limit articles from the feeds userID subscribes
// to, ordered by timeline_at then ID, newest first, starting after before
// (from the start if before is zero).
func (r *ArticleRepository) ListTimeline(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
	stmt := `SELECT ` + qualifiedArticleColumns + `, ` + qualifiedFeedColumns + `
		FROM subscriptions sub
		JOIN articles a ON a.feed_id = sub.feed_id
		JOIN feeds f ON f.id = a.feed_id
		WHERE sub.user_id = $1
			AND ($2::timestamptz IS NULL OR (a.timeline_at, a.id) < ($2, $3::uuid))
		ORDER BY a.timeline_at DESC, a.id DESC
		LIMIT $4`

	var at sql.NullTime
	var id sql.NullString
	if !before.IsZero() {
		at = sql.NullTime{Time: before.At, Valid: true}
		id = sql.NullString{String: before.ID, Valid: true}
	}

	rows, err := r.DB.QueryContext(ctx, stmt, userID, at, id, limit)
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

	return items, nil
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

// saveArticles upserts articles for feedID by (feed_id, external_id) and
// records a successful fetch of the feed: sets last_fetched_at and
// last_attempt_at to now() and clears last_error and consecutive_failures.
// A new article's timeline_at is set on insert (see timelineAt) and never
// changes. It locks the feed row so concurrent saves of a feed are ordered.
// Callers dedupe external IDs.
func saveArticles(ctx context.Context, q querier, feedID string, articles []rss.Article) (rss.FetchResult, error) {
	var prevFetched sql.NullTime
	var now time.Time
	if err := q.QueryRowContext(ctx, `SELECT last_fetched_at, now() FROM feeds WHERE id = $1 FOR UPDATE`, feedID).Scan(&prevFetched, &now); err != nil {
		return rss.FetchResult{}, err
	}
	positions := timelineAt(articles, prevFetched.Time, now)

	// The conflict's WHERE skips unchanged articles, so they return no row.
	// An existing published_at is kept: some feeds bump the date on every edit.
	// timeline_at is deliberately not updated.
	stmt := `INSERT INTO articles (feed_id, external_id, url, canonical_url, image_url, title, summary, content, excerpt, published_at, timeline_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (feed_id, external_id) DO UPDATE SET
			url = EXCLUDED.url,
			canonical_url = EXCLUDED.canonical_url,
			image_url = EXCLUDED.image_url,
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			content = EXCLUDED.content,
			excerpt = EXCLUDED.excerpt,
			published_at = COALESCE(articles.published_at, EXCLUDED.published_at),
			updated_at = now()
		WHERE (articles.url, articles.image_url, articles.title, articles.summary, articles.content, articles.excerpt)
				IS DISTINCT FROM (EXCLUDED.url, EXCLUDED.image_url, EXCLUDED.title, EXCLUDED.summary, EXCLUDED.content, EXCLUDED.excerpt)
			OR (articles.published_at IS NULL AND EXCLUDED.published_at IS NOT NULL)
		RETURNING (xmax = 0) AS inserted`

	var res rss.FetchResult
	for i, a := range articles {
		publishedAt := sql.NullTime{Time: a.PublishedAt, Valid: !a.PublishedAt.IsZero()}

		var inserted bool
		err := q.QueryRowContext(ctx, stmt, feedID, a.ExternalID, a.URL, a.CanonicalURL, a.ImageURL, a.Title, a.Summary, a.Content, a.Excerpt, publishedAt, positions[i]).Scan(&inserted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Unchanged.
		case err != nil:
			return rss.FetchResult{}, err
		case inserted:
			res.New++
		default:
			res.Updated++
		}
	}

	feedStmt := `UPDATE feeds SET last_fetched_at = now(), last_attempt_at = now(),
			last_error = '', consecutive_failures = 0
		WHERE id = $1`
	if _, err := q.ExecContext(ctx, feedStmt, feedID); err != nil {
		return rss.FetchResult{}, err
	}

	return res, nil
}
