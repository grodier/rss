package psql

import (
	"context"
	"database/sql"
	"errors"

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
