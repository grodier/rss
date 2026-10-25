package psql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

const importFeedColumns = `f.import_id, i.user_id, f.position, f.url, f.title, f.tags, f.status, f.failure, f.feed_id, f.error, f.attempts`

// ImportRepository stores OPML imports and their feeds, which are also the
// import queue.
type ImportRepository struct {
	DB *sql.DB
}

func NewImportRepository(db *sql.DB) *ImportRepository {
	return &ImportRepository{DB: db}
}

// Create inserts an import for userID and its feeds as pending, numbered
// from 0 in order (their Position is ignored), in one transaction, and
// returns the import ID.
func (r *ImportRepository) Create(ctx context.Context, userID, filename string, feeds []rss.ImportFeed) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO imports (user_id, filename) VALUES ($1, $2) RETURNING id`,
		userID, filename).Scan(&id)
	if err != nil {
		return "", err
	}

	urls := make([]string, len(feeds))
	titles := make([]string, len(feeds))
	// unnest can't take an array of arrays of different lengths, so each
	// feed's tags go in as a JSON array.
	tags := make([]string, len(feeds))
	for i, f := range feeds {
		urls[i], titles[i] = f.URL, f.Title
		tags[i] = "[]"
		if len(f.Tags) > 0 {
			b, err := json.Marshal(f.Tags)
			if err != nil {
				return "", err
			}
			tags[i] = string(b)
		}
	}
	stmt := `INSERT INTO import_feeds (import_id, position, url, title, tags)
		SELECT $1, ord - 1, url, title, ARRAY(SELECT jsonb_array_elements_text(tags::jsonb))
		FROM unnest($2::text[], $3::text[], $4::text[]) WITH ORDINALITY AS t(url, title, tags, ord)`
	if _, err := tx.ExecContext(ctx, stmt, id, pq.Array(urls), pq.Array(titles), pq.Array(tags)); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// Get returns the import id and its feeds by position, with their feeds'
// titles, or rss.ErrNoRecord.
func (r *ImportRepository) Get(ctx context.Context, id string) (rss.Import, []rss.ImportFeed, error) {
	var imp rss.Import
	err := r.DB.QueryRowContext(ctx, `SELECT id, user_id, filename, created_at FROM imports WHERE id = $1`, id).
		Scan(&imp.ID, &imp.UserID, &imp.Filename, &imp.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Import{}, nil, rss.ErrNoRecord
		}
		return rss.Import{}, nil, err
	}

	stmt := `SELECT ` + importFeedColumns + `, COALESCE(fd.title, '')
		FROM import_feeds f
		JOIN imports i ON i.id = f.import_id
		LEFT JOIN feeds fd ON fd.id = f.feed_id
		WHERE f.import_id = $1
		ORDER BY f.position`
	rows, err := r.DB.QueryContext(ctx, stmt, id)
	if err != nil {
		return rss.Import{}, nil, err
	}
	defer rows.Close()

	feeds := []rss.ImportFeed{}
	for rows.Next() {
		var feedTitle string
		f, err := scanImportFeed(rows, &feedTitle)
		if err != nil {
			return rss.Import{}, nil, err
		}
		f.FeedTitle = feedTitle
		feeds = append(feeds, f)
	}
	if err := rows.Err(); err != nil {
		return rss.Import{}, nil, err
	}
	return imp, feeds, nil
}

// ListByUser returns userID's newest limit imports, newest first, with their
// feeds counted by status.
func (r *ImportRepository) ListByUser(ctx context.Context, userID string, limit int) ([]rss.ImportSummary, error) {
	stmt := `SELECT i.id, i.user_id, i.filename, i.created_at,
			count(f.position),
			count(f.position) FILTER (WHERE f.status IN ('pending', 'running')),
			count(f.position) FILTER (WHERE f.status = 'failed')
		FROM (
			SELECT * FROM imports WHERE user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		) i
		LEFT JOIN import_feeds f ON f.import_id = i.id
		GROUP BY i.id, i.user_id, i.filename, i.created_at
		ORDER BY i.created_at DESC, i.id DESC`
	rows, err := r.DB.QueryContext(ctx, stmt, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	imports := []rss.ImportSummary{}
	for rows.Next() {
		var s rss.ImportSummary
		err := rows.Scan(&s.Import.ID, &s.Import.UserID, &s.Import.Filename, &s.Import.CreatedAt, &s.Total, &s.Pending, &s.Failed)
		if err != nil {
			return nil, err
		}
		imports = append(imports, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return imports, nil
}

// ClaimNext marks the next pending feed running and returns it with its
// import's user: the oldest import's first, lowest position first.
// rss.ErrNoRecord when the queue is empty.
func (r *ImportRepository) ClaimNext(ctx context.Context) (rss.ImportFeed, error) {
	stmt := `UPDATE import_feeds f SET status = 'running', started_at = now(), attempts = f.attempts + 1
		FROM imports i
		WHERE i.id = f.import_id AND (f.import_id, f.position) = (
			SELECT p.import_id, p.position
			FROM import_feeds p
			JOIN imports pi ON pi.id = p.import_id
			WHERE p.status = 'pending'
			ORDER BY pi.created_at, p.import_id, p.position
			FOR UPDATE OF p SKIP LOCKED
			LIMIT 1
		)
		RETURNING ` + importFeedColumns

	f, err := scanImportFeed(r.DB.QueryRowContext(ctx, stmt))
	if errors.Is(err, sql.ErrNoRows) {
		return rss.ImportFeed{}, rss.ErrNoRecord
	}
	return f, err
}

// Finish records a feed's outcome: its status, failure, feed ID ("" for
// none) and internal error, truncated to maxErrorLen bytes.
// rss.ErrNoRecord if the feed doesn't exist.
func (r *ImportRepository) Finish(ctx context.Context, f rss.ImportFeed) error {
	stmt := `UPDATE import_feeds SET status = $3, failure = $4, feed_id = NULLIF($5, '')::uuid,
			error = $6, finished_at = now()
		WHERE import_id = $1 AND position = $2`

	return execOne(ctx, r.DB, stmt, f.ImportID, f.Position, f.Status, f.Failure, f.FeedID, truncateUTF8(f.Error, maxErrorLen))
}

// ResetStale puts running feeds started more than olderThan ago back to
// pending (worker crashed), or marks them failed as unreachable once
// attempts >= maxAttempts. Returns how many rows changed.
func (r *ImportRepository) ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error) {
	stmt := `UPDATE import_feeds SET
			status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
			failure = CASE WHEN attempts >= $2 THEN 'unreachable' ELSE failure END,
			error = CASE WHEN attempts >= $2 THEN 'stale: gave up after too many attempts' ELSE error END,
			finished_at = CASE WHEN attempts >= $2 THEN now() ELSE NULL END,
			started_at = CASE WHEN attempts >= $2 THEN started_at ELSE NULL END
		WHERE status = 'running' AND started_at < now() - make_interval(secs => $1)`

	res, err := r.DB.ExecContext(ctx, stmt, olderThan.Seconds(), maxAttempts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// scanImportFeed scans a row selected with importFeedColumns, then any
// columns after them into extra.
func scanImportFeed(row interface{ Scan(...any) error }, extra ...any) (rss.ImportFeed, error) {
	var f rss.ImportFeed
	var feedID sql.NullString
	var tags pq.StringArray
	dest := append([]any{&f.ImportID, &f.UserID, &f.Position, &f.URL, &f.Title, &tags, &f.Status, &f.Failure, &feedID, &f.Error, &f.Attempts}, extra...)
	err := row.Scan(dest...)
	if err != nil {
		return rss.ImportFeed{}, err
	}
	f.FeedID = feedID.String
	if len(tags) > 0 {
		f.Tags = tags
	}
	return f, nil
}
