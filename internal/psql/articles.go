package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

type ArticleRepository struct {
	DB *sql.DB
}

func NewArticleRepository(db *sql.DB) *ArticleRepository {
	return &ArticleRepository{DB: db}
}

// ListByFeed returns a feed's newest articles first (by published date, else
// when we first saw them), at most limit.
func (r *ArticleRepository) ListByFeed(ctx context.Context, feedID string, limit int) ([]rss.Article, error) {
	stmt := `SELECT id, feed_id, external_id, url, title, summary, content, published_at, created_at, updated_at
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
		var a rss.Article
		var publishedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.FeedID, &a.ExternalID, &a.URL, &a.Title, &a.Summary, &a.Content, &publishedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.PublishedAt = publishedAt.Time
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return articles, nil
}

// saveArticles upserts articles for feedID by (feed_id, external_id) and
// sets the feed's last_fetched_at to now(). Callers dedupe external IDs.
func saveArticles(ctx context.Context, q querier, feedID string, articles []rss.Article) (rss.FetchResult, error) {
	// The conflict's WHERE skips unchanged articles, so they return no row.
	// An existing published_at is kept: some feeds bump the date on every edit.
	stmt := `INSERT INTO articles (feed_id, external_id, url, title, summary, content, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (feed_id, external_id) DO UPDATE SET
			url = EXCLUDED.url,
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			content = EXCLUDED.content,
			published_at = COALESCE(articles.published_at, EXCLUDED.published_at),
			updated_at = now()
		WHERE (articles.url, articles.title, articles.summary, articles.content)
				IS DISTINCT FROM (EXCLUDED.url, EXCLUDED.title, EXCLUDED.summary, EXCLUDED.content)
			OR (articles.published_at IS NULL AND EXCLUDED.published_at IS NOT NULL)
		RETURNING (xmax = 0) AS inserted`

	var res rss.FetchResult
	for _, a := range articles {
		publishedAt := sql.NullTime{Time: a.PublishedAt, Valid: !a.PublishedAt.IsZero()}

		var inserted bool
		err := q.QueryRowContext(ctx, stmt, feedID, a.ExternalID, a.URL, a.Title, a.Summary, a.Content, publishedAt).Scan(&inserted)
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

	if _, err := q.ExecContext(ctx, `UPDATE feeds SET last_fetched_at = now() WHERE id = $1`, feedID); err != nil {
		return rss.FetchResult{}, err
	}

	return res, nil
}
