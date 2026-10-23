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

func hideCount(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM hides WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count hides: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestHideRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewHideRepository(db)
	ctx := t.Context()

	hiddenIDs := func(t *testing.T, userID string, ids ...string) map[string]bool {
		t.Helper()
		got, err := repo.HiddenArticleIDs(ctx, userID, ids)
		if err != nil {
			t.Fatalf("HiddenArticleIDs: %v", err)
		}
		return got
	}
	hide := func(t *testing.T, userID, articleID string) {
		t.Helper()
		if err := repo.Hide(ctx, userID, articleID); err != nil {
			t.Fatalf("Hide: %v", err)
		}
	}

	// copies returns two articles in different feeds sharing a unique
	// canonical URL, and an unrelated article.
	copies := func(t *testing.T) (a, b, unrelated string) {
		t.Helper()
		canonical := fmt.Sprintf("example.com/hidden-%d", time.Now().UnixNano())
		a = newTestArticleWithCanonical(t, db, canonical)
		b = newTestArticleWithCanonical(t, db, canonical)
		unrelated = newTestArticleWithCanonical(t, db, canonical+"-other")
		return a, b, unrelated
	}

	t.Run("Hide then HiddenArticleIDs reports it hidden", func(t *testing.T) {
		user := newTestUser(t, db)
		ids := newTestArticles(t, db, 2)
		hide(t, user, ids[0])

		if got := hiddenIDs(t, user, ids...); !got[ids[0]] || len(got) != 1 {
			t.Errorf("HiddenArticleIDs = %v, want only %s", got, ids[0])
		}
	})

	t.Run("hiding twice is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]
		hide(t, user, article)
		hide(t, user, article)

		if n := hideCount(t, db, user); n != 1 {
			t.Errorf("hides = %d, want 1", n)
		}
	})

	t.Run("hiding a copy of a hidden article adds no row; both report hidden", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, unrelated := copies(t)
		hide(t, user, a)
		hide(t, user, b)

		if n := hideCount(t, db, user); n != 1 {
			t.Errorf("hides = %d, want 1", n)
		}
		if got := hiddenIDs(t, user, a, b, unrelated); !got[a] || !got[b] || len(got) != 2 {
			t.Errorf("HiddenArticleIDs = %v, want only %s and %s", got, a, b)
		}
	})

	t.Run("articles with an empty canonical_url only match themselves", func(t *testing.T) {
		user := newTestUser(t, db)
		a := newTestArticleWithCanonical(t, db, "")
		b := newTestArticleWithCanonical(t, db, "")
		hide(t, user, a)

		if got := hiddenIDs(t, user, a, b); !got[a] || len(got) != 1 {
			t.Errorf("HiddenArticleIDs = %v, want only %s", got, a)
		}
	})

	t.Run("Unhide of either copy clears both", func(t *testing.T) {
		for _, unhideFirst := range []bool{true, false} {
			user := newTestUser(t, db)
			a, b, unrelated := copies(t)
			hide(t, user, a)
			hide(t, user, unrelated)

			target := b
			if unhideFirst {
				target = a
			}
			if err := repo.Unhide(ctx, user, target); err != nil {
				t.Fatalf("Unhide: %v", err)
			}

			if got := hiddenIDs(t, user, a, b, unrelated); !got[unrelated] || len(got) != 1 {
				t.Errorf("after Unhide(%s): HiddenArticleIDs = %v, want only %s", target, got, unrelated)
			}
		}
	})

	t.Run("Unhide of an article that isn't hidden is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]

		if err := repo.Unhide(ctx, user, article); err != nil {
			t.Errorf("Unhide: %v", err)
		}
	})

	t.Run("unknown article is ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.Hide(ctx, user, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("unknown user is ErrNoRecord", func(t *testing.T) {
		article := newTestArticles(t, db, 1)[0]

		err := repo.Hide(ctx, "00000000-0000-0000-0000-000000000000", article)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("another user's hides don't show", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		a, b, unrelated := copies(t)
		hide(t, other, a)
		hide(t, other, unrelated)

		if got := hiddenIDs(t, user, a, b, unrelated); len(got) != 0 {
			t.Errorf("HiddenArticleIDs = %v, want none", got)
		}
	})

	t.Run("HiddenArticleIDs with no IDs returns an empty map", func(t *testing.T) {
		user := newTestUser(t, db)

		got, err := repo.HiddenArticleIDs(ctx, user, nil)
		if err != nil {
			t.Fatalf("HiddenArticleIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("HiddenArticleIDs = %#v, want empty map", got)
		}
	})
}
