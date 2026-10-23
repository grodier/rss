package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func saveCount(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM saves WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count saves: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestSaveRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSaveRepository(db)
	ctx := t.Context()

	savedIDs := func(t *testing.T, userID string, ids ...string) map[string]bool {
		t.Helper()
		got, err := repo.SavedArticleIDs(ctx, userID, ids)
		if err != nil {
			t.Fatalf("SavedArticleIDs: %v", err)
		}
		return got
	}
	save := func(t *testing.T, userID, articleID string) {
		t.Helper()
		if err := repo.Save(ctx, userID, articleID); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// copies returns two articles in different feeds sharing a unique
	// canonical URL, and an unrelated article.
	copies := func(t *testing.T) (a, b, unrelated string) {
		t.Helper()
		canonical := fmt.Sprintf("example.com/saved-%d", time.Now().UnixNano())
		a = newTestArticleWithCanonical(t, db, canonical)
		b = newTestArticleWithCanonical(t, db, canonical)
		unrelated = newTestArticleWithCanonical(t, db, canonical+"-other")
		return a, b, unrelated
	}

	t.Run("Save then SavedArticleIDs reports it saved", func(t *testing.T) {
		user := newTestUser(t, db)
		ids := newTestArticles(t, db, 2)
		save(t, user, ids[0])

		if got := savedIDs(t, user, ids...); !got[ids[0]] || len(got) != 1 {
			t.Errorf("SavedArticleIDs = %v, want only %s", got, ids[0])
		}
	})

	t.Run("saving twice is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]
		save(t, user, article)
		save(t, user, article)

		if n := saveCount(t, db, user); n != 1 {
			t.Errorf("saves = %d, want 1", n)
		}
	})

	t.Run("saving a copy of a saved article adds no row; both report saved", func(t *testing.T) {
		user := newTestUser(t, db)
		a, b, unrelated := copies(t)
		save(t, user, a)
		save(t, user, b)

		if n := saveCount(t, db, user); n != 1 {
			t.Errorf("saves = %d, want 1", n)
		}
		if got := savedIDs(t, user, a, b, unrelated); !got[a] || !got[b] || len(got) != 2 {
			t.Errorf("SavedArticleIDs = %v, want only %s and %s", got, a, b)
		}
	})

	t.Run("articles with an empty canonical_url only match themselves", func(t *testing.T) {
		user := newTestUser(t, db)
		a := newTestArticleWithCanonical(t, db, "")
		b := newTestArticleWithCanonical(t, db, "")
		save(t, user, a)

		if got := savedIDs(t, user, a, b); !got[a] || len(got) != 1 {
			t.Errorf("SavedArticleIDs = %v, want only %s", got, a)
		}
		save(t, user, b)
		if n := saveCount(t, db, user); n != 2 {
			t.Errorf("saves = %d, want 2", n)
		}
	})

	t.Run("Unsave of either copy clears both", func(t *testing.T) {
		for _, unsaveFirst := range []bool{true, false} {
			user := newTestUser(t, db)
			a, b, unrelated := copies(t)
			save(t, user, a)
			save(t, user, unrelated)

			target := b
			if unsaveFirst {
				target = a
			}
			if err := repo.Unsave(ctx, user, target); err != nil {
				t.Fatalf("Unsave: %v", err)
			}

			if got := savedIDs(t, user, a, b, unrelated); !got[unrelated] || len(got) != 1 {
				t.Errorf("after Unsave(%s): SavedArticleIDs = %v, want only %s", target, got, unrelated)
			}
		}
	})

	t.Run("Unsave of an unsaved article is fine", func(t *testing.T) {
		user := newTestUser(t, db)
		article := newTestArticles(t, db, 1)[0]

		if err := repo.Unsave(ctx, user, article); err != nil {
			t.Errorf("Unsave: %v", err)
		}
	})

	t.Run("unknown article is ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.Save(ctx, user, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("unknown user is ErrNoRecord", func(t *testing.T) {
		article := newTestArticles(t, db, 1)[0]

		err := repo.Save(ctx, "00000000-0000-0000-0000-000000000000", article)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("another user's saves don't show", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		a, b, unrelated := copies(t)
		save(t, other, a)
		save(t, other, unrelated)

		if got := savedIDs(t, user, a, b, unrelated); len(got) != 0 {
			t.Errorf("SavedArticleIDs = %v, want none", got)
		}
		// Nor do they stop this user saving a copy.
		save(t, user, b)
		if got := savedIDs(t, user, a, b); !got[a] || !got[b] {
			t.Errorf("SavedArticleIDs = %v, want %s and %s", got, a, b)
		}
	})

	t.Run("SavedArticleIDs with no IDs returns an empty map", func(t *testing.T) {
		user := newTestUser(t, db)

		got, err := repo.SavedArticleIDs(ctx, user, nil)
		if err != nil {
			t.Fatalf("SavedArticleIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("SavedArticleIDs = %#v, want empty map", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListSaved(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSaveRepository(db)
	ctx := t.Context()

	// One feed's articles, with a summary and content that ListSaved must
	// leave out.
	feed := newTestFeed(t, db)
	items := make([]rss.Article, 4)
	for i := range items {
		items[i] = rss.Article{ExternalID: string(rune('a' + i)), Title: "Article", Summary: "<p>summary</p>", Content: "<p>content</p>"}
	}
	if _, err := NewFeedRepository(db).SaveFetch(ctx, feed, items); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	articles, err := NewArticleRepository(db).ListByFeed(ctx, feed.ID, rss.ArticleCursor{}, len(items))
	if err != nil {
		t.Fatalf("ListByFeed: %v", err)
	}
	if len(articles) != len(items) {
		t.Fatalf("saved %d articles, want %d", len(articles), len(items))
	}

	// saveAt saves articleID for userID at savedAt.
	saveAt := func(t *testing.T, userID, articleID string, savedAt time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO saves (user_id, article_id, saved_at) VALUES ($1, $2, $3)`, userID, articleID, savedAt); err != nil {
			t.Fatalf("insert save: %v", err)
		}
	}

	user := newTestUser(t, db)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Two saves share the newest saved_at, so the article ID breaks the tie.
	saveAt(t, user, articles[0].ID, base)
	saveAt(t, user, articles[1].ID, base.Add(2*time.Hour))
	saveAt(t, user, articles[2].ID, base.Add(2*time.Hour))
	saveAt(t, user, articles[3].ID, base.Add(time.Hour))

	// Another user's save isn't listed.
	other := newTestUser(t, db)
	saveAt(t, other, articles[0].ID, base.Add(3*time.Hour))

	hi, lo := articles[1].ID, articles[2].ID
	if hi < lo {
		hi, lo = lo, hi
	}
	want := []string{hi, lo, articles[3].ID, articles[0].ID}

	all, err := repo.ListSaved(ctx, user, rss.ArticleCursor{}, 100)
	if err != nil {
		t.Fatalf("ListSaved: %v", err)
	}

	t.Run("newest saved first, only this user's", func(t *testing.T) {
		var got []string
		for _, item := range all {
			got = append(got, item.Article.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("ListSaved IDs = %v, want %v", got, want)
		}
	})

	t.Run("each item has its feed and saved_at, without summary or content", func(t *testing.T) {
		for _, item := range all {
			if item.Feed.ID != feed.ID || item.Feed.Title != feed.Title {
				t.Errorf("article %s: feed = %q (%s), want %q (%s)", item.Article.ID, item.Feed.Title, item.Feed.ID, feed.Title, feed.ID)
			}
			if item.Article.Summary != "" || item.Article.Content != "" {
				t.Errorf("article %s: Summary = %q, Content = %q, want both empty", item.Article.ID, item.Article.Summary, item.Article.Content)
			}
			if item.Article.Title != "Article" {
				t.Errorf("article %s: Title = %q, want %q", item.Article.ID, item.Article.Title, "Article")
			}
		}
		if len(all) > 0 && !all[0].SavedAt.Equal(base.Add(2*time.Hour)) {
			t.Errorf("first SavedAt = %v, want %v", all[0].SavedAt, base.Add(2*time.Hour))
		}
	})

	t.Run("paging has no overlap or gap", func(t *testing.T) {
		// Pages of one walk every boundary, including between the two
		// saves with the same saved_at.
		var got []string
		cursor := rss.ArticleCursor{}
		for range len(want) + 1 {
			page, err := repo.ListSaved(ctx, user, cursor, 1)
			if err != nil {
				t.Fatalf("ListSaved: %v", err)
			}
			if len(page) == 0 {
				break
			}
			got = append(got, page[0].Article.ID)
			cursor = rss.ArticleCursor{At: page[0].SavedAt, ID: page[0].Article.ID}
		}
		if !slices.Equal(got, want) {
			t.Errorf("paged IDs = %v, want %v", got, want)
		}
	})

	t.Run("no saves", func(t *testing.T) {
		got, err := repo.ListSaved(ctx, newTestUser(t, db), rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListSaved: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ListSaved = %v, want none", got)
		}
	})
}
