package psql

import (
	"context"
	"database/sql"

	"github.com/grodier/rss/internal/rss"
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
	return markArticle(ctx, r.DB, starsTable, userID, articleID)
}

// Unstar removes userID's stars of articleID and of every copy of it. Not
// starred is not an error.
func (r *StarRepository) Unstar(ctx context.Context, userID, articleID string) error {
	return unmarkArticle(ctx, r.DB, starsTable, userID, articleID)
}

// StarredArticleIDs reports which of articleIDs userID has starred. An
// article counts as starred if the user starred it or any article with the
// same non-empty canonical_url (a copy of it in another feed); articles with
// an empty canonical_url only match themselves. An empty articleIDs returns
// an empty map without querying.
func (r *StarRepository) StarredArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	return markedArticleIDs(ctx, r.DB, starsTable, userID, articleIDs)
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

// sharedStarred returns the condition a shared starred-only timeline adds
// on the feed alias f: no newsletter feed's article (see sharedWhere). ""
// unless shared.
func sharedStarred(shared bool) string {
	if !shared {
		return ""
	}
	return `
		  AND f.owner_id IS NULL`
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
		  AND ($2::timestamptz IS NULL OR (s.starred_at, s.article_id) < ($2, $3::uuid))` + timelineWhere(filter, args, "sub", "a", "a.feed_id") + sharedWhere(shared, "a") + sharedStarred(shared) + `
		ORDER BY s.starred_at DESC, s.article_id DESC
		LIMIT $4`
}
