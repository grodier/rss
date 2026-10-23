package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

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

// ClaimIconCheck claims a site whose icon check is due (icon_next_check_at
// <= now()) and that has never been checked or has a subscribed feed, by
// moving its icon_next_check_at to now() + lease. It returns the site and its
// current icon, zero if it has none; only the icon's SiteID, SourceURL, ETag
// and LastModified are loaded. If the claimer records nothing, the site is
// due again when the lease runs out. rss.ErrNoRecord when no site is due.
func (r *SiteRepository) ClaimIconCheck(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error) {
	stmt := `WITH claimed AS (
			UPDATE sites SET icon_next_check_at = now() + make_interval(secs => $1)
			WHERE id = (
				SELECT id FROM sites
				WHERE icon_next_check_at <= now()
				  AND (icon_checked_at IS NULL
				       OR EXISTS (SELECT 1 FROM feeds f JOIN subscriptions s ON s.feed_id = f.id
				                  WHERE f.site_id = sites.id))
				ORDER BY icon_next_check_at
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, host, url, title, description, created_at, updated_at
		)
		SELECT c.id, c.host, c.url, c.title, c.description, c.created_at, c.updated_at,
			COALESCE(i.site_id::text, ''), COALESCE(i.source_url, ''), COALESCE(i.etag, ''), COALESCE(i.last_modified, '')
		FROM claimed c
		LEFT JOIN site_icons i ON i.site_id = c.id`

	var (
		s    rss.Site
		icon rss.SiteIcon
	)
	err := r.DB.QueryRowContext(ctx, stmt, lease.Seconds()).Scan(
		&s.ID, &s.Host, &s.URL, &s.Title, &s.Description, &s.CreatedAt, &s.UpdatedAt,
		&icon.SiteID, &icon.SourceURL, &icon.ETag, &icon.LastModified,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Site{}, rss.SiteIcon{}, rss.ErrNoRecord
		}
		return rss.Site{}, rss.SiteIcon{}, err
	}
	return s, icon, nil
}

// SaveIcon stores icon as its site's icon, replacing any previous one, and
// records the check: icon_checked_at = now(), icon_next_check_at = next.
// rss.ErrNoRecord if the site doesn't exist.
func (r *SiteRepository) SaveIcon(ctx context.Context, icon rss.SiteIcon, next time.Time) error {
	stmt := `WITH site AS (
			UPDATE sites SET icon_checked_at = now(), icon_next_check_at = $10
			WHERE id = $1
			RETURNING id
		)
		INSERT INTO site_icons (site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at)
		SELECT id, $2, $3, $4, $5, $6, $7, $8, $9, now() FROM site
		ON CONFLICT (site_id) DO UPDATE SET
			source_url = EXCLUDED.source_url,
			content_type = EXCLUDED.content_type,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			data = EXCLUDED.data,
			hash = EXCLUDED.hash,
			etag = EXCLUDED.etag,
			last_modified = EXCLUDED.last_modified,
			fetched_at = EXCLUDED.fetched_at`

	return execOne(ctx, r.DB, stmt, icon.SiteID, icon.SourceURL, icon.ContentType, icon.Width, icon.Height,
		icon.Data, icon.Hash, icon.ETag, icon.LastModified, next)
}

// RemoveIcon deletes site siteID's icon, if it has one, and records the
// check: icon_checked_at = now(), icon_next_check_at = next. rss.ErrNoRecord
// if the site doesn't exist.
func (r *SiteRepository) RemoveIcon(ctx context.Context, siteID string, next time.Time) error {
	stmt := `WITH removed AS (
			DELETE FROM site_icons WHERE site_id = $1
		)
		UPDATE sites SET icon_checked_at = now(), icon_next_check_at = $2
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, siteID, next)
}

// KeepIcon leaves site siteID's icon as it is and sets its
// icon_next_check_at to next. checked records that the icon was confirmed
// (icon_checked_at = now()), as when it hadn't changed; a failed check
// passes false. rss.ErrNoRecord if the site doesn't exist.
func (r *SiteRepository) KeepIcon(ctx context.Context, siteID string, checked bool, next time.Time) error {
	stmt := `UPDATE sites SET
			icon_checked_at = CASE WHEN $2 THEN now() ELSE icon_checked_at END,
			icon_next_check_at = $3
		WHERE id = $1`

	return execOne(ctx, r.DB, stmt, siteID, checked, next)
}
