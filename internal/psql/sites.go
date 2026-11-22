package psql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type SiteRepository struct {
	DB *sql.DB
}

func NewSiteRepository(db *sql.DB) *SiteRepository {
	return &SiteRepository{DB: db}
}

// Upsert inserts the site or updates it by host and returns its ID.
func (r *SiteRepository) Upsert(ctx context.Context, s rss.Site) (string, error) {
	return upsertSite(ctx, r.DB, s, true)
}

// upsertSite inserts the site or updates it by host and returns its ID. An
// empty title or description doesn't overwrite a known one. Only details
// read from the site's home page (fromHome, discovery.Result.HomePage)
// replace an existing site's address, title and description: details read
// from a feed or another page (an article, a Medium author's page) describe
// a new site, but an existing one keeps its own, gaining only a title or
// description it lacks.
func upsertSite(ctx context.Context, q querier, s rss.Site, fromHome bool) (string, error) {
	stmt := `INSERT INTO sites (host, url, title, description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (host) DO UPDATE SET
			url = CASE WHEN $5 THEN EXCLUDED.url ELSE sites.url END,
			title = CASE WHEN $5 THEN COALESCE(NULLIF(EXCLUDED.title, ''), sites.title)
				ELSE COALESCE(NULLIF(sites.title, ''), EXCLUDED.title) END,
			description = CASE WHEN $5 THEN COALESCE(NULLIF(EXCLUDED.description, ''), sites.description)
				ELSE COALESCE(NULLIF(sites.description, ''), EXCLUDED.description) END,
			updated_at = now()
		RETURNING id`

	var id string
	if err := q.QueryRowContext(ctx, stmt, s.Host, s.URL, s.Title, s.Description, fromHome).Scan(&id); err != nil {
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

// GetIcon returns the site's stored icon, or rss.ErrNoRecord.
func (r *SiteRepository) GetIcon(ctx context.Context, siteID string) (rss.SiteIcon, error) {
	stmt := `SELECT site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at
		FROM site_icons
		WHERE site_id = $1`

	var icon rss.SiteIcon
	err := r.DB.QueryRowContext(ctx, stmt, siteID).Scan(&icon.SiteID, &icon.SourceURL, &icon.ContentType,
		&icon.Width, &icon.Height, &icon.Data, &icon.Hash, &icon.ETag, &icon.LastModified, &icon.FetchedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.SiteIcon{}, rss.ErrNoRecord
		}
		return rss.SiteIcon{}, err
	}
	return icon, nil
}

// IconHashes returns the icon hash of each of siteIDs that has an icon.
func (r *SiteRepository) IconHashes(ctx context.Context, siteIDs []string) (map[string]string, error) {
	hashes := map[string]string{}
	if len(siteIDs) == 0 {
		return hashes, nil
	}

	stmt := `SELECT site_id, hash FROM site_icons WHERE site_id = ANY($1)`

	rows, err := r.DB.QueryContext(ctx, stmt, pq.Array(siteIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		hashes[id] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return hashes, nil
}

// siteIconDue is true for a site ClaimIconCheck may claim: its check is
// due, and it's never been checked or has a subscribed feed. The admin's
// Overview counts the icon queue with it (AdminOverviewRepository.Queues).
const siteIconDue = `icon_next_check_at <= now()
	AND (icon_checked_at IS NULL
	     OR EXISTS (SELECT 1 FROM feeds f JOIN subscriptions s ON s.feed_id = f.id
	                WHERE f.site_id = sites.id))`

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
				WHERE ` + siteIconDue + `
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

// RekeyResult is what Rekey did.
type RekeyResult struct {
	Skipped                 bool // the version was current
	Feeds, Created, Deleted int
}

// Rekey moves each feed onto the site its key now names, when the stored
// rules version is older than version: a feed whose site is the bare host
// of the key that siteKey gives it (from its site_url, else its url) moves
// to that key's site, created if needed, whose root lookup is queued at the
// seed's priority so the site gets its own details, feeds and icon. A bare
// host's site left with no feeds is deleted. It then records version, all
// in one transaction. As upsertFeed, it never moves a feed between two
// sites with a path or two hosts. RekeyResult says what it did; nothing
// when the version is current.
func (r *SiteRepository) Rekey(ctx context.Context, version int, siteKey func(siteURL, feedURL string) (key, root string)) (RekeyResult, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return RekeyResult{}, err
	}
	defer tx.Rollback()

	// FOR UPDATE: a second deploy's re-key waits, then finds it current.
	var stored int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM site_key_rules FOR UPDATE`).Scan(&stored); err != nil {
		return RekeyResult{}, err
	}
	if stored >= version {
		return RekeyResult{Skipped: true}, nil
	}

	// Only feeds on a bare host's site can move. They're read in full
	// before any write, as a connection runs one statement at a time.
	type feedSite struct{ feedID, url, siteURL, siteID, host string }
	rows, err := tx.QueryContext(ctx, `SELECT f.id, f.url, f.site_url, s.id, s.host
		FROM feeds f JOIN sites s ON s.id = f.site_id
		WHERE position('/' in s.host) = 0`)
	if err != nil {
		return RekeyResult{}, err
	}
	var feeds []feedSite
	for rows.Next() {
		var f feedSite
		if err := rows.Scan(&f.feedID, &f.url, &f.siteURL, &f.siteID, &f.host); err != nil {
			rows.Close()
			return RekeyResult{}, err
		}
		feeds = append(feeds, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RekeyResult{}, err
	}

	var res RekeyResult
	sites := map[string]string{} // key -> site ID, for keys already handled
	emptied := map[string]bool{} // bare sites that lost a feed
	for _, f := range feeds {
		key, root := siteKey(f.siteURL, f.url)
		if !strings.Contains(key, "/") || discovery.KeyHost(key) != f.host {
			continue
		}
		id, ok := sites[key]
		if !ok {
			created := true
			err := tx.QueryRowContext(ctx, `INSERT INTO sites (host, url) VALUES ($1, $2)
				ON CONFLICT (host) DO NOTHING RETURNING id`, key, root).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				created = false
				err = tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE host = $1`, key).Scan(&id)
			}
			if err != nil {
				return RekeyResult{}, err
			}
			if created {
				res.Created++
			}
			// As the seed list queues one, after readers' lookups.
			if _, err := tx.ExecContext(ctx, `INSERT INTO lookups (site_key, url, priority, home_page)
				VALUES ($1, $2, 1, true) ON CONFLICT (site_key) DO NOTHING`, key, root); err != nil {
				return RekeyResult{}, err
			}
			sites[key] = id
		}
		if _, err := tx.ExecContext(ctx, `UPDATE feeds SET site_id = $2 WHERE id = $1`, f.feedID, id); err != nil {
			return RekeyResult{}, err
		}
		res.Feeds++
		emptied[f.siteID] = true
	}

	if len(emptied) > 0 {
		ids := make([]string, 0, len(emptied))
		for id := range emptied {
			ids = append(ids, id)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM sites s WHERE s.id = ANY($1)
			AND NOT EXISTS (SELECT 1 FROM feeds f WHERE f.site_id = s.id)`, pq.Array(ids))
		if err != nil {
			return RekeyResult{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return RekeyResult{}, err
		}
		res.Deleted = int(n)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE site_key_rules SET version = $1`, version); err != nil {
		return RekeyResult{}, err
	}
	return res, tx.Commit()
}
