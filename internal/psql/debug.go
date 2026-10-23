package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// DebugRepository reads what debug panels show beyond the usual rows. It is
// read-only.
type DebugRepository struct {
	DB *sql.DB
}

func NewDebugRepository(db *sql.DB) *DebugRepository {
	return &DebugRepository{DB: db}
}

// SiteDebug returns the site's icon bookkeeping and stored icon (without its
// data), or rss.ErrNoRecord if the site doesn't exist.
func (r *DebugRepository) SiteDebug(ctx context.Context, siteID string) (rss.SiteDebug, error) {
	stmt := `SELECT s.icon_checked_at, s.icon_next_check_at,
			i.site_id, i.source_url, i.content_type, i.width, i.height, octet_length(i.data),
			i.hash, i.etag, i.last_modified, i.fetched_at
		FROM sites s
		LEFT JOIN site_icons i ON i.site_id = s.id
		WHERE s.id = $1`

	var d rss.SiteDebug
	var checkedAt, fetchedAt sql.NullTime
	var iconSiteID, sourceURL, contentType, hash, etag, lastModified sql.NullString
	var width, height, size sql.NullInt64
	err := r.DB.QueryRowContext(ctx, stmt, siteID).Scan(&checkedAt, &d.IconNextCheckAt,
		&iconSiteID, &sourceURL, &contentType, &width, &height, &size,
		&hash, &etag, &lastModified, &fetchedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.SiteDebug{}, rss.ErrNoRecord
		}
		return rss.SiteDebug{}, err
	}

	d.IconCheckedAt = checkedAt.Time
	if iconSiteID.Valid {
		d.Icon = rss.SiteIcon{
			SiteID:       iconSiteID.String,
			SourceURL:    sourceURL.String,
			ContentType:  contentType.String,
			Width:        int(width.Int64),
			Height:       int(height.Int64),
			Hash:         hash.String,
			ETag:         etag.String,
			LastModified: lastModified.String,
			FetchedAt:    fetchedAt.Time,
		}
		d.IconBytes = int(size.Int64)
	}
	return d, nil
}

// FeedDebug returns the debug counts of each of feedIDs. IDs that don't
// exist are absent from the map.
func (r *DebugRepository) FeedDebug(ctx context.Context, feedIDs []string) (map[string]rss.FeedDebug, error) {
	out := map[string]rss.FeedDebug{}
	if len(feedIDs) == 0 {
		return out, nil
	}

	stmt := `SELECT f.id,
			(SELECT count(*) FROM articles a WHERE a.feed_id = f.id),
			(SELECT max(a.created_at) FROM articles a WHERE a.feed_id = f.id),
			(SELECT max(a.timeline_at) FROM articles a WHERE a.feed_id = f.id),
			(SELECT count(*) FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE a.feed_id = f.id)
		FROM feeds f
		WHERE f.id = ANY($1)`

	rows, err := r.DB.QueryContext(ctx, stmt, pq.Array(feedIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var d rss.FeedDebug
		var newestCreated, newestTimeline sql.NullTime
		if err := rows.Scan(&id, &d.Articles, &newestCreated, &newestTimeline, &d.EnrichJobs); err != nil {
			return nil, err
		}
		d.NewestCreatedAt = newestCreated.Time
		d.NewestTimelineAt = newestTimeline.Time
		out[id] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// ArticlesByExternalID returns the feed's stored articles with these
// external IDs, by external ID. IDs with no article are absent from the map.
func (r *DebugRepository) ArticlesByExternalID(ctx context.Context, feedID string, externalIDs []string) (map[string]rss.Article, error) {
	out := map[string]rss.Article{}
	if len(externalIDs) == 0 {
		return out, nil
	}

	stmt := `SELECT ` + articleColumns + `
		FROM articles
		WHERE feed_id = $1 AND external_id = ANY($2)`

	rows, err := r.DB.QueryContext(ctx, stmt, feedID, pq.Array(externalIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out[a.ExternalID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}
