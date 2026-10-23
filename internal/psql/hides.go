package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

type HideRepository struct {
	DB *sql.DB
}

func NewHideRepository(db *sql.DB) *HideRepository {
	return &HideRepository{DB: db}
}

// Hide records that userID hid articleID from their timeline, unless the
// user already hid it or a copy of it (same non-empty canonical_url); hiding
// twice is not an error. rss.ErrNoRecord if the article (or user) doesn't
// exist.
func (r *HideRepository) Hide(ctx context.Context, userID, articleID string) error {
	// The final SELECT reports whether the article exists, since the INSERT
	// inserts nothing both for an unknown article and for one already hidden.
	stmt := `WITH a AS (
		    SELECT id, canonical_url FROM articles WHERE id = $2
		), ins AS (
		    INSERT INTO hides (user_id, article_id)
		    SELECT $1, a.id
		    FROM a
		    WHERE NOT (a.canonical_url <> '' AND EXISTS (
		        SELECT 1
		        FROM articles b
		        JOIN hides h ON h.article_id = b.id
		        WHERE h.user_id = $1
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

// Unhide removes userID's hides of articleID and of every copy of it. Not
// hidden is not an error.
func (r *HideRepository) Unhide(ctx context.Context, userID, articleID string) error {
	stmt := `DELETE FROM hides
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

// HiddenArticleIDs reports which of articleIDs userID has hidden. An article
// counts as hidden if the user hid it or any article with the same non-empty
// canonical_url (a copy of it in another feed); articles with an empty
// canonical_url only match themselves. An empty articleIDs returns an empty
// map without querying.
func (r *HideRepository) HiddenArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	hidden := map[string]bool{}
	if len(articleIDs) == 0 {
		return hidden, nil
	}

	// Two EXISTS joined by OR, as in SavedArticleIDs, so each stays a
	// per-article lookup on hides_pkey and articles_canonical_url_idx.
	stmt := `SELECT a.id
		FROM articles a
		WHERE a.id = ANY($2)
		  AND (
		      EXISTS (SELECT 1 FROM hides h WHERE h.user_id = $1 AND h.article_id = a.id)
		      OR (a.canonical_url <> '' AND EXISTS (
		          SELECT 1
		          FROM articles b
		          JOIN hides h ON h.article_id = b.id
		          WHERE h.user_id = $1
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
		hidden[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return hidden, nil
}
