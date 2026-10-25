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

// Requires a migrated database; see psqltest.NewDB.
func TestListStarred(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewStarRepository(db)
	feeds := NewFeedRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	// The user subscribes to A, B and C, and used to subscribe to U. A is
	// tagged T1, B is tagged T2. Each feed has one article, starred in the
	// order U, A, B, C (C newest); a1 and b1 share a starred_at, so the
	// article ID breaks the tie.
	user := newTestUser(t, db)
	ids := newTestSubscriptions(t, db, user, 4)
	feedOf := map[string]string{"A": ids[0], "B": ids[1], "C": ids[2], "U": ids[3]}
	t1, t2 := mustCreateTag(t, tags, user, "T1"), mustCreateTag(t, tags, user, "T2")
	mustSetFeeds(t, tags, user, t1.ID, feedOf["A"])
	mustSetFeeds(t, tags, user, t2.ID, feedOf["B"])

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nameOf := map[string]string{}
	articleOf := map[string]string{}
	for _, c := range []struct {
		feed   string
		offset time.Duration
	}{
		{"U", 0},
		{"A", time.Hour},
		{"B", time.Hour},
		{"C", 2 * time.Hour},
	} {
		feed := rss.Feed{ID: feedOf[c.feed], NextFetch: time.Now().Add(time.Hour)}
		ext := c.feed + "1"
		items := []rss.Article{{ExternalID: ext, Title: "Article", Summary: "<p>summary</p>", Content: "<p>content</p>"}}
		if _, err := feeds.SaveFetch(ctx, feed, items); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		var id string
		if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feed.ID, ext).Scan(&id); err != nil {
			t.Fatalf("select article: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO stars (user_id, article_id, starred_at) VALUES ($1, $2, $3)`, user, id, base.Add(c.offset)); err != nil {
			t.Fatalf("insert star: %v", err)
		}
		nameOf[id] = c.feed
		articleOf[c.feed] = id
	}
	if err := NewSubscriptionRepository(db).Unsubscribe(ctx, user, feedOf["U"]); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	// Another user's star never shows.
	other := newTestUser(t, db)
	mustStar(t, repo, other, newTestArticles(t, db, 1)[0])
	mustStar(t, repo, other, articleOf["A"])

	// list returns filter's stars as feed names, newest starred first.
	list := func(t *testing.T, userID string, filter rss.TimelineFilter) []string {
		t.Helper()
		items, err := repo.ListStarred(ctx, userID, filter, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListStarred: %v", err)
		}
		got := []string{}
		for _, item := range items {
			got = append(got, nameOf[item.Article.ID])
		}
		return got
	}

	// A and B share a starred_at; the higher article ID comes first.
	ab := []string{"A", "B"}
	if articleOf["B"] > articleOf["A"] {
		ab = []string{"B", "A"}
	}
	all := append(append([]string{"C"}, ab...), "U")

	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   []string
	}{
		{"show all includes a star from an unsubscribed feed", rss.TimelineFilter{StarredOnly: true}, all},
		{"show only a tag leaves out the unsubscribed feed", rss.TimelineFilter{StarredOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{t1.ID}},
			[]string{"A"}},
		{"show only a feed", rss.TimelineFilter{StarredOnly: true, OnlyIncluded: true, IncludeFeedIDs: []string{feedOf["C"]}},
			[]string{"C"}},
		{"leave out a tag keeps the unsubscribed feed", rss.TimelineFilter{StarredOnly: true, ExcludeTagIDs: []string{t1.ID}},
			[]string{"C", "B", "U"}},
		{"leave out a feed keeps the unsubscribed feed", rss.TimelineFilter{StarredOnly: true, ExcludeFeedIDs: []string{feedOf["B"]}},
			[]string{"C", "A", "U"}},
		{"leave out wins over show", rss.TimelineFilter{StarredOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{t1.ID, t2.ID}, ExcludeTagIDs: []string{t2.ID}},
			[]string{"A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(t, user, tt.filter); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("only this user's stars", func(t *testing.T) {
		if got := list(t, other, rss.TimelineFilter{StarredOnly: true}); len(got) != 2 {
			t.Errorf("other user's stars = %d, want 2", len(got))
		}
		if got := list(t, newTestUser(t, db), rss.TimelineFilter{StarredOnly: true}); len(got) != 0 {
			t.Errorf("new user's stars = %v, want none", got)
		}
	})

	t.Run("each item has its feed and starred_at, without summary or content", func(t *testing.T) {
		items, err := repo.ListStarred(ctx, user, rss.TimelineFilter{StarredOnly: true}, rss.ArticleCursor{}, 100)
		if err != nil {
			t.Fatalf("ListStarred: %v", err)
		}
		for _, item := range items {
			if item.Feed.ID != feedOf[nameOf[item.Article.ID]] {
				t.Errorf("article %s: feed = %s, want %s", item.Article.ID, item.Feed.ID, feedOf[nameOf[item.Article.ID]])
			}
			if item.Article.Summary != "" || item.Article.Content != "" {
				t.Errorf("article %s: Summary = %q, Content = %q, want both empty", item.Article.ID, item.Article.Summary, item.Article.Content)
			}
		}
		if len(items) > 0 && !items[0].StarredAt.Equal(base.Add(2*time.Hour)) {
			t.Errorf("first StarredAt = %v, want %v", items[0].StarredAt, base.Add(2*time.Hour))
		}
	})

	t.Run("paging by starred_at has no overlap or gap", func(t *testing.T) {
		var got []string
		cursor := rss.ArticleCursor{}
		for range len(all) + 1 {
			page, err := repo.ListStarred(ctx, user, rss.TimelineFilter{StarredOnly: true}, cursor, 1)
			if err != nil {
				t.Fatalf("ListStarred: %v", err)
			}
			if len(page) == 0 {
				break
			}
			got = append(got, nameOf[page[0].Article.ID])
			cursor = rss.ArticleCursor{At: page[0].StarredAt, ID: page[0].Article.ID}
		}
		if !slices.Equal(got, all) {
			t.Errorf("paged %v, want %v", got, all)
		}
	})

	t.Run("the reader's name for the feed", func(t *testing.T) {
		if err := NewSubscriptionRepository(db).Rename(ctx, user, feedOf["C"], "My C"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		items, err := repo.ListStarred(ctx, user, rss.TimelineFilter{StarredOnly: true}, rss.ArticleCursor{}, 1)
		if err != nil {
			t.Fatalf("ListStarred: %v", err)
		}
		if len(items) != 1 || items[0].Feed.Title != "My C" {
			t.Errorf("got %+v, want C titled My C", items)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestListStarredConditions(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewStarRepository(db)
	user := newTestUser(t, db)
	_, articleID, tag := newConditionArticles(t, db, user)

	// Star them all, newest article starred last; C-text stays unstarred.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	starred := []string{"C-audio", "A-text", "A-audio", "B-text", "B-audio"}
	for i, ext := range starred {
		if _, err := db.Exec(`INSERT INTO stars (user_id, article_id, starred_at) VALUES ($1, $2, $3)`,
			user, articleID[ext], base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("insert star: %v", err)
		}
	}
	extOf := map[string]string{}
	for ext, id := range articleID {
		extOf[id] = ext
	}

	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   []string
	}{
		{"any posting rate", rss.TimelineFilter{StarredOnly: true},
			[]string{"B-audio", "B-text", "A-audio", "A-text", "C-audio"}},
		{"at most 2 posts a week", rss.TimelineFilter{StarredOnly: true, MaxPostsPerWeek: 2},
			[]string{"A-audio", "A-text", "C-audio"}},
		{"at most 2 posts a week with a tag", rss.TimelineFilter{StarredOnly: true, MaxPostsPerWeek: 2, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"A-audio", "A-text"}},
		{"audio or video", rss.TimelineFilter{StarredOnly: true, MediaOnly: true},
			[]string{"B-audio", "A-audio", "C-audio"}},
		{"audio or video with a tag", rss.TimelineFilter{StarredOnly: true, MediaOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{tag.ID}},
			[]string{"B-audio", "A-audio"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := repo.ListStarred(t.Context(), user, tt.filter, rss.ArticleCursor{}, 100)
			if err != nil {
				t.Fatalf("ListStarred: %v", err)
			}
			got := []string{}
			for _, item := range items {
				got = append(got, extOf[item.Article.ID])
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
