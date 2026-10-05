package psql

import (
	"context"
	"database/sql"
	"errors"
	"github.com/grodier/rss/internal/rss"
)

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

// ListBySite returns a site's feeds ordered by title, then URL.
func (r *FeedRepository) ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error) {
	stmt := `SELECT id, site_id, url, site_url, title, description, created_at
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
		var feed rss.Feed
		if err := rows.Scan(&feed.ID, &feed.SiteID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt); err != nil {
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
	stmt := `SELECT id, site_id, url, site_url, title, description, created_at
		FROM feeds
		WHERE id = $1`

	var feed rss.Feed
	if err := r.DB.QueryRowContext(ctx, stmt, id).Scan(&feed.ID, &feed.SiteID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Feed{}, rss.ErrNoRecord
		} else {
			return rss.Feed{}, err
		}
	}

	return feed, nil
}

func (r *FeedRepository) GetLatest(ctx context.Context) ([]rss.Feed, error) {
	stmt := `SELECT id, site_id, url, site_url, title, description, created_at
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
		var feed rss.Feed
		err = rows.Scan(&feed.ID, &feed.SiteID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt)
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
