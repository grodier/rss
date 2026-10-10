package psql

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestArticles saves n articles in a new feed and returns their IDs. They're
// deleted along with the feed's site on cleanup.
func newTestArticles(t *testing.T, db *sql.DB, n int) []string {
	t.Helper()
	feed := newTestFeed(t, db)
	items := make([]rss.Article, n)
	for i := range items {
		items[i] = rss.Article{ExternalID: string(rune('a' + i)), Title: "Article"}
	}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, items); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	saved, err := NewArticleRepository(db).ListByFeed(t.Context(), feed.ID, rss.ArticleCursor{}, n)
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}
	if len(saved) != n {
		t.Fatalf("saved %d articles, want %d", len(saved), n)
	}
	ids := make([]string, n)
	for i, a := range saved {
		ids[i] = a.ID
	}
	return ids
}

func readAt(t *testing.T, db *sql.DB, userID, articleID string) time.Time {
	t.Helper()
	var at time.Time
	if err := db.QueryRow(`SELECT read_at FROM reads WHERE user_id = $1 AND article_id = $2`, userID, articleID).Scan(&at); err != nil {
		t.Fatalf("select read_at: %v", err)
	}
	return at
}

// Requires a migrated database; see psqltest.NewDB.
func TestReadRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()

	t.Run("MarkRead inserts; marking again keeps the first read_at", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]

		if err := repo.MarkRead(ctx, user, article); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
		first := readAt(t, db, user, article)

		if err := repo.MarkRead(ctx, user, article); err != nil {
			t.Fatalf("second MarkRead: %v", err)
		}
		if got := readAt(t, db, user, article); !got.Equal(first) {
			t.Errorf("read_at = %v, want first read_at %v", got, first)
		}
	})

	t.Run("unknown article is ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.MarkRead(ctx, user, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("ReadArticleIDs reports only this user's reads", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		ids := newTestArticles(t, db, 3)
		mine, theirs, unread := ids[0], ids[1], ids[2]
		for _, m := range []struct{ user, article string }{{user, mine}, {other, theirs}} {
			if err := repo.MarkRead(ctx, m.user, m.article); err != nil {
				t.Fatalf("MarkRead: %v", err)
			}
		}

		got, err := repo.ReadArticleIDs(ctx, user, []string{mine, theirs, unread})
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if !got[mine] || len(got) != 1 {
			t.Errorf("ReadArticleIDs = %v, want only %s", got, mine)
		}
	})

	t.Run("ReadArticleIDs with no IDs returns an empty map", func(t *testing.T) {
		user := newTestUser(t, db)

		got, err := repo.ReadArticleIDs(ctx, user, nil)
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("ReadArticleIDs = %#v, want empty map", got)
		}
	})

	t.Run("deleting the article deletes its reads", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]
		if err := repo.MarkRead(ctx, user, article); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}

		if _, err := db.Exec(`DELETE FROM articles WHERE id = $1`, article); err != nil {
			t.Fatalf("delete article: %v", err)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM reads WHERE article_id = $1`, article).Scan(&n); err != nil {
			t.Fatalf("count reads: %v", err)
		}
		if n != 0 {
			t.Errorf("reads = %d, want 0", n)
		}
	})
}
