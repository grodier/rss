package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// feedColumns are the columns scanFeed reads, in order.
const feedColumns = `id, site_id, url, site_url, title, description, last_fetched_at, created_at`

// scanFeed scans a row selected with feedColumns.
func scanFeed(row interface{ Scan(...any) error }) (rss.Feed, error) {
	var feed rss.Feed
	var lastFetched sql.NullTime
	if err := row.Scan(&feed.ID, &feed.SiteID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &lastFetched, &feed.CreatedAt); err != nil {
		return rss.Feed{}, err
	}
	feed.LastFetched = lastFetched.Time
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
// discovered from).
func (r *FeedRepository) Upsert(ctx context.Context, f rss.Feed) (string, error) {
	return upsertFeed(ctx, r.DB, f)
}

func upsertFeed(ctx context.Context, q querier, f rss.Feed) (string, error) {
	stmt := `INSERT INTO feeds (url, site_url, title, description, site_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (url) DO UPDATE SET
			site_url = EXCLUDED.site_url,
			title = COALESCE(NULLIF(EXCLUDED.title, ''), feeds.title),
			description = COALESCE(NULLIF(EXCLUDED.description, ''), feeds.description)
		RETURNING id`

	var id string
	if err := q.QueryRowContext(ctx, stmt, f.Url, f.SiteUrl, f.Title, f.Description, f.SiteID).Scan(&id); err != nil {
		return "", err
	}

	return id, nil
}

// SaveFetch records a successful fetch of feed f.ID in one transaction:
// updates the feed's title, description and site URL (keeping the current
// value when the new one is empty), saves articles (see saveArticles) and
// sets last_fetched_at. It returns rss.ErrNoRecord if the feed doesn't exist.
func (r *FeedRepository) SaveFetch(ctx context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.FetchResult{}, err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `UPDATE feeds SET
			title = COALESCE(NULLIF($2, ''), title),
			description = COALESCE(NULLIF($3, ''), description),
			site_url = COALESCE(NULLIF($4, ''), site_url)
		WHERE id = $1`

	result, err := tx.ExecContext(ctx, stmt, f.ID, f.Title, f.Description, f.SiteUrl)
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

	if err := tx.Commit(); err != nil {
		return rss.FetchResult{}, err
	}

	return res, nil
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

func (r *FeedRepository) GetLatest(ctx context.Context) ([]rss.Feed, error) {
	stmt := `SELECT ` + feedColumns + `
		FROM feeds
		ORDER BY created_at DESC
		LIMIT 10`

	rows, err := r.DB.QueryContext(ctx, stmt)
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

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return feeds, nil
}
