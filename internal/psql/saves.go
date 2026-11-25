package psql

import (
	"context"
	"database/sql"

	"github.com/grodier/rss/internal/rss"
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
	return markArticle(ctx, r.DB, savesTable, userID, articleID)
}

// Unsave removes userID's saves of articleID and of every copy of it. Not
// saved is not an error.
func (r *SaveRepository) Unsave(ctx context.Context, userID, articleID string) error {
	return unmarkArticle(ctx, r.DB, savesTable, userID, articleID)
}

// SavedArticleIDs reports which of articleIDs userID has saved. An article
// counts as saved if the user saved it or any article with the same non-empty
// canonical_url (a copy of it in another feed); articles with an empty
// canonical_url only match themselves. An empty articleIDs returns an empty
// map without querying.
func (r *SaveRepository) SavedArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	return markedArticleIDs(ctx, r.DB, savesTable, userID, articleIDs)
}

// HasSaves reports whether userID has saved anything.
func (r *SaveRepository) HasSaves(ctx context.Context, userID string) (bool, error) {
	var has bool
	err := r.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM saves WHERE user_id = $1)`, userID).Scan(&has)
	return has, err
}

// ListSaved returns up to limit of userID's saved articles whose title,
// page title, excerpt, feed title or the user's name for the feed contains q
// (ignoring case; every saved article if q is ""), newest saved first
// (saved_at, then article ID), starting after before (from the start if
// before is zero; its At is a saved_at). Feed titles are the user's names
// for their subscriptions where they've set one. Summary and Content are left empty.
func (r *SaveRepository) ListSaved(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
	// A scan of the user's saves, filtered with ILIKE: a user's saves are a
	// small set (see the Performance watchlist in docs/backlog.md). Saved
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
		if item.Article, item.Feed, err = scanArticleWithFeed(rows, &item.SavedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
