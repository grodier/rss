package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// markCount counts userID's rows in table.
func markCount(t *testing.T, db *sql.DB, table markTable, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM `+string(table)+` WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// isMarked reports whether userID has a row in table for articleID itself
// (not a copy).
func isMarked(t *testing.T, db *sql.DB, table markTable, userID, articleID string) bool {
	t.Helper()
	var ok bool
	stmt := `SELECT EXISTS (SELECT 1 FROM ` + string(table) + ` WHERE user_id = $1 AND article_id = $2)`
	if err := db.QueryRow(stmt, userID, articleID).Scan(&ok); err != nil {
		t.Fatalf("select %s: %v", table, err)
	}
	return ok
}

// markCopies returns two articles in different feeds sharing a unique
// canonical URL, and an unrelated article.
func markCopies(t *testing.T, db *sql.DB) (a, b, unrelated string) {
	t.Helper()
	canonical := fmt.Sprintf("example.com/marked-%d", psqltest.Unique())
	a = newTestArticleWithCanonical(t, db, canonical)
	b = newTestArticleWithCanonical(t, db, canonical)
	unrelated = newTestArticleWithCanonical(t, db, canonical+"-other")
	return a, b, unrelated
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkedArticleIDs(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	ctx := t.Context()

	for _, table := range []markTable{readsTable, savesTable, starsTable} {
		t.Run(string(table), func(t *testing.T) {
			// insert adds the row directly, so these cases don't depend on
			// markArticle (reads have MarkRead, which has no copy rule).
			insert := func(t *testing.T, userID, articleID string) {
				t.Helper()
				if _, err := db.Exec(`INSERT INTO `+string(table)+` (user_id, article_id) VALUES ($1, $2)`, userID, articleID); err != nil {
					t.Fatalf("insert %s: %v", table, err)
				}
			}
			marked := func(t *testing.T, userID string, ids ...string) map[string]bool {
				t.Helper()
				got, err := markedArticleIDs(ctx, db, table, userID, ids)
				if err != nil {
					t.Fatalf("markedArticleIDs: %v", err)
				}
				return got
			}

			t.Run("an empty list returns an empty map without querying", func(t *testing.T) {
				// A closed DB fails any query, so success means none was made.
				closed := psqltest.NewDB(t)
				closed.Close()

				got, err := markedArticleIDs(ctx, closed, table, "00000000-0000-0000-0000-000000000000", nil)
				if err != nil {
					t.Fatalf("markedArticleIDs: %v", err)
				}
				if got == nil || len(got) != 0 {
					t.Errorf("markedArticleIDs = %#v, want empty map", got)
				}
			})

			t.Run("a marked article counts", func(t *testing.T) {
				user := newTestUser(t, db)
				article := newTestArticles(t, db, 1)[0]
				insert(t, user, article)

				if got := marked(t, user, article); !got[article] || len(got) != 1 {
					t.Errorf("markedArticleIDs = %v, want only %s", got, article)
				}
			})

			t.Run("an unmarked article doesn't", func(t *testing.T) {
				user := newTestUser(t, db)
				ids := newTestArticles(t, db, 2)
				insert(t, user, ids[0])

				if got := marked(t, user, ids[1]); len(got) != 0 {
					t.Errorf("markedArticleIDs = %v, want none", got)
				}
			})

			t.Run("a copy in another feed counts", func(t *testing.T) {
				user := newTestUser(t, db)
				a, b, unrelated := markCopies(t, db)
				insert(t, user, a)

				if got := marked(t, user, a, b, unrelated); !got[a] || !got[b] || len(got) != 2 {
					t.Errorf("markedArticleIDs = %v, want only %s and %s", got, a, b)
				}
			})

			t.Run("an empty canonical_url only matches itself", func(t *testing.T) {
				user := newTestUser(t, db)
				a := newTestArticleWithCanonical(t, db, "")
				b := newTestArticleWithCanonical(t, db, "")
				insert(t, user, a)

				if got := marked(t, user, a, b); !got[a] || len(got) != 1 {
					t.Errorf("markedArticleIDs = %v, want only %s", got, a)
				}
			})

			t.Run("another user's mark doesn't count", func(t *testing.T) {
				user := newTestUser(t, db)
				other := newTestUser(t, db)
				a, b, _ := markCopies(t, db)
				insert(t, other, a)

				if got := marked(t, user, a, b); len(got) != 0 {
					t.Errorf("markedArticleIDs = %v, want none", got)
				}
			})
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkArticle(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	ctx := t.Context()

	for _, table := range []markTable{savesTable, starsTable} {
		t.Run(string(table), func(t *testing.T) {
			mark := func(t *testing.T, userID, articleID string) {
				t.Helper()
				if err := markArticle(ctx, db, table, userID, articleID); err != nil {
					t.Fatalf("markArticle: %v", err)
				}
			}

			t.Run("marks an article", func(t *testing.T) {
				user := newTestUser(t, db)
				article := newTestArticles(t, db, 1)[0]
				mark(t, user, article)

				if !isMarked(t, db, table, user, article) {
					t.Errorf("%s has no row for %s", table, article)
				}
			})

			t.Run("a second mark is a no-op", func(t *testing.T) {
				user := newTestUser(t, db)
				article := newTestArticles(t, db, 1)[0]
				mark(t, user, article)
				mark(t, user, article)

				if n := markCount(t, db, table, user); n != 1 {
					t.Errorf("%s = %d, want 1", table, n)
				}
			})

			t.Run("a mark on a copy of a marked article adds nothing", func(t *testing.T) {
				user := newTestUser(t, db)
				a, b, _ := markCopies(t, db)
				mark(t, user, a)
				mark(t, user, b)

				if n := markCount(t, db, table, user); n != 1 {
					t.Errorf("%s = %d, want 1", table, n)
				}
				if isMarked(t, db, table, user, b) {
					t.Errorf("the copy %s got its own row", b)
				}
			})

			t.Run("an article with an empty canonical_url isn't a copy", func(t *testing.T) {
				user := newTestUser(t, db)
				a := newTestArticleWithCanonical(t, db, "")
				b := newTestArticleWithCanonical(t, db, "")
				mark(t, user, a)
				mark(t, user, b)

				if n := markCount(t, db, table, user); n != 2 {
					t.Errorf("%s = %d, want 2", table, n)
				}
			})

			t.Run("another user's mark on a copy doesn't stop this user's", func(t *testing.T) {
				user := newTestUser(t, db)
				other := newTestUser(t, db)
				a, b, _ := markCopies(t, db)
				mark(t, other, a)
				mark(t, user, b)

				if !isMarked(t, db, table, user, b) {
					t.Errorf("%s has no row for %s", table, b)
				}
			})

			t.Run("an unknown article returns ErrNoRecord", func(t *testing.T) {
				user := newTestUser(t, db)

				err := markArticle(ctx, db, table, user, "00000000-0000-0000-0000-000000000000")
				if !errors.Is(err, rss.ErrNoRecord) {
					t.Errorf("err = %v, want ErrNoRecord", err)
				}
			})

			t.Run("an unknown user returns ErrNoRecord", func(t *testing.T) {
				article := newTestArticles(t, db, 1)[0]

				err := markArticle(ctx, db, table, "00000000-0000-0000-0000-000000000000", article)
				if !errors.Is(err, rss.ErrNoRecord) {
					t.Errorf("err = %v, want ErrNoRecord", err)
				}
			})
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestUnmarkArticle(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	ctx := t.Context()

	for _, table := range []markTable{savesTable, starsTable} {
		t.Run(string(table), func(t *testing.T) {
			mark := func(t *testing.T, userID, articleID string) {
				t.Helper()
				if err := markArticle(ctx, db, table, userID, articleID); err != nil {
					t.Fatalf("markArticle: %v", err)
				}
			}
			unmark := func(t *testing.T, userID, articleID string) {
				t.Helper()
				if err := unmarkArticle(ctx, db, table, userID, articleID); err != nil {
					t.Fatalf("unmarkArticle: %v", err)
				}
			}

			t.Run("removes the mark", func(t *testing.T) {
				user := newTestUser(t, db)
				article := newTestArticles(t, db, 1)[0]
				mark(t, user, article)
				unmark(t, user, article)

				if n := markCount(t, db, table, user); n != 0 {
					t.Errorf("%s = %d, want 0", table, n)
				}
			})

			t.Run("unmarking removes the copies' marks too", func(t *testing.T) {
				user := newTestUser(t, db)
				a, b, unrelated := markCopies(t, db)
				// Rows on both copies, as when each was marked before the
				// other existed.
				for _, id := range []string{a, b, unrelated} {
					if _, err := db.Exec(`INSERT INTO `+string(table)+` (user_id, article_id) VALUES ($1, $2)`, user, id); err != nil {
						t.Fatalf("insert %s: %v", table, err)
					}
				}
				unmark(t, user, b)

				if isMarked(t, db, table, user, a) || isMarked(t, db, table, user, b) {
					t.Errorf("a copy is still marked after unmarking %s", b)
				}
				if !isMarked(t, db, table, user, unrelated) {
					t.Errorf("the unrelated article %s lost its mark", unrelated)
				}
			})

			t.Run("not marked is not an error", func(t *testing.T) {
				user := newTestUser(t, db)
				article := newTestArticles(t, db, 1)[0]

				unmark(t, user, article)
			})

			t.Run("another user's marks stay", func(t *testing.T) {
				user := newTestUser(t, db)
				other := newTestUser(t, db)
				a, b, _ := markCopies(t, db)
				mark(t, user, a)
				mark(t, other, a)
				unmark(t, user, b)

				if n := markCount(t, db, table, user); n != 0 {
					t.Errorf("user's %s = %d, want 0", table, n)
				}
				if !isMarked(t, db, table, other, a) {
					t.Errorf("other's mark on %s is gone", a)
				}
			})
		})
	}
}
