package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// MaxTags is how many tags a user can have.
const MaxTags = 100

type TagRepository struct {
	DB *sql.DB
}

func NewTagRepository(db *sql.DB) *TagRepository {
	return &TagRepository{DB: db}
}

// Create makes a tag called name (already normalized) for userID.
// rss.ErrDuplicateTag if the user has one by that name (ignoring case);
// rss.ErrTooManyTags if they have MaxTags already.
func (r *TagRepository) Create(ctx context.Context, userID, name string) (rss.Tag, error) {
	// Concurrent requests can overshoot MaxTags slightly; that's accepted.
	stmt := `INSERT INTO tags (user_id, name)
		SELECT $1, $2
		WHERE (SELECT count(*) FROM tags WHERE user_id = $1) < $3
		RETURNING id, name`

	var tag rss.Tag
	err := r.DB.QueryRowContext(ctx, stmt, userID, name, MaxTags).Scan(&tag.ID, &tag.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Tag{}, rss.ErrTooManyTags
		}
		return rss.Tag{}, tagError(err)
	}
	return tag, nil
}

// List returns userID's tags ordered by name, ignoring case, each with
// its number of subscriptions.
func (r *TagRepository) List(ctx context.Context, userID string) ([]rss.Tag, error) {
	// name is citext, so ordering by it ignores case.
	stmt := `SELECT t.id, t.name, count(st.feed_id)
		FROM tags t
		LEFT JOIN subscription_tags st ON st.tag_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id
		ORDER BY t.name`

	rows, err := r.DB.QueryContext(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []rss.Tag
	for rows.Next() {
		var tag rss.Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Feeds); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tags, nil
}

// Get returns userID's tag id. rss.ErrNoRecord if it doesn't exist or
// is another user's.
func (r *TagRepository) Get(ctx context.Context, userID, id string) (rss.Tag, error) {
	stmt := `SELECT id, name FROM tags WHERE id = $2 AND user_id = $1`

	var tag rss.Tag
	err := r.DB.QueryRowContext(ctx, stmt, userID, id).Scan(&tag.ID, &tag.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.Tag{}, rss.ErrNoRecord
	}
	return tag, err
}

// Rename renames userID's tag id. rss.ErrNoRecord as Get;
// rss.ErrDuplicateTag if another of the user's tags has the name.
func (r *TagRepository) Rename(ctx context.Context, userID, id, name string) error {
	stmt := `UPDATE tags SET name = $3 WHERE id = $2 AND user_id = $1`

	return tagError(execOne(ctx, r.DB, stmt, userID, id, name))
}

// Delete deletes userID's tag id, removing it from subscriptions and
// timelines. rss.ErrNoRecord as Get.
func (r *TagRepository) Delete(ctx context.Context, userID, id string) error {
	stmt := `DELETE FROM tags WHERE id = $2 AND user_id = $1`

	return execOne(ctx, r.DB, stmt, userID, id)
}

// FeedIDs returns the feeds of userID's subscriptions that have tag id.
func (r *TagRepository) FeedIDs(ctx context.Context, userID, id string) (map[string]bool, error) {
	stmt := `SELECT feed_id FROM subscription_tags WHERE user_id = $1 AND tag_id = $2`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feedIDs := map[string]bool{}
	for rows.Next() {
		var feedID string
		if err := rows.Scan(&feedID); err != nil {
			return nil, err
		}
		feedIDs[feedID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return feedIDs, nil
}

// SetFeeds makes feedIDs exactly the subscriptions of userID that have
// tag id: adds it to those without it and removes it from the rest, in
// one transaction. rss.ErrNoRecord if the tag isn't the user's or the user
// doesn't subscribe to one of feedIDs.
func (r *TagRepository) SetFeeds(ctx context.Context, userID, id string, feedIDs []string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// With no feedIDs nothing is inserted, so the foreign key alone wouldn't
	// catch a tag that isn't the user's.
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE id = $2 AND user_id = $1)`,
		userID, id).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return rss.ErrNoRecord
	}

	// A nil slice would be NULL, and NOT (feed_id = ANY(NULL)) deletes nothing.
	if feedIDs == nil {
		feedIDs = []string{}
	}
	ids := pq.Array(feedIDs)
	stmt := `DELETE FROM subscription_tags
		WHERE user_id = $1 AND tag_id = $2 AND NOT (feed_id = ANY($3::uuid[]))`
	if _, err := tx.ExecContext(ctx, stmt, userID, id, ids); err != nil {
		return err
	}

	stmt = `INSERT INTO subscription_tags (user_id, feed_id, tag_id)
		SELECT $1, feed_id, $2
		FROM unnest($3::uuid[]) AS feed_id
		ON CONFLICT DO NOTHING`
	if _, err := tx.ExecContext(ctx, stmt, userID, id, ids); err != nil {
		return tagError(err)
	}

	return tx.Commit()
}

// SetForSubscription replaces the tags on userID's subscription to feedID
// with tagIDs plus the tags named newNames (normalized), creating those
// the user doesn't have, all in one transaction. rss.ErrNoRecord if the
// user doesn't subscribe to feedID or a tag ID isn't theirs;
// rss.ErrTooManyTags if creating the new ones would pass MaxTags.
func (r *TagRepository) SetForSubscription(ctx context.Context, userID, feedID string, tagIDs, newNames []string) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// With no tags nothing is inserted, so the foreign key alone wouldn't
	// catch a feed the user doesn't subscribe to.
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions WHERE user_id = $1 AND feed_id = $2)`,
		userID, feedID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return rss.ErrNoRecord
	}

	ids := append([]string{}, tagIDs...)
	if len(newNames) > 0 {
		// name is citext, so DISTINCT and the comparison ignore case.
		// Concurrent requests can overshoot MaxTags slightly; that's
		// accepted, as in Create.
		names := pq.Array(newNames)
		var total int
		stmt := `SELECT (SELECT count(*) FROM tags WHERE user_id = $1)
			+ (SELECT count(DISTINCT n::citext) FROM unnest($2::text[]) AS n
				WHERE NOT EXISTS (SELECT 1 FROM tags WHERE user_id = $1 AND name = n::citext))`
		if err := tx.QueryRowContext(ctx, stmt, userID, names).Scan(&total); err != nil {
			return err
		}
		if total > MaxTags {
			return rss.ErrTooManyTags
		}

		// DO UPDATE (a no-op) rather than DO NOTHING, so RETURNING also
		// gives the IDs of the tags that already exist.
		stmt = `INSERT INTO tags (user_id, name)
			SELECT DISTINCT $1::uuid, n::citext FROM unnest($2::text[]) AS n
			ON CONFLICT (user_id, name) DO UPDATE SET name = tags.name
			RETURNING id`
		rows, err := tx.QueryContext(ctx, stmt, userID, names)
		if err != nil {
			return tagError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	stmt := `DELETE FROM subscription_tags WHERE user_id = $1 AND feed_id = $2`
	if _, err := tx.ExecContext(ctx, stmt, userID, feedID); err != nil {
		return err
	}

	stmt = `INSERT INTO subscription_tags (user_id, feed_id, tag_id)
		SELECT $1, $2, tag_id
		FROM unnest($3::uuid[]) AS tag_id
		ON CONFLICT DO NOTHING`
	if _, err := tx.ExecContext(ctx, stmt, userID, feedID, pq.Array(ids)); err != nil {
		return tagError(err)
	}

	return tx.Commit()
}

// tagError maps a unique violation to rss.ErrDuplicateTag and a foreign key
// violation to rss.ErrNoRecord.
func tagError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return rss.ErrDuplicateTag
		case "23503":
			return rss.ErrNoRecord
		}
	}
	return err
}
