package psql

import (
	"context"
	"database/sql"
	"errors"
	"github.com/grodier/rss/internal/rss"

	"github.com/lib/pq"
)

type FeedRepository struct {
	DB *sql.DB
}

func NewFeedRepository(db *sql.DB) *FeedRepository {
	return &FeedRepository{DB: db}
}

func (r *FeedRepository) Create(ctx context.Context, feed rss.Feed) (string, error) {
	stmt := `INSERT INTO feeds (url, site_url, title, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id string
	if err := r.DB.QueryRowContext(ctx, stmt, feed.Url, feed.SiteUrl, feed.Title, feed.Description).Scan(&id); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", rss.ErrDuplicateFeed
		}
		return "", err
	}

	return id, nil
}

func (r *FeedRepository) GetByID(ctx context.Context, id string) (rss.Feed, error) {
	stmt := `SELECT id, url, site_url, title, description, created_at
		FROM feeds
		WHERE id = $1`

	var feed rss.Feed
	if err := r.DB.QueryRowContext(ctx, stmt, id).Scan(&feed.ID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Feed{}, rss.ErrNoRecord
		} else {
			return rss.Feed{}, err
		}
	}

	return feed, nil
}

func (r *FeedRepository) GetLatest(ctx context.Context) ([]rss.Feed, error) {
	stmt := `SELECT id, url, site_url, title, description, created_at
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
		err = rows.Scan(&feed.ID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt)
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
