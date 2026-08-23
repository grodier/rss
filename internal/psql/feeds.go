package psql

import (
	"database/sql"
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
	return "", nil
}

func (r *FeedRepository) GetByID(id string) (Feed, error) {
	return Feed{}, nil
}

func (r *FeedRepository) GetAll() ([]Feed, error) {
	return nil, nil
}
