package psql

import (
	"context"
	"database/sql"

	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
)

// markTable is one of the tables that record a reader's marks on articles:
// reads, saves or stars. Each has (user_id, article_id) as its primary key.
type markTable string

const (
	readsTable markTable = "reads"
	savesTable markTable = "saves"
	starsTable markTable = "stars"
)

// markedArticleIDs reports which of articleIDs userID has a row for in
// table, counting a mark on any copy (same non-empty canonical_url). An
// empty articleIDs returns an empty map without querying.
func markedArticleIDs(ctx context.Context, db *sql.DB, table markTable, userID string, articleIDs []string) (map[string]bool, error) {
	marked := map[string]bool{}
	if len(articleIDs) == 0 {
		return marked, nil
	}

	// Two EXISTS joined by OR, rather than one EXISTS with the OR inside: the
	// planner turns a lone EXISTS into a semi-join that scans all of the
	// user's marks, while these stay per-article lookups on table's indexes
	// and articles_canonical_url_idx (which needs b.canonical_url <> ''
	// spelled out).
	stmt := `SELECT a.id
		FROM articles a
		WHERE a.id = ANY($2)
		  AND (
		      EXISTS (SELECT 1 FROM ` + string(table) + ` m WHERE m.user_id = $1 AND m.article_id = a.id)
		      OR (a.canonical_url <> '' AND EXISTS (
		          SELECT 1
		          FROM articles b
		          JOIN ` + string(table) + ` m ON m.article_id = b.id
		          WHERE m.user_id = $1
		            AND b.canonical_url = a.canonical_url
		            AND b.canonical_url <> ''
		      ))
		  )`

	rows, err := db.QueryContext(ctx, stmt, userID, pq.Array(articleIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		marked[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return marked, nil
}

// markArticle adds userID's mark on articleID to table, unless they've
// marked it or a copy already. rss.ErrNoRecord if the article (or user)
// doesn't exist.
func markArticle(ctx context.Context, db *sql.DB, table markTable, userID, articleID string) error {
	// The final SELECT reports whether the article exists, since the INSERT
	// inserts nothing both for an unknown article and for one already marked.
	stmt := `WITH a AS (
		    SELECT id, canonical_url FROM articles WHERE id = $2
		), ins AS (
		    INSERT INTO ` + string(table) + ` (user_id, article_id)
		    SELECT $1, a.id
		    FROM a
		    WHERE NOT (a.canonical_url <> '' AND EXISTS (
		        SELECT 1
		        FROM articles b
		        JOIN ` + string(table) + ` m ON m.article_id = b.id
		        WHERE m.user_id = $1
		          AND b.canonical_url = a.canonical_url
		          AND b.canonical_url <> ''
		    ))
		    ON CONFLICT DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM a)`

	var exists bool
	if err := db.QueryRowContext(ctx, stmt, userID, articleID).Scan(&exists); err != nil {
		if isForeignKeyViolation(err) {
			return rss.ErrNoRecord
		}
		return err
	}
	if !exists {
		return rss.ErrNoRecord
	}
	return nil
}

// unmarkArticle removes userID's marks on articleID and every copy of it
// from table. Not marked is not an error.
func unmarkArticle(ctx context.Context, db *sql.DB, table markTable, userID, articleID string) error {
	stmt := `DELETE FROM ` + string(table) + `
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

	_, err := db.ExecContext(ctx, stmt, userID, articleID)
	return err
}
