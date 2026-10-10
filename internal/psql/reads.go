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

// ReadArticleIDs reports which of articleIDs userID has read. An empty
// articleIDs returns an empty map without querying.
func (r *ReadRepository) ReadArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	read := map[string]bool{}
	if len(articleIDs) == 0 {
		return read, nil
	}

	stmt := `SELECT article_id FROM reads WHERE user_id = $1 AND article_id = ANY($2)`

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
