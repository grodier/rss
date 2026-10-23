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
