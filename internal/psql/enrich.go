package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
)

// EnrichRepository hands out enrich_jobs and records what enrichment read
// from articles' web pages.
//
// Jobs exist only for wanted feeds (enrichWanted): a feed someone follows
// or one in Discover's catalog (#708). saveArticles queues a wanted feed's
// new arrivals; a feed becoming wanted gets its backlog queued
// (queueEnrichBacklog: a first follower, or entering the catalog), and one
// becoming unwanted has its pending jobs dropped (dropUnwantedEnrichJobs:
// its last follower leaving, or leaving the catalog), so no job is left
// that ClaimNext would never claim.
type EnrichRepository struct {
	DB *sql.DB
}

func NewEnrichRepository(db *sql.DB) *EnrichRepository {
	return &EnrichRepository{DB: db}
}

// enrichWanted is true for the feed whose ID is the SQL expression feed
// when its articles may have enrichment jobs: someone follows it or it's
// in Discover's catalog.
func enrichWanted(feed string) string {
	return `(EXISTS (SELECT 1 FROM subscriptions ws WHERE ws.feed_id = ` + feed + `)
		OR EXISTS (SELECT 1 FROM discover_feeds wd WHERE wd.feed_id = ` + feed + `))`
}

// enrichJobDue is true for a job j, of article a, that ClaimNext may
// claim: due, and the article's feed is wanted. The admin's Overview
// counts the enrichment queue with it (AdminOverviewRepository.Queues).
var enrichJobDue = `j.next_attempt_at <= now()
	AND ` + enrichWanted("a.feed_id")

// enrichFirstWanted is how many of a feed's articles, the latest by
// timeline position, get an enrichment job when the feed becomes wanted.
const enrichFirstWanted = 20

// Priorities of enrichment jobs; lower goes first.
const (
	enrichPriorityNew     = 0 // new arrivals of a wanted feed
	enrichPriorityBacklog = 1 // a feed's latest articles when it becomes wanted
)

// queueEnrichBacklog queues jobs for the enrichFirstWanted latest articles
// of feedID that need enrichment and haven't had it, at
// enrichPriorityBacklog, for a feed that has just become wanted. Its first
// conditions are needsEnrichment's, in SQL: keep the two in step; an
// article whose feed image a reader reported broken (image_failed_at, see
// ArticleRepository.ReportImageFailed) needs it too, since a report while
// the feed wasn't wanted queued nothing. The page_* ones skip articles
// enriched while the feed was wanted before. An article that already has a
// job keeps it.
func queueEnrichBacklog(ctx context.Context, q querier, feedID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO enrich_jobs (article_id, priority)
		SELECT id, $2 FROM articles
		WHERE feed_id = $1
		  AND url <> '' AND (image_url = '' OR title = '' OR excerpt = '' OR image_failed_at IS NOT NULL)
		  AND page_image_url = '' AND page_title = '' AND page_description = ''
		ORDER BY timeline_at DESC, id DESC
		LIMIT $3
		ON CONFLICT (article_id) DO NOTHING`, feedID, enrichPriorityBacklog, enrichFirstWanted)
	return err
}

// dropUnwantedEnrichJobs deletes the pending jobs of those of feedIDs that
// are no longer wanted, after a change that may have made them so.
func dropUnwantedEnrichJobs(ctx context.Context, q querier, feedIDs []string) error {
	if len(feedIDs) == 0 {
		return nil
	}
	_, err := q.ExecContext(ctx, `DELETE FROM enrich_jobs j USING articles a
		WHERE a.id = j.article_id AND a.feed_id = ANY($1::uuid[]) AND NOT `+enrichWanted("a.feed_id"),
		pq.Array(feedIDs))
	return err
}

// ClaimNext claims a due job (next_attempt_at <= now()) whose article's feed
// is wanted, lowest priority then earliest next_attempt_at first: it
// moves the job's next_attempt_at to now() + lease and counts the attempt.
// If the claimer records nothing, the job is due again when the lease runs
// out. rss.ErrNoRecord when no job is due.
func (r *EnrichRepository) ClaimNext(ctx context.Context, lease time.Duration) (rss.EnrichJob, error) {
	stmt := `WITH claimed AS (
			UPDATE enrich_jobs SET next_attempt_at = now() + make_interval(secs => $1), attempts = attempts + 1
			WHERE article_id = (
				SELECT j.article_id FROM enrich_jobs j
				JOIN articles a ON a.id = j.article_id
				WHERE ` + enrichJobDue + `
				ORDER BY j.priority, j.next_attempt_at
				FOR UPDATE OF j SKIP LOCKED
				LIMIT 1
			)
			RETURNING article_id, attempts
		)
		SELECT c.article_id, a.url, c.attempts
		FROM claimed c
		JOIN articles a ON a.id = c.article_id`

	var job rss.EnrichJob
	err := r.DB.QueryRowContext(ctx, stmt, lease.Seconds()).Scan(&job.ArticleID, &job.URL, &job.Attempts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.EnrichJob{}, rss.ErrNoRecord
		}
		return rss.EnrichJob{}, err
	}
	return job, nil
}

// SavePage stores p in article articleID's page_* columns and deletes its
// job, in one transaction. rss.ErrNoRecord if the article doesn't exist.
func (r *EnrichRepository) SavePage(ctx context.Context, articleID string, p rss.ArticlePage) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `UPDATE articles SET page_image_url = $2, page_title = $3, page_description = $4
		WHERE id = $1`
	if err := execOne(ctx, tx, stmt, articleID, p.ImageURL, p.Title, p.Description); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM enrich_jobs WHERE article_id = $1`, articleID); err != nil {
		return err
	}
	return tx.Commit()
}

// Retry records a failed attempt at article articleID's job: sets its
// last_error (msg truncated to maxErrorLen bytes) and next_attempt_at.
// rss.ErrNoRecord if the article has no job.
func (r *EnrichRepository) Retry(ctx context.Context, articleID, msg string, next time.Time) error {
	stmt := `UPDATE enrich_jobs SET last_error = $2, next_attempt_at = $3
		WHERE article_id = $1`
	return execOne(ctx, r.DB, stmt, articleID, textcut.Bytes(msg, maxErrorLen), next)
}

// GiveUp deletes article articleID's job, leaving its page_* columns empty.
// The caller logs msg. rss.ErrNoRecord if the article has no job.
func (r *EnrichRepository) GiveUp(ctx context.Context, articleID, msg string) error {
	return execOne(ctx, r.DB, `DELETE FROM enrich_jobs WHERE article_id = $1`, articleID)
}
