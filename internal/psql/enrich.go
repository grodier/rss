package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// EnrichRepository hands out enrich_jobs and records what enrichment read
// from articles' web pages. Jobs are queued by saveArticles.
type EnrichRepository struct {
	DB *sql.DB
}

func NewEnrichRepository(db *sql.DB) *EnrichRepository {
	return &EnrichRepository{DB: db}
}

// ClaimNext claims a due job (next_attempt_at <= now()) whose article's feed
// has a subscriber, lowest priority then earliest next_attempt_at first: it
// moves the job's next_attempt_at to now() + lease and counts the attempt.
// If the claimer records nothing, the job is due again when the lease runs
// out. rss.ErrNoRecord when no job is due.
func (r *EnrichRepository) ClaimNext(ctx context.Context, lease time.Duration) (rss.EnrichJob, error) {
	stmt := `WITH claimed AS (
			UPDATE enrich_jobs SET next_attempt_at = now() + make_interval(secs => $1), attempts = attempts + 1
			WHERE article_id = (
				SELECT j.article_id FROM enrich_jobs j
				JOIN articles a ON a.id = j.article_id
				WHERE j.next_attempt_at <= now()
				  AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = a.feed_id)
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
	return execOne(ctx, r.DB, stmt, articleID, truncateUTF8(msg, maxErrorLen), next)
}

// GiveUp deletes article articleID's job, leaving its page_* columns empty.
// The caller logs msg. rss.ErrNoRecord if the article has no job.
func (r *EnrichRepository) GiveUp(ctx context.Context, articleID, msg string) error {
	return execOne(ctx, r.DB, `DELETE FROM enrich_jobs WHERE article_id = $1`, articleID)
}
