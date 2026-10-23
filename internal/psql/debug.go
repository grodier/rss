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

// maxArticleCopies is how many copies ArticleDebug returns.
const maxArticleCopies = 20

// ArticleDebug returns the article's enrich job, its copies in other feeds
// (same non-empty canonical_url, oldest first, at most 20) and userID's read
// and save times. Missing rows are zero values, not errors.
func (r *DebugRepository) ArticleDebug(ctx context.Context, userID, articleID string) (rss.ArticleDebug, error) {
	var d rss.ArticleDebug

	jobStmt := `SELECT priority, attempts, next_attempt_at, last_error, created_at
		FROM enrich_jobs
		WHERE article_id = $1`
	j := &d.EnrichJob
	err := r.DB.QueryRowContext(ctx, jobStmt, articleID).Scan(&j.Priority, &j.Attempts, &j.NextAttemptAt, &j.LastError, &j.CreatedAt)
	switch {
	case err == nil:
		j.Exists = true
	case !errors.Is(err, sql.ErrNoRows):
		return rss.ArticleDebug{}, err
	}

	// Uses articles_canonical_url_idx (a partial index, so the query repeats
	// its predicate).
	copiesStmt := `SELECT a.id, a.feed_id, COALESCE(NULLIF(f.title, ''), f.url), a.created_at, a.timeline_at
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		JOIN articles me ON me.id = $1
		WHERE me.canonical_url <> '' AND a.canonical_url <> '' AND a.canonical_url = me.canonical_url AND a.id <> me.id
		ORDER BY a.created_at, a.id
		LIMIT $2`
	rows, err := r.DB.QueryContext(ctx, copiesStmt, articleID, maxArticleCopies)
	if err != nil {
		return rss.ArticleDebug{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c rss.ArticleCopy
		if err := rows.Scan(&c.ArticleID, &c.FeedID, &c.FeedTitle, &c.CreatedAt, &c.TimelineAt); err != nil {
			return rss.ArticleDebug{}, err
		}
		d.Copies = append(d.Copies, c)
	}
	if err := rows.Err(); err != nil {
		return rss.ArticleDebug{}, err
	}

	userStmt := `SELECT (SELECT read_at FROM reads WHERE user_id = $1 AND article_id = $2),
			(SELECT saved_at FROM saves WHERE user_id = $1 AND article_id = $2)`
	var readAt, savedAt sql.NullTime
	if err := r.DB.QueryRowContext(ctx, userStmt, userID, articleID).Scan(&readAt, &savedAt); err != nil {
		return rss.ArticleDebug{}, err
	}
	d.ReadAt = readAt.Time
	d.SavedAt = savedAt.Time

	return d, nil
}
