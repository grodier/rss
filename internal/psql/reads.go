package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type ReadRepository struct {
	DB *sql.DB
}

func NewReadRepository(db *sql.DB) *ReadRepository {
	return &ReadRepository{DB: db}
}

// MarkRead records that userID read articleID. Marking twice is not an
// error and keeps the first read_at. rss.ErrNoRecord if the article (or
// user) doesn't exist.
func (r *ReadRepository) MarkRead(ctx context.Context, userID, articleID string) error {
	stmt := `INSERT INTO reads (user_id, article_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`

	if _, err := r.DB.ExecContext(ctx, stmt, userID, articleID); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return rss.ErrNoRecord
		}
		return err
	}
	return nil
}

// ReadArticleIDs reports which of articleIDs userID has read. An article
// counts as read if the user read it or any article with the same non-empty
// canonical_url (a copy of it in another feed); articles with an empty
// canonical_url only match themselves. An empty articleIDs returns an empty
// map without querying.
func (r *ReadRepository) ReadArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	read := map[string]bool{}
	if len(articleIDs) == 0 {
		return read, nil
	}

	// Two EXISTS joined by OR, rather than one EXISTS with the OR inside: the
	// planner turns a lone EXISTS into a semi-join that scans all of the
	// user's reads, while these stay per-article lookups on reads_pkey and
	// articles_canonical_url_idx (which needs b.canonical_url <> '' spelled out).
	stmt := `SELECT a.id
		FROM articles a
		WHERE a.id = ANY($2)
		  AND (
		      EXISTS (SELECT 1 FROM reads r WHERE r.user_id = $1 AND r.article_id = a.id)
		      OR (a.canonical_url <> '' AND EXISTS (
		          SELECT 1
		          FROM articles b
		          JOIN reads r ON r.article_id = b.id
		          WHERE r.user_id = $1
		            AND b.canonical_url = a.canonical_url
		            AND b.canonical_url <> ''
		      ))
		  )`

	rows, err := r.DB.QueryContext(ctx, stmt, userID, pq.Array(articleIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		read[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return read, nil
}

// TimelineMark returns the user's mark, or a zero TimelineMark if the
// timeline has never reported one.
func (r *ReadRepository) TimelineMark(ctx context.Context, userID string) (rss.TimelineMark, error) {
	stmt := `SELECT seen_up_to, seen_at, divider_at FROM timeline_marks WHERE user_id = $1`

	var m rss.TimelineMark
	var dividerAt sql.NullTime
	err := r.DB.QueryRowContext(ctx, stmt, userID).Scan(&m.SeenUpTo, &m.SeenAt, &dividerAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.TimelineMark{}, nil // the first visit hasn't reported yet
	}
	if err != nil {
		return rss.TimelineMark{}, err
	}
	m.DividerAt = dividerAt.Time
	return m, nil
}

// MarkSeen records that the user has had the timeline position upTo on
// screen. A report after rss.VisitGap without one starts a new visit: the
// visit's divider becomes where the previous visit ended, and the reader's
// own positions (timeline_entries) are trimmed to every row above the
// previous visit's divider, but at least rss.MinEntries and at most
// rss.MaxEntries, the newest by position (the newest rss.MaxEntries when
// there's no divider yet). A deleted row's article goes back to its shared
// position, which differs only if the row was raised, and is never higher.
func (r *ReadRepository) MarkSeen(ctx context.Context, userID string, upTo time.Time) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// The error after a successful Commit is expected and ignored.
	defer tx.Rollback()

	// The previous visit's divider, and whether this report starts a new
	// visit, judged by the same now() as the upsert below (the transaction's
	// start). FOR UPDATE keeps a concurrent report from starting the same
	// visit again.
	var newVisit bool
	var dividerAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT seen_at <= now() - make_interval(secs => $2), divider_at
		FROM timeline_marks WHERE user_id = $1 FOR UPDATE`, userID, rss.VisitGap.Seconds()).Scan(&newVisit, &dividerAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) { // no row: the first report, nothing to trim
		return err
	}

	// A client-supplied position is never stored later than now, so a bad
	// clock or a forged report can't hide future articles above the line,
	// or raise the positions of what arrives next past them.
	stmt := `INSERT INTO timeline_marks (user_id, seen_up_to, seen_at)
		VALUES ($1, LEAST($2, now()), now())
		ON CONFLICT (user_id) DO UPDATE SET
		    divider_at = CASE WHEN timeline_marks.seen_at <= now() - make_interval(secs => $3)
		                      THEN timeline_marks.seen_up_to
		                      ELSE timeline_marks.divider_at END,
		    seen_up_to = GREATEST(timeline_marks.seen_up_to, EXCLUDED.seen_up_to),
		    seen_at = now()`

	if _, err := tx.ExecContext(ctx, stmt, userID, upTo, rss.VisitGap.Seconds()); err != nil {
		return err
	}

	if newVisit {
		trim := `DELETE FROM timeline_entries e
			USING (
			    SELECT article_id, timeline_at,
			        row_number() OVER (ORDER BY timeline_at DESC, article_id DESC) AS rank
			    FROM timeline_entries
			    WHERE user_id = $1
			) r
			WHERE e.user_id = $1 AND e.article_id = r.article_id
			    AND (r.rank > $2 OR (r.rank > $3 AND $4::timestamptz IS NOT NULL AND r.timeline_at <= $4))`

		if _, err := tx.ExecContext(ctx, trim, userID, rss.MaxEntries, rss.MinEntries, dividerAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}
