package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

type SiteRepository struct {
	DB *sql.DB
}

func NewSiteRepository(db *sql.DB) *SiteRepository {
	return &SiteRepository{DB: db}
}

// Upsert inserts the site or updates it by host and returns its ID.
func (r *SiteRepository) Upsert(ctx context.Context, s rss.Site) (string, error) {
	return upsertSite(ctx, r.DB, s)
}

// upsertSite inserts the site or updates it by host and returns its ID. An
// empty title or description doesn't overwrite a known one.
func upsertSite(ctx context.Context, q querier, s rss.Site) (string, error) {
	stmt := `INSERT INTO sites (host, url, title, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (host) DO UPDATE SET
			url = EXCLUDED.url,
			title = COALESCE(NULLIF(EXCLUDED.title, ''), sites.title),
			description = COALESCE(NULLIF(EXCLUDED.description, ''), sites.description),
			updated_at = now()
		RETURNING id`

	var id string
	if err := q.QueryRowContext(ctx, stmt, s.Host, s.URL, s.Title, s.Description).Scan(&id); err != nil {
		return "", err
	}

	return id, nil
}

func (r *SiteRepository) GetByID(ctx context.Context, id string) (rss.Site, error) {
	stmt := `SELECT id, host, url, title, description, created_at, updated_at
		FROM sites
		WHERE id = $1`

	return r.get(ctx, stmt, id)
}

func (r *SiteRepository) GetByHost(ctx context.Context, host string) (rss.Site, error) {
	stmt := `SELECT id, host, url, title, description, created_at, updated_at
		FROM sites
		WHERE host = $1`

	return r.get(ctx, stmt, host)
}

func (r *SiteRepository) get(ctx context.Context, stmt string, arg any) (rss.Site, error) {
	var s rss.Site
	err := r.DB.QueryRowContext(ctx, stmt, arg).Scan(&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Site{}, rss.ErrNoRecord
		}
		return rss.Site{}, err
	}

	return s, nil
}
