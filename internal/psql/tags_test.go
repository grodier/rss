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

// subscriptionTagNames returns the names of the tags on userID's
// subscription to feedID, sorted.
func subscriptionTagNames(t *testing.T, db *sql.DB, userID, feedID string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT t.name FROM subscription_tags st JOIN tags t ON t.id = st.tag_id
		WHERE st.user_id = $1 AND st.feed_id = $2 ORDER BY t.name`, userID, feedID)
	if err != nil {
		t.Fatalf("select subscription_tags: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return names
}

func tagCount(t *testing.T, db *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tags WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestSetForSubscription(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("sets the ticked tags", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		ai := mustCreateTag(t, repo, user, "AI")
		news := mustCreateTag(t, repo, user, "News")
		mustCreateTag(t, repo, user, "Sports")
		mustSetFeeds(t, repo, user, news.ID, feeds[1])

		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{ai.ID, news.ID}, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"AI", "News"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
		// Other subscriptions keep theirs.
		if got, want := subscriptionTagNames(t, db, user, feeds[1]), []string{"News"}; !slices.Equal(got, want) {
			t.Errorf("other subscription's tags = %v, want %v", got, want)
		}
	})

	t.Run("creates new names and reuses existing ones ignoring case", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		ai := mustCreateTag(t, repo, user, "AI")

		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{ai.ID}, []string{"Cooking", "ai"}); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"AI", "Cooking"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
		if got := tagCount(t, db, user); got != 2 {
			t.Errorf("tags = %d, want 2", got)
		}
	})

	t.Run("replaces the tags, and an empty set removes them all", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		ai := mustCreateTag(t, repo, user, "AI")
		news := mustCreateTag(t, repo, user, "News")
		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{ai.ID, news.ID}, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}

		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{news.ID}, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"News"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}

		if err := repo.SetForSubscription(ctx, user, feeds[0], nil, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}
		if got := subscriptionTagNames(t, db, user, feeds[0]); len(got) != 0 {
			t.Errorf("tags = %v, want none", got)
		}
		// The tags themselves stay.
		if got := tagCount(t, db, user); got != 2 {
			t.Errorf("tags = %d, want 2", got)
		}
	})

	t.Run("another user's tag ID", func(t *testing.T) {
		owner := newTestUser(t, db)
		theirs := mustCreateTag(t, repo, owner, "AI")
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		mine := mustCreateTag(t, repo, user, "News")
		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{mine.ID}, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}

		err := repo.SetForSubscription(ctx, user, feeds[0], []string{theirs.ID}, []string{"Cooking"})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		// The transaction is rolled back: the earlier state stays.
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"News"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
		if got := tagCount(t, db, user); got != 1 {
			t.Errorf("tags = %d, want 1", got)
		}
	})

	t.Run("not subscribed", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db).ID
		ai := mustCreateTag(t, repo, user, "AI")

		for _, ids := range [][]string{nil, {ai.ID}} {
			if err := repo.SetForSubscription(ctx, user, feed, ids, nil); !errors.Is(err, rss.ErrNoRecord) {
				t.Errorf("SetForSubscription(%v): err = %v, want %v", ids, err, rss.ErrNoRecord)
			}
		}
		if err := repo.SetForSubscription(ctx, user, feed, nil, []string{"Cooking"}); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SetForSubscription with a new name: err = %v, want %v", err, rss.ErrNoRecord)
		}
		if got := tagCount(t, db, user); got != 1 {
			t.Errorf("tags = %d, want 1", got)
		}
	})

	t.Run("passing MaxTags changes nothing", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		var first rss.Tag
		for i := range MaxTags - 1 {
			tag := mustCreateTag(t, repo, user, fmt.Sprintf("Tag %d", i))
			if i == 0 {
				first = tag
			}
		}
		if err := repo.SetForSubscription(ctx, user, feeds[0], []string{first.ID}, nil); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}

		// One new name (plus an existing one) reaches MaxTags exactly.
		err := repo.SetForSubscription(ctx, user, feeds[0], nil, []string{"New 1", "tag 1"})
		if err != nil {
			t.Fatalf("SetForSubscription to MaxTags: %v", err)
		}
		if got := tagCount(t, db, user); got != MaxTags {
			t.Errorf("tags = %d, want %d", got, MaxTags)
		}

		err = repo.SetForSubscription(ctx, user, feeds[0], []string{first.ID}, []string{"New 2"})
		if !errors.Is(err, rss.ErrTooManyTags) {
			t.Errorf("err = %v, want %v", err, rss.ErrTooManyTags)
		}
		if got := tagCount(t, db, user); got != MaxTags {
			t.Errorf("tags = %d, want %d", got, MaxTags)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"New 1", "Tag 1"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
	})
}

func TestAddToSubscription(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTagRepository(db)
	ctx := t.Context()

	t.Run("adds new and existing tags, keeping those on it", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		ai := mustCreateTag(t, repo, user, "AI")
		news := mustCreateTag(t, repo, user, "News")
		mustSetFeeds(t, repo, user, news.ID, feeds[0], feeds[1])

		if err := repo.AddToSubscription(ctx, user, feeds[0], []string{"ai", "Cooking", "news"}); err != nil {
			t.Fatalf("AddToSubscription: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"AI", "Cooking", "News"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
		if got := tagCount(t, db, user); got != 3 {
			t.Errorf("tags = %d, want 3", got)
		}
		if got := tagFeedIDs(t, db, ai.ID); !slices.Equal(got, []string{feeds[0]}) {
			t.Errorf("AI's feeds = %v, want %v", got, []string{feeds[0]})
		}
		// Other subscriptions keep theirs.
		if got, want := subscriptionTagNames(t, db, user, feeds[1]), []string{"News"}; !slices.Equal(got, want) {
			t.Errorf("other subscription's tags = %v, want %v", got, want)
		}

		// Again: nothing duplicates.
		if err := repo.AddToSubscription(ctx, user, feeds[0], []string{"Cooking"}); err != nil {
			t.Fatalf("AddToSubscription again: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"AI", "Cooking", "News"}; !slices.Equal(got, want) {
			t.Errorf("tags after the second call = %v, want %v", got, want)
		}
		if got := tagCount(t, db, user); got != 3 {
			t.Errorf("tags after the second call = %d, want 3", got)
		}
	})

	t.Run("no names changes nothing", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		news := mustCreateTag(t, repo, user, "News")
		mustSetFeeds(t, repo, user, news.ID, feeds[0])

		if err := repo.AddToSubscription(ctx, user, feeds[0], nil); err != nil {
			t.Fatalf("AddToSubscription: %v", err)
		}
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"News"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
	})

	t.Run("new names past MaxTags are skipped", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 1)
		for i := range MaxTags - 1 {
			mustCreateTag(t, repo, user, fmt.Sprintf("Tag %d", i))
		}

		err := repo.AddToSubscription(ctx, user, feeds[0], []string{"First new", "tag 5", "Second new", "Third new"})
		if err != nil {
			t.Fatalf("AddToSubscription: %v", err)
		}
		// The first new one fits; an existing one is still added.
		if got, want := subscriptionTagNames(t, db, user, feeds[0]), []string{"First new", "Tag 5"}; !slices.Equal(got, want) {
			t.Errorf("tags = %v, want %v", got, want)
		}
		if got := tagCount(t, db, user); got != MaxTags {
			t.Errorf("tags = %d, want %d", got, MaxTags)
		}
	})

	t.Run("not subscribed", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db).ID

		if err := repo.AddToSubscription(ctx, user, feed, []string{"Cooking"}); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		if got := tagCount(t, db, user); got != 0 {
			t.Errorf("tags = %d, want 0", got)
		}
	})
}
