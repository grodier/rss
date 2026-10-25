package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func mustCreateTag(t *testing.T, repo *TagRepository, userID, name string) rss.Tag {
	t.Helper()
	tag, err := repo.Create(t.Context(), userID, name)
	if err != nil {
		t.Fatalf("Create %q: %v", name, err)
	}
	return tag
}

// newTestSubscriptions subscribes userID to n new feeds and returns their IDs.
func newTestSubscriptions(t *testing.T, db *sql.DB, userID string, n int) []string {
	t.Helper()
	subs := NewSubscriptionRepository(db)
	ids := make([]string, n)
	for i := range ids {
		ids[i] = newTestFeed(t, db).ID
		if err := subs.Subscribe(t.Context(), userID, ids[i]); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	return ids
}

func mustSetFeeds(t *testing.T, repo *TagRepository, userID, tagID string, feedIDs ...string) {
	t.Helper()
	if err := repo.SetFeeds(t.Context(), userID, tagID, feedIDs); err != nil {
		t.Fatalf("SetFeeds: %v", err)
	}
}

// tagFeedIDs returns the sorted feeds that have tagID in subscription_tags.
func tagFeedIDs(t *testing.T, db *sql.DB, tagID string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT feed_id FROM subscription_tags WHERE tag_id = $1 ORDER BY feed_id`, tagID)
	if err != nil {
		t.Fatalf("select subscription_tags: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return ids
}

func sorted(ids ...string) []string {
	return slices.Sorted(slices.Values(ids))
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagCreate(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("creates a tag", func(t *testing.T) {
		user := newTestUser(t, db)
		tag := mustCreateTag(t, repo, user, "AI")
		if tag.ID == "" || tag.Name != "AI" {
			t.Errorf("Create = %+v, want an ID and the name AI", tag)
		}
		got, err := repo.Get(ctx, user, tag.ID)
		if err != nil || got != tag {
			t.Errorf("Get = %+v, %v; want %+v", got, err, tag)
		}
	})

	t.Run("a name differing only in case is a duplicate", func(t *testing.T) {
		user := newTestUser(t, db)
		mustCreateTag(t, repo, user, "AI")
		if _, err := repo.Create(ctx, user, "ai"); !errors.Is(err, rss.ErrDuplicateTag) {
			t.Errorf("err = %v, want %v", err, rss.ErrDuplicateTag)
		}
	})

	t.Run("another user can have the same name", func(t *testing.T) {
		mustCreateTag(t, repo, newTestUser(t, db), "AI")
		mustCreateTag(t, repo, newTestUser(t, db), "AI")
	})

	t.Run("at most MaxTags", func(t *testing.T) {
		user := newTestUser(t, db)
		for i := range MaxTags {
			mustCreateTag(t, repo, user, fmt.Sprintf("Tag %d", i))
		}
		if _, err := repo.Create(ctx, user, "One too many"); !errors.Is(err, rss.ErrTooManyTags) {
			t.Errorf("err = %v, want %v", err, rss.ErrTooManyTags)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	user := newTestUser(t, db)
	other := newTestUser(t, db)
	feeds := newTestSubscriptions(t, db, user, 3)

	news := mustCreateTag(t, repo, user, "news")
	ai := mustCreateTag(t, repo, user, "AI")
	empty := mustCreateTag(t, repo, user, "Machine learning")
	mustCreateTag(t, repo, other, "Other's")
	mustSetFeeds(t, repo, user, ai.ID, feeds...)
	mustSetFeeds(t, repo, user, news.ID, feeds[0])

	got, err := repo.List(t.Context(), user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ai.Feeds, news.Feeds = 3, 1
	want := []rss.Tag{ai, empty, news}
	if !slices.Equal(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}

	t.Run("no tags", func(t *testing.T) {
		got, err := repo.List(t.Context(), newTestUser(t, db))
		if err != nil || len(got) != 0 {
			t.Errorf("List = %+v, %v; want none", got, err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagGet(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	user := newTestUser(t, db)
	tag := mustCreateTag(t, repo, user, "AI")

	t.Run("another user's tag", func(t *testing.T) {
		if _, err := repo.Get(t.Context(), newTestUser(t, db), tag.ID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("unknown tag", func(t *testing.T) {
		if _, err := repo.Get(t.Context(), user, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagRename(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("renames", func(t *testing.T) {
		user := newTestUser(t, db)
		tag := mustCreateTag(t, repo, user, "AI")
		if err := repo.Rename(ctx, user, tag.ID, "Artificial intelligence"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		got, err := repo.Get(ctx, user, tag.ID)
		if err != nil || got.Name != "Artificial intelligence" {
			t.Errorf("Get = %+v, %v; want the new name", got, err)
		}
	})

	t.Run("changing only the case", func(t *testing.T) {
		user := newTestUser(t, db)
		tag := mustCreateTag(t, repo, user, "ai")
		if err := repo.Rename(ctx, user, tag.ID, "AI"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
	})

	t.Run("another of the user's tags has the name", func(t *testing.T) {
		user := newTestUser(t, db)
		mustCreateTag(t, repo, user, "AI")
		tag := mustCreateTag(t, repo, user, "News")
		if err := repo.Rename(ctx, user, tag.ID, "ai"); !errors.Is(err, rss.ErrDuplicateTag) {
			t.Errorf("err = %v, want %v", err, rss.ErrDuplicateTag)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		owner := newTestUser(t, db)
		tag := mustCreateTag(t, repo, owner, "AI")
		if err := repo.Rename(ctx, newTestUser(t, db), tag.ID, "Mine"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		if got, _ := repo.Get(ctx, owner, tag.ID); got.Name != "AI" {
			t.Errorf("name = %q, want AI", got.Name)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagDelete(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("deletes it and its subscription_tags rows", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		tag := mustCreateTag(t, repo, user, "AI")
		mustSetFeeds(t, repo, user, tag.ID, feeds...)

		if err := repo.Delete(ctx, user, tag.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repo.Get(ctx, user, tag.ID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Get after Delete: err = %v, want %v", err, rss.ErrNoRecord)
		}
		if got := tagFeedIDs(t, db, tag.ID); len(got) != 0 {
			t.Errorf("subscription_tags = %v, want none", got)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		owner := newTestUser(t, db)
		tag := mustCreateTag(t, repo, owner, "AI")
		if err := repo.Delete(ctx, newTestUser(t, db), tag.ID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		if _, err := repo.Get(ctx, owner, tag.ID); err != nil {
			t.Errorf("Get: %v; the tag should still exist", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagFeedIDs(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	user := newTestUser(t, db)
	feeds := newTestSubscriptions(t, db, user, 3)
	tag := mustCreateTag(t, repo, user, "AI")
	otherTag := mustCreateTag(t, repo, user, "News")
	mustSetFeeds(t, repo, user, tag.ID, feeds[0], feeds[1])
	mustSetFeeds(t, repo, user, otherTag.ID, feeds[2])

	got, err := repo.FeedIDs(t.Context(), user, tag.ID)
	if err != nil {
		t.Fatalf("FeedIDs: %v", err)
	}
	if want := map[string]bool{feeds[0]: true, feeds[1]: true}; !maps.Equal(got, want) {
		t.Errorf("FeedIDs = %v, want %v", got, want)
	}

	t.Run("another user's tag", func(t *testing.T) {
		got, err := repo.FeedIDs(t.Context(), newTestUser(t, db), tag.ID)
		if err != nil || len(got) != 0 {
			t.Errorf("FeedIDs = %v, %v; want none", got, err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTagSetFeeds(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("adds and removes in one call", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 3)
		tag := mustCreateTag(t, repo, user, "AI")
		mustSetFeeds(t, repo, user, tag.ID, feeds[0], feeds[1])

		mustSetFeeds(t, repo, user, tag.ID, feeds[1], feeds[2])
		if got, want := tagFeedIDs(t, db, tag.ID), sorted(feeds[1], feeds[2]); !slices.Equal(got, want) {
			t.Errorf("tagged = %v, want %v", got, want)
		}
	})

	t.Run("a feed listed twice", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		tag := mustCreateTag(t, repo, user, "AI")

		mustSetFeeds(t, repo, user, tag.ID, feeds[0], feeds[0])
		if got := tagFeedIDs(t, db, tag.ID); !slices.Equal(got, feeds) {
			t.Errorf("tagged = %v, want %v", got, feeds)
		}
	})

	t.Run("leaves the subscriptions' other tags alone", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		tag := mustCreateTag(t, repo, user, "AI")
		other := mustCreateTag(t, repo, user, "News")
		mustSetFeeds(t, repo, user, other.ID, feeds...)

		mustSetFeeds(t, repo, user, tag.ID, feeds[0])
		if got, want := tagFeedIDs(t, db, other.ID), sorted(feeds...); !slices.Equal(got, want) {
			t.Errorf("other tag's feeds = %v, want %v", got, want)
		}
	})

	t.Run("an empty list removes the tag from all", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		tag := mustCreateTag(t, repo, user, "AI")
		mustSetFeeds(t, repo, user, tag.ID, feeds...)

		mustSetFeeds(t, repo, user, tag.ID)
		if got := tagFeedIDs(t, db, tag.ID); len(got) != 0 {
			t.Errorf("tagged = %v, want none", got)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		owner := newTestUser(t, db)
		tag := mustCreateTag(t, repo, owner, "AI")
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)

		for _, ids := range [][]string{nil, feeds} {
			if err := repo.SetFeeds(ctx, user, tag.ID, ids); !errors.Is(err, rss.ErrNoRecord) {
				t.Errorf("SetFeeds(%v): err = %v, want %v", ids, err, rss.ErrNoRecord)
			}
		}
		if got := tagFeedIDs(t, db, tag.ID); len(got) != 0 {
			t.Errorf("tagged = %v, want none", got)
		}
	})

	t.Run("a feed the user doesn't subscribe to", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		tag := mustCreateTag(t, repo, user, "AI")
		mustSetFeeds(t, repo, user, tag.ID, feeds[0])
		unsubscribed := newTestFeed(t, db).ID

		err := repo.SetFeeds(ctx, user, tag.ID, []string{feeds[1], unsubscribed})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		// The transaction is rolled back: the earlier state stays.
		if got := tagFeedIDs(t, db, tag.ID); !slices.Equal(got, feeds[:1]) {
			t.Errorf("tagged = %v, want %v", got, feeds[:1])
		}
	})
}
