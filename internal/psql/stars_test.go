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

func starCount(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM stars WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count stars: %v", err)
	}
	return n
}

// starCopies returns two articles in different feeds sharing a unique
// canonical URL, and an unrelated article.
func starCopies(t *testing.T, db *sql.DB) (a, b, unrelated string) {
	t.Helper()
	canonical := fmt.Sprintf("example.com/starred-%d", time.Now().UnixNano())
	a = newTestArticleWithCanonical(t, db, canonical)
	b = newTestArticleWithCanonical(t, db, canonical)
	unrelated = newTestArticleWithCanonical(t, db, canonical+"-other")
	return a, b, unrelated
}

func mustStar(t *testing.T, repo *StarRepository, userID, articleID string) {
	t.Helper()
	if err := repo.Star(t.Context(), userID, articleID); err != nil {
		t.Fatalf("Star: %v", err)
	}
}

func starredIDs(t *testing.T, repo *StarRepository, userID string, ids ...string) map[string]bool {
	t.Helper()
	got, err := repo.StarredArticleIDs(t.Context(), userID, ids)
	if err != nil {
		t.Fatalf("StarredArticleIDs: %v", err)
	}
	return got
}

// Requires a migrated database; see psqltest.NewDB.
func TestStar(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewStarRepository(db)
	ctx := t.Context()

	t.Run("stars an article", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]
		mustStar(t, repo, user, article)

		if n := starCount(t, db, user); n != 1 {
			t.Errorf("stars = %d, want 1", n)
		}
	})

	t.Run("starring twice is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]
		mustStar(t, repo, user, article)
		mustStar(t, repo, user, article)

		if n := starCount(t, db, user); n != 1 {
			t.Errorf("stars = %d, want 1", n)
		}
	})

	t.Run("starring a copy of a starred article adds no row", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, _ := starCopies(t, db)
		mustStar(t, repo, user, a)
		mustStar(t, repo, user, b)

		if n := starCount(t, db, user); n != 1 {
			t.Errorf("stars = %d, want 1", n)
		}
	})

	t.Run("unknown article is ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.Star(ctx, user, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("unknown user is ErrNoRecord", func(t *testing.T) {
		article := newTestArticles(t, db, 1)[0]

		err := repo.Star(ctx, "00000000-0000-0000-0000-000000000000", article)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestUnstar(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewStarRepository(db)
	ctx := t.Context()

	t.Run("Unstar of either copy clears both", func(t *testing.T) {
		for _, unstarFirst := range []bool{true, false} {
			user := newTestUser(t, db)
			a, b, unrelated := starCopies(t, db)
			mustStar(t, repo, user, a)
			mustStar(t, repo, user, unrelated)

			target := b
			if unstarFirst {
				target = a
			}
			if err := repo.Unstar(ctx, user, target); err != nil {
				t.Fatalf("Unstar: %v", err)
			}

			if got := starredIDs(t, repo, user, a, b, unrelated); !got[unrelated] || len(got) != 1 {
				t.Errorf("after Unstar(%s): StarredArticleIDs = %v, want only %s", target, got, unrelated)
			}
		}
	})

	t.Run("not starred is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]

		if err := repo.Unstar(ctx, user, article); err != nil {
			t.Errorf("Unstar: %v", err)
		}
	})

	t.Run("another user's stars stay", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		a, b, _ := starCopies(t, db)
		mustStar(t, repo, user, a)
		mustStar(t, repo, other, a)

		if err := repo.Unstar(ctx, user, b); err != nil {
			t.Fatalf("Unstar: %v", err)
		}
		if n := starCount(t, db, user); n != 0 {
			t.Errorf("user's stars = %d, want 0", n)
		}
		if got := starredIDs(t, repo, other, a, b); !got[a] || !got[b] {
			t.Errorf("other's StarredArticleIDs = %v, want %s and %s", got, a, b)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestStarredArticleIDs(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewStarRepository(db)
	ctx := t.Context()

	t.Run("own star", func(t *testing.T) {
		user := newTestUser(t, db)
		ids := newTestArticles(t, db, 2)
		mustStar(t, repo, user, ids[0])

		if got := starredIDs(t, repo, user, ids...); !got[ids[0]] || len(got) != 1 {
			t.Errorf("StarredArticleIDs = %v, want only %s", got, ids[0])
		}
	})

	t.Run("a copy's star", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, unrelated := starCopies(t, db)
		mustStar(t, repo, user, a)

		if got := starredIDs(t, repo, user, a, b, unrelated); !got[a] || !got[b] || len(got) != 2 {
			t.Errorf("StarredArticleIDs = %v, want only %s and %s", got, a, b)
		}
	})

	t.Run("an article without a canonical URL only matches itself", func(t *testing.T) {
		user := newTestUser(t, db)
		a := newTestArticleWithCanonical(t, db, "")
		b := newTestArticleWithCanonical(t, db, "")
		mustStar(t, repo, user, a)

		if got := starredIDs(t, repo, user, a, b); !got[a] || len(got) != 1 {
			t.Errorf("StarredArticleIDs = %v, want only %s", got, a)
		}
	})

	t.Run("another user's stars don't show", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		a, b, _ := starCopies(t, db)
		mustStar(t, repo, other, a)

		if got := starredIDs(t, repo, user, a, b); len(got) != 0 {
			t.Errorf("StarredArticleIDs = %v, want none", got)
		}
	})

	t.Run("an empty list returns an empty map without querying", func(t *testing.T) {
		// A closed DB fails any query, so success means none was made.
		closed := psqltest.NewDB(t)
		closed.Close()

		got, err := NewStarRepository(closed).StarredArticleIDs(ctx, "00000000-0000-0000-0000-000000000000", nil)
		if err != nil {
			t.Fatalf("StarredArticleIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("StarredArticleIDs = %#v, want empty map", got)
		}
	})
}
