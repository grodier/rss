package psql

import (
	"database/sql"
	"errors"
	"fmt"
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

// newTestArticleWithCanonical saves one article with canonicalURL in a new feed
// and returns its ID. It's deleted along with the feed's site on cleanup.
func newTestArticleWithCanonical(t *testing.T, db *sql.DB, canonicalURL string) string {
	t.Helper()
	feed := newTestFeed(t, db)
	item := rss.Article{ExternalID: "a", Title: "Article", CanonicalURL: canonicalURL}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, []rss.Article{item}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	saved, err := NewArticleRepository(db).ListByFeed(t.Context(), feed.ID, rss.ArticleCursor{}, 1)
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d articles, want 1", len(saved))
	}
	return saved[0].ID
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

	// copies returns two articles in different feeds sharing a unique
	// canonical URL, and an unrelated article.
	copies := func(t *testing.T) (a, b, unrelated string) {
		t.Helper()
		canonical := fmt.Sprintf("example.com/post-%d", time.Now().UnixNano())
		a = newTestArticleWithCanonical(t, db, canonical)
		b = newTestArticleWithCanonical(t, db, canonical)
		unrelated = newTestArticleWithCanonical(t, db, canonical+"-other")
		return a, b, unrelated
	}

	t.Run("reading one copy marks every copy read", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, unrelated := copies(t)
		if err := repo.MarkRead(ctx, user, a); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}

		got, err := repo.ReadArticleIDs(ctx, user, []string{a, b, unrelated})
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if !got[a] || !got[b] || len(got) != 2 {
			t.Errorf("ReadArticleIDs = %v, want only %s and %s", got, a, b)
		}
	})

	t.Run("articles with an empty canonical_url only match themselves", func(t *testing.T) {
		user := newTestUser(t, db)
		a := newTestArticleWithCanonical(t, db, "")
		b := newTestArticleWithCanonical(t, db, "")
		if err := repo.MarkRead(ctx, user, a); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}

		got, err := repo.ReadArticleIDs(ctx, user, []string{a, b})
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if !got[a] || len(got) != 1 {
			t.Errorf("ReadArticleIDs = %v, want only %s", got, a)
		}
	})

	t.Run("another user's read of a copy marks nothing", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		a, b, unrelated := copies(t)
		if err := repo.MarkRead(ctx, other, a); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}

		got, err := repo.ReadArticleIDs(ctx, user, []string{a, b, unrelated})
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ReadArticleIDs = %v, want none", got)
		}
	})

	t.Run("deleting the read copy leaves the other unread", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, _ := copies(t)
		if err := repo.MarkRead(ctx, user, a); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM articles WHERE id = $1`, a); err != nil {
			t.Fatalf("delete article: %v", err)
		}

		got, err := repo.ReadArticleIDs(ctx, user, []string{b})
		if err != nil {
			t.Fatalf("ReadArticleIDs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ReadArticleIDs = %v, want none", got)
		}
	})
}

// timelineMarkRow reads userID's timeline_marks row; ok is false if there's
// none.
func timelineMarkRow(t *testing.T, db *sql.DB, userID string) (m rss.TimelineMark, dividerNull, ok bool) {
	t.Helper()
	var dividerAt sql.NullTime
	err := db.QueryRow(`SELECT seen_up_to, seen_at, divider_at FROM timeline_marks WHERE user_id = $1`, userID).Scan(&m.SeenUpTo, &m.SeenAt, &dividerAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rss.TimelineMark{}, false, false
	}
	if err != nil {
		t.Fatalf("select timeline_marks: %v", err)
	}
	m.DividerAt = dividerAt.Time
	return m, !dividerAt.Valid, true
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineMarkNone(t *testing.T) {
	db := psqltest.NewDB(t)
	user := newTestUser(t, db)

	m, err := NewReadRepository(db).TimelineMark(t.Context(), user)
	if err != nil {
		t.Fatalf("TimelineMark: %v", err)
	}
	if m != (rss.TimelineMark{}) {
		t.Errorf("TimelineMark = %+v, want zero", m)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkSeen(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()
	user := newTestUser(t, db)
	base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)

	// The first report stores the position and when, with no divider yet.
	if err := repo.MarkSeen(ctx, user, base); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	m, dividerNull, ok := timelineMarkRow(t, db, user)
	if !ok || !m.SeenUpTo.Equal(base) || m.SeenAt.IsZero() || !dividerNull {
		t.Fatalf("after the first report: %+v (divider NULL: %v, row: %v), want seen_up_to %v, seen_at set, divider_at NULL", m, dividerNull, ok, base)
	}
	got, err := repo.TimelineMark(ctx, user)
	if err != nil {
		t.Fatalf("TimelineMark: %v", err)
	}
	if !got.SeenUpTo.Equal(m.SeenUpTo) || !got.SeenAt.Equal(m.SeenAt) || !got.DividerAt.IsZero() {
		t.Errorf("TimelineMark = %+v, want %+v", got, m)
	}

	// A later position in the same visit raises seen_up_to and keeps divider_at.
	if _, err := db.Exec(`UPDATE timeline_marks SET divider_at = $2 WHERE user_id = $1`, user, base.Add(-time.Hour)); err != nil {
		t.Fatalf("set divider_at: %v", err)
	}
	later := base.Add(10 * time.Minute)
	if err := repo.MarkSeen(ctx, user, later); err != nil {
		t.Fatalf("MarkSeen later: %v", err)
	}
	m, _, _ = timelineMarkRow(t, db, user)
	if !m.SeenUpTo.Equal(later) || !m.DividerAt.Equal(base.Add(-time.Hour)) {
		t.Errorf("after a later report: %+v, want seen_up_to %v and divider_at %v", m, later, base.Add(-time.Hour))
	}

	// An earlier position doesn't lower seen_up_to.
	if err := repo.MarkSeen(ctx, user, base); err != nil {
		t.Fatalf("MarkSeen earlier: %v", err)
	}
	if m, _, _ = timelineMarkRow(t, db, user); !m.SeenUpTo.Equal(later) {
		t.Errorf("after an earlier report: seen_up_to = %v, want %v", m.SeenUpTo, later)
	}

	// A position in the future is stored as at most now.
	if err := repo.MarkSeen(ctx, user, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("MarkSeen future: %v", err)
	}
	if m, _, _ = timelineMarkRow(t, db, user); m.SeenUpTo.After(m.SeenAt) {
		t.Errorf("after a future report: seen_up_to = %v, after seen_at %v", m.SeenUpTo, m.SeenAt)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkSeenNewVisit(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()
	user := newTestUser(t, db)
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)

	if err := repo.MarkSeen(ctx, user, old); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if _, err := db.Exec(`UPDATE timeline_marks SET seen_at = now() - interval '1 hour' WHERE user_id = $1`, user); err != nil {
		t.Fatalf("set seen_at: %v", err)
	}
	if err := repo.MarkSeen(ctx, user, old.Add(time.Hour)); err != nil {
		t.Fatalf("MarkSeen again: %v", err)
	}

	m, dividerNull, _ := timelineMarkRow(t, db, user)
	if dividerNull || !m.DividerAt.Equal(old) {
		t.Errorf("divider_at = %v (NULL: %v), want the old seen_up_to %v", m.DividerAt, dividerNull, old)
	}
	if !m.SeenUpTo.Equal(old.Add(time.Hour)) {
		t.Errorf("seen_up_to = %v, want %v", m.SeenUpTo, old.Add(time.Hour))
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineMarkDeletedWithUser(t *testing.T) {
	db := psqltest.NewDB(t)
	user := newTestUser(t, db)
	if err := NewReadRepository(db).MarkSeen(t.Context(), user, time.Now()); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, user); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, _, ok := timelineMarkRow(t, db, user); ok {
		t.Errorf("the mark outlived its user")
	}
}
