package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type SaveRepository struct {
	DB *sql.DB
}

func NewSaveRepository(db *sql.DB) *SaveRepository {
	return &SaveRepository{DB: db}
}

// Save records that userID saved articleID, unless the user already saved
// it or a copy of it (same non-empty canonical_url); saving twice is not an
// error. rss.ErrNoRecord if the article (or user) doesn't exist.
func (r *SaveRepository) Save(ctx context.Context, userID, articleID string) error {
	// The final SELECT reports whether the article exists, since the INSERT
	// inserts nothing both for an unknown article and for one already saved.
	stmt := `WITH a AS (
		    SELECT id, canonical_url FROM articles WHERE id = $2
		), ins AS (
		    INSERT INTO saves (user_id, article_id)
		    SELECT $1, a.id
		    FROM a
		    WHERE NOT (a.canonical_url <> '' AND EXISTS (
		        SELECT 1
		        FROM articles b
		        JOIN saves s ON s.article_id = b.id
		        WHERE s.user_id = $1
		          AND b.canonical_url = a.canonical_url
		          AND b.canonical_url <> ''
		    ))
		    ON CONFLICT DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM a)`

	var exists bool
	if err := r.DB.QueryRowContext(ctx, stmt, userID, articleID).Scan(&exists); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return rss.ErrNoRecord
		}
		return err
	}
	if !exists {
		return rss.ErrNoRecord
	}
	return nil
}

// Unsave removes userID's saves of articleID and of every copy of it. Not
// saved is not an error.
func (r *SaveRepository) Unsave(ctx context.Context, userID, articleID string) error {
	stmt := `DELETE FROM saves
		WHERE user_id = $1
		  AND article_id IN (
		      SELECT $2::uuid
		      UNION
		      SELECT b.id
		      FROM articles a
		      JOIN articles b ON b.canonical_url = a.canonical_url
		      WHERE a.id = $2
		        AND a.canonical_url <> ''
		        AND b.canonical_url <> ''
		  )`

	_, err := r.DB.ExecContext(ctx, stmt, userID, articleID)
	return err
}

// SavedArticleIDs reports which of articleIDs userID has saved. An article
// counts as saved if the user saved it or any article with the same non-empty
// canonical_url (a copy of it in another feed); articles with an empty
// canonical_url only match themselves. An empty articleIDs returns an empty
// map without querying.
func (r *SaveRepository) SavedArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	saved := map[string]bool{}
	if len(articleIDs) == 0 {
		return saved, nil
	}

	// Two EXISTS joined by OR, rather than one EXISTS with the OR inside: the
	// planner turns a lone EXISTS into a semi-join that scans all of the
	// user's saves, while these stay per-article lookups on saves_pkey and
	// articles_canonical_url_idx (which needs b.canonical_url <> '' spelled out).
	stmt := `SELECT a.id
		FROM articles a
		WHERE a.id = ANY($2)
		  AND (
		      EXISTS (SELECT 1 FROM saves s WHERE s.user_id = $1 AND s.article_id = a.id)
		      OR (a.canonical_url <> '' AND EXISTS (
		          SELECT 1
		          FROM articles b
		          JOIN saves s ON s.article_id = b.id
		          WHERE s.user_id = $1
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
		saved[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return saved, nil
}

// ListSaved returns up to limit of userID's saved articles whose title,
// page title, excerpt, feed title or the user's name for the feed contains q
// (ignoring case; every saved article if q is ""), newest saved first
// (saved_at, then article ID), starting after before (from the start if
// before is zero; its At is a saved_at). Feed titles are the user's names
// for their subscriptions where they've set one. Summary and Content are left empty.
func (r *SaveRepository) ListSaved(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
	// A scan of the user's saves, filtered with ILIKE: a user's saves are a
	// small set (see the Performance watchlist in docs/todo.md). Saved
	// articles can come from feeds the user no longer subscribes to (hence
	// the LEFT JOIN), which keep their own title.
	stmt := `SELECT ` + qualifiedListArticleColumns + `, ` + qualifiedFeedColumnsTitled(subscriptionTitle) + `, s.saved_at
		FROM saves s
		JOIN articles a ON a.id = s.article_id
		JOIN feeds f ON f.id = a.feed_id
		LEFT JOIN subscriptions sub ON sub.user_id = s.user_id AND sub.feed_id = a.feed_id
		WHERE s.user_id = $1
		  AND ($2::timestamptz IS NULL OR (s.saved_at, s.article_id) < ($2, $3::uuid))
		  AND ($5 = '' OR a.title ILIKE $6 ESCAPE '\' OR a.page_title ILIKE $6 ESCAPE '\'
		       OR a.excerpt ILIKE $6 ESCAPE '\' OR f.title ILIKE $6 ESCAPE '\'
		       OR sub.title ILIKE $6 ESCAPE '\')
		ORDER BY s.saved_at DESC, s.article_id DESC
		LIMIT $4`

	at, id := cursorArgs(before)

	rows, err := r.DB.QueryContext(ctx, stmt, userID, at, id, limit, q, likePattern(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []rss.SavedArticle{}
	for rows.Next() {
		var item rss.SavedArticle
		// Nest scanArticle and scanFeed as ListTimeline does, with saved_at
		// after the feed's columns.
		article, err := scanArticle(scanFunc(func(articleDests ...any) error {
			var feedErr error
			item.Feed, feedErr = scanFeed(scanFunc(func(feedDests ...any) error {
				dests := append(articleDests, feedDests...)
				return rows.Scan(append(dests, &item.SavedAt)...)
			}))
			return feedErr
		}))
		if err != nil {
			return nil, err
		}
		item.Article = article
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
