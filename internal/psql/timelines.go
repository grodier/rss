package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// MaxTimelines is how many custom timelines a user can have.
const MaxTimelines = 20

type TimelineRepository struct {
	DB *sql.DB
}

func NewTimelineRepository(db *sql.DB) *TimelineRepository {
	return &TimelineRepository{DB: db}
}

// Create stores a custom timeline after userID's others (position = max + 1)
// with its sources, in one transaction, and returns its ID.
// rss.ErrTooManyTimelines at MaxTimelines; rss.ErrNoRecord if a tag or feed
// isn't the user's (23503).
func (r *TimelineRepository) Create(ctx context.Context, userID string, t rss.Timeline) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// Concurrent requests can overshoot MaxTimelines slightly; that's accepted.
	stmt := `INSERT INTO timelines (user_id, name, description, position, all_feeds, starred_only)
		SELECT $1, $2, $3, COALESCE(max(position), 0) + 1, $4, $6
		FROM timelines
		WHERE user_id = $1 AND NOT home
		HAVING count(*) < $5
		RETURNING id`

	var id string
	err = tx.QueryRowContext(ctx, stmt, userID, t.Name, t.Description, !t.Filter.OnlyIncluded, MaxTimelines, t.Filter.StarredOnly).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", rss.ErrTooManyTimelines
		}
		return "", err
	}

	if err := insertTimelineSources(ctx, tx, userID, id, t.Filter); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// Get returns userID's timeline id with its filter. rss.ErrNoRecord if it
// doesn't exist or is another user's.
func (r *TimelineRepository) Get(ctx context.Context, userID, id string) (rss.Timeline, error) {
	stmt := `SELECT id, home, name, description, position, all_feeds, starred_only, COALESCE(share_token, ''), created_at, updated_at
		FROM timelines
		WHERE id = $2 AND user_id = $1`

	var t rss.Timeline
	var allFeeds bool
	err := r.DB.QueryRowContext(ctx, stmt, userID, id).Scan(&t.ID, &t.Home, &t.Name, &t.Description, &t.Position, &allFeeds, &t.Filter.StarredOnly, &t.ShareToken, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Timeline{}, rss.ErrNoRecord
		}
		return rss.Timeline{}, err
	}
	t.Filter.OnlyIncluded = !allFeeds

	stmt = `SELECT tag_id, exclude FROM timeline_tags WHERE timeline_id = $1 ORDER BY tag_id`
	if err := scanTimelineSources(ctx, r.DB, stmt, id, &t.Filter.IncludeTagIDs, &t.Filter.ExcludeTagIDs); err != nil {
		return rss.Timeline{}, err
	}
	stmt = `SELECT feed_id, exclude FROM timeline_feeds WHERE timeline_id = $1 ORDER BY feed_id`
	if err := scanTimelineSources(ctx, r.DB, stmt, id, &t.Filter.IncludeFeedIDs, &t.Filter.ExcludeFeedIDs); err != nil {
		return rss.Timeline{}, err
	}
	return t, nil
}

// List returns userID's custom timelines (not Home) by position, then
// created_at, without their filters: what the tab row needs.
func (r *TimelineRepository) List(ctx context.Context, userID string) ([]rss.Timeline, error) {
	stmt := `SELECT id, name, description, position, COALESCE(share_token, ''), created_at, updated_at
		FROM timelines
		WHERE user_id = $1 AND NOT home
		ORDER BY position, created_at`

	rows, err := r.DB.QueryContext(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var timelines []rss.Timeline
	for rows.Next() {
		var t rss.Timeline
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Position, &t.ShareToken, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		timelines = append(timelines, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return timelines, nil
}

// Update replaces the name, description and filter of userID's custom
// timeline t.ID, in one transaction. rss.ErrNoRecord if it isn't one of the
// user's custom timelines, or a tag or feed isn't the user's.
func (r *TimelineRepository) Update(ctx context.Context, userID string, t rss.Timeline) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `UPDATE timelines
		SET name = $3, description = $4, all_feeds = $5, starred_only = $6, updated_at = now()
		WHERE id = $2 AND user_id = $1 AND NOT home`
	if err := execOne(ctx, tx, stmt, userID, t.ID, t.Name, t.Description, !t.Filter.OnlyIncluded, t.Filter.StarredOnly); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM timeline_tags WHERE timeline_id = $1`, t.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM timeline_feeds WHERE timeline_id = $1`, t.ID); err != nil {
		return err
	}
	if err := insertTimelineSources(ctx, tx, userID, t.ID, t.Filter); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete deletes userID's custom timeline id. rss.ErrNoRecord if it isn't
// one of the user's custom timelines.
func (r *TimelineRepository) Delete(ctx context.Context, userID, id string) error {
	stmt := `DELETE FROM timelines WHERE id = $2 AND user_id = $1 AND NOT home`

	return execOne(ctx, r.DB, stmt, userID, id)
}

// insertTimelineSources stores filter's tags and feeds as timelineID's
// timeline_tags and timeline_feeds rows. An ID both included and excluded
// is stored once, as excluded, since Leave out wins.
func insertTimelineSources(ctx context.Context, tx *sql.Tx, userID, timelineID string, filter rss.TimelineFilter) error {
	stmt := `INSERT INTO timeline_tags (timeline_id, user_id, tag_id, exclude)
		SELECT $1, $2, tag_id, exclude
		FROM unnest($3::uuid[], $4::boolean[]) AS s (tag_id, exclude)
		ON CONFLICT DO NOTHING`
	ids, exclude := timelineSources(filter.IncludeTagIDs, filter.ExcludeTagIDs)
	if _, err := tx.ExecContext(ctx, stmt, timelineID, userID, pq.Array(ids), pq.Array(exclude)); err != nil {
		return timelineError(err)
	}

	stmt = `INSERT INTO timeline_feeds (timeline_id, user_id, feed_id, exclude)
		SELECT $1, $2, feed_id, exclude
		FROM unnest($3::uuid[], $4::boolean[]) AS s (feed_id, exclude)
		ON CONFLICT DO NOTHING`
	ids, exclude = timelineSources(filter.IncludeFeedIDs, filter.ExcludeFeedIDs)
	if _, err := tx.ExecContext(ctx, stmt, timelineID, userID, pq.Array(ids), pq.Array(exclude)); err != nil {
		return timelineError(err)
	}
	return nil
}

// timelineSources returns the IDs to store and whether each is excluded:
// the excluded ones first, then the included ones that aren't excluded.
func timelineSources(include, exclude []string) ([]string, []bool) {
	ids := []string{}
	flags := []bool{}
	excluded := map[string]bool{}
	for _, id := range exclude {
		excluded[id] = true
		ids = append(ids, id)
		flags = append(flags, true)
	}
	for _, id := range include {
		if !excluded[id] {
			ids = append(ids, id)
			flags = append(flags, false)
		}
	}
	return ids, flags
}

// scanTimelineSources runs stmt (selecting an ID and its exclude flag for
// timelineID) and appends each ID to include or exclude.
func scanTimelineSources(ctx context.Context, db *sql.DB, stmt, timelineID string, include, exclude *[]string) error {
	rows, err := db.QueryContext(ctx, stmt, timelineID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var ex bool
		if err := rows.Scan(&id, &ex); err != nil {
			return err
		}
		if ex {
			*exclude = append(*exclude, id)
		} else {
			*include = append(*include, id)
		}
	}
	return rows.Err()
}

// timelineError maps a foreign key violation (a tag or feed that isn't the
// user's) to rss.ErrNoRecord.
func timelineError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return rss.ErrNoRecord
	}
	return err
}
