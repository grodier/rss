package psql

import (
	"database/sql"
	"errors"
	"time"
)

type Feed struct {
	ID          string
	Url         string
	SiteUrl     string
	Title       string
	Description string
	LastFetched time.Time
	CreatedAt   time.Time
}

type FeedRepository struct {
	DB *sql.DB
}

func NewFeedRepository(db *sql.DB) *FeedRepository {
	return &FeedRepository{DB: db}
}

func (r *FeedRepository) Create(feed Feed) (string, error) {
	stmt := `INSERT INTO feeds (url, site_url, title, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id string
	if err := r.DB.QueryRow(stmt, feed.Url, feed.SiteUrl, feed.Title, feed.Description).Scan(&id); err != nil {
		return "", err
	}

	return id, nil
}

func (r *FeedRepository) GetByID(id string) (Feed, error) {
	stmt := `SELECT id, url, site_url, title, description, created_at
		FROM feeds
		WHERE id = $1`

	var feed Feed
	if err := r.DB.QueryRow(stmt, id).Scan(&feed.ID, &feed.Url, &feed.SiteUrl, &feed.Title, &feed.Description, &feed.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Feed{}, ErrNoRecord
		} else {
			return Feed{}, err
		}
	}

	return feed, nil
}

func (r *FeedRepository) GetLatest() ([]Feed, error) {
	stmt := `SELECT id, url, site_url, title, description, created_at
		FROM feeds
		ORDER BY created_at DESC
		LIMIT 10`

	rows, err := r.DB.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []Feed

	for rows.Next() {
		var feed Feed
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
