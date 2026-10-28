package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type StarRepository struct {
	DB *sql.DB
}

func NewStarRepository(db *sql.DB) *StarRepository {
	return &StarRepository{DB: db}
}

// Star records that userID starred articleID, unless the user already
// starred it or a copy of it (same non-empty canonical_url); starring twice
// is not an error. rss.ErrNoRecord if the article (or user) doesn't exist.
func (r *StarRepository) Star(ctx context.Context, userID, articleID string) error {
	// The final SELECT reports whether the article exists, since the INSERT
	// inserts nothing both for an unknown article and for one already starred.
	stmt := `WITH a AS (
		    SELECT id, canonical_url FROM articles WHERE id = $2
		), ins AS (
		    INSERT INTO stars (user_id, article_id)
		    SELECT $1, a.id
		    FROM a
		    WHERE NOT (a.canonical_url <> '' AND EXISTS (
		        SELECT 1
		        FROM articles b
		        JOIN stars s ON s.article_id = b.id
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

// Unstar removes userID's stars of articleID and of every copy of it. Not
// starred is not an error.
func (r *StarRepository) Unstar(ctx context.Context, userID, articleID string) error {
	stmt := `DELETE FROM stars
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

// StarredArticleIDs reports which of articleIDs userID has starred. An
// article counts as starred if the user starred it or any article with the
// same non-empty canonical_url (a copy of it in another feed); articles with
// an empty canonical_url only match themselves. An empty articleIDs returns
// an empty map without querying.
func (r *StarRepository) StarredArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	starred := map[string]bool{}
	if len(articleIDs) == 0 {
		return starred, nil
	}

	// Two EXISTS joined by OR, rather than one EXISTS with the OR inside: the
	// planner turns a lone EXISTS into a semi-join that scans all of the
	// user's stars, while these stay per-article lookups on stars_pkey and
	// articles_canonical_url_idx (which needs b.canonical_url <> '' spelled out).
	stmt := `SELECT a.id
		FROM articles a
		WHERE a.id = ANY($2)
		  AND (
		      EXISTS (SELECT 1 FROM stars s WHERE s.user_id = $1 AND s.article_id = a.id)
		      OR (a.canonical_url <> '' AND EXISTS (
		          SELECT 1
		          FROM articles b
		          JOIN stars s ON s.article_id = b.id
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
		starred[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return starred, nil
}

// ListStarred returns up to limit of userID's starred articles that pass
// filter (see timelineWhere), newest starred first (starred_at, then article
// ID), starting after before (from the start if before is zero; its At is a
// starred_at). Feed titles are the user's names for their subscriptions where
// they've set one. Summary and Content are left empty.
func (r *StarRepository) ListStarred(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.StarredArticle, error) {
	at, id := cursorArgs(before)
	args := []any{userID, at, id, limit}

	rows, err := r.DB.QueryContext(ctx, starredStmt(filter, false, &args), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []rss.StarredArticle{}
	for rows.Next() {
		var item rss.StarredArticle
		if item.Article, item.Feed, err = scanArticleWithFeed(rows, &item.StarredAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// starredStmt returns ListStarred's query for filter, appending filter's
// parameters to args as timelineStmt does; it selects starred_at after the
// feed's columns. shared is as for timelineStmt.
func starredStmt(filter rss.TimelineFilter, shared bool, args *[]any) string {
	// A walk of stars_user_starred_at_idx. Stars outlive subscriptions
	// (hence the LEFT JOIN): with Show = All my feeds they all show, while
	// chosen tags or feeds only match subscriptions. Starring already covers
	// every copy of an article, so there's nothing to collapse.
	return `SELECT ` + qualifiedListArticleColumns + `, ` + listFeedColumns(shared) + `, s.starred_at
		FROM stars s
		JOIN articles a ON a.id = s.article_id
		JOIN feeds f ON f.id = a.feed_id
		LEFT JOIN subscriptions sub ON sub.user_id = s.user_id AND sub.feed_id = a.feed_id
		WHERE s.user_id = $1
		  AND ($2::timestamptz IS NULL OR (s.starred_at, s.article_id) < ($2, $3::uuid))` + timelineWhere(filter, args, "sub", "a", "a.feed_id") + sharedWhere(shared, "a") + `
		ORDER BY s.starred_at DESC, s.article_id DESC
		LIMIT $4`
}
