package psql

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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
	stmt := `INSERT INTO timelines (user_id, name, description, position, all_feeds, starred_only, max_posts_per_week, media_only,
			include_words, exclude_words)
		SELECT $1, $2, $3, COALESCE(max(position), 0) + 1, $4, $6, $7, $8, $9, $10
		FROM timelines
		WHERE user_id = $1
		HAVING count(*) < $5
		RETURNING id`

	var id string
	err = tx.QueryRowContext(ctx, stmt, userID, t.Name, t.Description, !t.Filter.OnlyIncluded, MaxTimelines,
		t.Filter.StarredOnly, t.Filter.MaxPostsPerWeek, t.Filter.MediaOnly,
		words(t.Filter.IncludeWords), words(t.Filter.ExcludeWords)).Scan(&id)
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
	stmt := `SELECT id, name, description, position, all_feeds, starred_only, max_posts_per_week, media_only,
			include_words, exclude_words, COALESCE(share_token, ''), created_at, updated_at
		FROM timelines
		WHERE id = $2 AND user_id = $1`

	var t rss.Timeline
	var allFeeds bool
	err := r.DB.QueryRowContext(ctx, stmt, userID, id).Scan(&t.ID, &t.Name, &t.Description, &t.Position, &allFeeds,
		&t.Filter.StarredOnly, &t.Filter.MaxPostsPerWeek, &t.Filter.MediaOnly,
		pq.Array(&t.Filter.IncludeWords), pq.Array(&t.Filter.ExcludeWords), &t.ShareToken, &t.CreatedAt, &t.UpdatedAt)
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

// List returns userID's custom timelines by position, then
// created_at, without their filters: what the tab row needs.
func (r *TimelineRepository) List(ctx context.Context, userID string) ([]rss.Timeline, error) {
	stmt := `SELECT id, name, description, position, COALESCE(share_token, ''), created_at, updated_at
		FROM timelines
		WHERE user_id = $1
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

// ListWithFilters returns userID's custom timelines in tab order, like
// List, each with its filter: what /timelines needs for its summaries. It
// reads the timelines, then their tags and their feeds, three queries in
// all rather than a Get per timeline. The sources are joined to the user's
// timelines rather than filtered on their own user_id, which
// timeline_tags has no index for.
func (r *TimelineRepository) ListWithFilters(ctx context.Context, userID string) ([]rss.Timeline, error) {
	stmt := `SELECT id, name, description, position, all_feeds, starred_only, max_posts_per_week, media_only,
			include_words, exclude_words, COALESCE(share_token, ''), created_at, updated_at
		FROM timelines
		WHERE user_id = $1
		ORDER BY position, created_at`

	rows, err := r.DB.QueryContext(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var timelines []rss.Timeline
	for rows.Next() {
		var t rss.Timeline
		var allFeeds bool
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Position, &allFeeds,
			&t.Filter.StarredOnly, &t.Filter.MaxPostsPerWeek, &t.Filter.MediaOnly,
			pq.Array(&t.Filter.IncludeWords), pq.Array(&t.Filter.ExcludeWords), &t.ShareToken, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Filter.OnlyIncluded = !allFeeds
		timelines = append(timelines, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(timelines) == 0 {
		return nil, nil
	}

	byID := make(map[string]*rss.TimelineFilter, len(timelines))
	for i := range timelines {
		byID[timelines[i].ID] = &timelines[i].Filter
	}
	stmt = `SELECT s.timeline_id, s.tag_id, s.exclude
		FROM timeline_tags s
		JOIN timelines t ON t.id = s.timeline_id
		WHERE t.user_id = $1
		ORDER BY s.timeline_id, s.tag_id`
	err = scanTimelinesSources(ctx, r.DB, stmt, userID, byID, func(f *rss.TimelineFilter, exclude bool) *[]string {
		if exclude {
			return &f.ExcludeTagIDs
		}
		return &f.IncludeTagIDs
	})
	if err != nil {
		return nil, err
	}
	stmt = `SELECT s.timeline_id, s.feed_id, s.exclude
		FROM timeline_feeds s
		JOIN timelines t ON t.id = s.timeline_id
		WHERE t.user_id = $1
		ORDER BY s.timeline_id, s.feed_id`
	err = scanTimelinesSources(ctx, r.DB, stmt, userID, byID, func(f *rss.TimelineFilter, exclude bool) *[]string {
		if exclude {
			return &f.ExcludeFeedIDs
		}
		return &f.IncludeFeedIDs
	})
	if err != nil {
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
		SET name = $3, description = $4, all_feeds = $5, starred_only = $6,
			max_posts_per_week = $7, media_only = $8, include_words = $9, exclude_words = $10,
			updated_at = now()
		WHERE id = $2 AND user_id = $1`
	if err := execOne(ctx, tx, stmt, userID, t.ID, t.Name, t.Description, !t.Filter.OnlyIncluded,
		t.Filter.StarredOnly, t.Filter.MaxPostsPerWeek, t.Filter.MediaOnly,
		words(t.Filter.IncludeWords), words(t.Filter.ExcludeWords)); err != nil {
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
	stmt := `DELETE FROM timelines WHERE id = $2 AND user_id = $1`

	return execOne(ctx, r.DB, stmt, userID, id)
}

// Move swaps userID's custom timeline id with its neighbour in tab order:
// the one before it if up, else the one after it. At either end it does
// nothing. rss.ErrNoRecord if the timeline isn't the user's custom one.
//
// It renumbers the user's timelines 0…n−1 each time, which also repairs ties
// and gaps left by deletes.
func (r *TimelineRepository) Move(ctx context.Context, userID, id string, up bool) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	stmt := `SELECT id FROM timelines
		WHERE user_id = $1
		ORDER BY position, created_at
		FOR UPDATE`
	rows, err := tx.QueryContext(ctx, stmt, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, tid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	i := slices.Index(ids, id)
	if i < 0 {
		return rss.ErrNoRecord
	}
	j := i + 1
	if up {
		j = i - 1
	}
	if j >= 0 && j < len(ids) {
		ids[i], ids[j] = ids[j], ids[i]
	}

	// Each ID's new position is its index in ids, counted from 0.
	stmt = `UPDATE timelines t
		SET position = s.position - 1
		FROM unnest($2::uuid[]) WITH ORDINALITY AS s (id, position)
		WHERE t.id = s.id AND t.user_id = $1 AND t.position <> s.position - 1`
	if _, err := tx.ExecContext(ctx, stmt, userID, pq.Array(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

// Share gives userID's custom timeline id the share token newToken if it
// has none, and returns its token. rss.ErrNoRecord if it isn't one of the
// user's custom timelines.
func (r *TimelineRepository) Share(ctx context.Context, userID, id, newToken string) (string, error) {
	stmt := `UPDATE timelines
		SET share_token = COALESCE(share_token, $3)
		WHERE id = $2 AND user_id = $1
		RETURNING share_token`

	var token string
	if err := r.DB.QueryRowContext(ctx, stmt, userID, id, newToken).Scan(&token); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", rss.ErrNoRecord
		}
		return "", err
	}
	return token, nil
}

// NewShareLink replaces the token of userID's shared timeline id with
// newToken. rss.ErrNoRecord if it isn't shared or isn't the user's.
func (r *TimelineRepository) NewShareLink(ctx context.Context, userID, id, newToken string) error {
	stmt := `UPDATE timelines
		SET share_token = $3
		WHERE id = $2 AND user_id = $1 AND share_token IS NOT NULL`

	return execOne(ctx, r.DB, stmt, userID, id, newToken)
}

// Unshare clears the share token of userID's custom timeline id. Not shared
// is not an error; rss.ErrNoRecord if it isn't one of the user's custom
// timelines.
func (r *TimelineRepository) Unshare(ctx context.Context, userID, id string) error {
	stmt := `UPDATE timelines SET share_token = NULL WHERE id = $2 AND user_id = $1`

	return execOne(ctx, r.DB, stmt, userID, id)
}

// GetShared returns the shared timeline with token, with its filter, and its
// owner's ID. rss.ErrNoRecord if no timeline has it.
func (r *TimelineRepository) GetShared(ctx context.Context, token string) (t rss.Timeline, ownerID string, err error) {
	stmt := `SELECT id, user_id FROM timelines WHERE share_token = $1`

	var id string
	if err := r.DB.QueryRowContext(ctx, stmt, token).Scan(&id, &ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Timeline{}, "", rss.ErrNoRecord
		}
		return rss.Timeline{}, "", err
	}
	// The timeline can be unshared or deleted in between: Get's row then
	// lacks the token, or is gone.
	t, err = r.Get(ctx, ownerID, id)
	if err != nil {
		return rss.Timeline{}, "", err
	}
	if t.ShareToken != token {
		return rss.Timeline{}, "", rss.ErrNoRecord
	}
	return t, ownerID, nil
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

// scanTimelinesSources runs stmt (selecting a timeline ID, a source ID and
// its exclude flag for userID's timelines) and appends each source ID to
// the list field picks from its timeline's filter in byID. Rows for a
// timeline not in byID (made after the timelines were read) are skipped.
func scanTimelinesSources(ctx context.Context, db *sql.DB, stmt, userID string, byID map[string]*rss.TimelineFilter,
	field func(f *rss.TimelineFilter, exclude bool) *[]string) error {
	rows, err := db.QueryContext(ctx, stmt, userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var timelineID, id string
		var exclude bool
		if err := rows.Scan(&timelineID, &id, &exclude); err != nil {
			return err
		}
		if f, ok := byID[timelineID]; ok {
			list := field(f, exclude)
			*list = append(*list, id)
		}
	}
	return rows.Err()
}

// words returns s as a TEXT[] parameter: '{}' rather than NULL when it's
// empty, for the NOT NULL include_words and exclude_words.
func words(s []string) any {
	if s == nil {
		s = []string{}
	}
	return pq.Array(s)
}

// wordsPattern returns a Postgres regular expression (for ~*) matching any
// of words where the characters just before and after it aren't letters or
// digits: (?<![[:alnum:]])(?:…|…)(?![[:alnum:]]). In each word, every
// ASCII character that isn't a letter, a digit or whitespace is escaped with
// a backslash, so it matches literally, and each run of whitespace becomes
// \s+. "" for no words.
//
// A backslash before a character that isn't a letter or digit is always a
// literal in Postgres regular expressions. Only ASCII characters are
// escaped: none outside ASCII has a meaning in a regular expression, and a
// backslash before one the database's locale counts as alphanumeric (such
// as ²) would be an invalid escape. [[:alnum:]] follows the database's
// LC_CTYPE, so in a UTF-8 database accented and non-Latin letters count as
// letters.
func wordsPattern(words []string) string {
	if len(words) == 0 {
		return ""
	}
	alts := make([]string, len(words))
	for i, word := range words {
		var b strings.Builder
		for j, field := range strings.Fields(word) {
			if j > 0 {
				b.WriteString(`\s+`)
			}
			for _, r := range field {
				if r < utf8.RuneSelf && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					b.WriteByte('\\')
				}
				b.WriteRune(r)
			}
		}
		alts[i] = b.String()
	}
	return `(?<![[:alnum:]])(?:` + strings.Join(alts, "|") + `)(?![[:alnum:]])`
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
