package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func mustCreateTimeline(t *testing.T, repo *TimelineRepository, userID string, tl rss.Timeline) string {
	t.Helper()
	id, err := repo.Create(t.Context(), userID, tl)
	if err != nil {
		t.Fatalf("Create %q: %v", tl.Name, err)
	}
	return id
}

func mustGetTimeline(t *testing.T, repo *TimelineRepository, userID, id string) rss.Timeline {
	t.Helper()
	tl, err := repo.Get(t.Context(), userID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return tl
}

// sortedFilter returns f with each list sorted, and nil for empty lists, so
// filters compare by content.
func sortedFilter(f rss.TimelineFilter) rss.TimelineFilter {
	s := func(ids []string) []string {
		if len(ids) == 0 {
			return nil
		}
		return slices.Sorted(slices.Values(ids))
	}
	return rss.TimelineFilter{
		OnlyIncluded:   f.OnlyIncluded,
		IncludeTagIDs:  s(f.IncludeTagIDs),
		IncludeFeedIDs: s(f.IncludeFeedIDs),
		ExcludeTagIDs:  s(f.ExcludeTagIDs),
		ExcludeFeedIDs: s(f.ExcludeFeedIDs),
		StarredOnly:    f.StarredOnly,
	}
}

func assertFilter(t *testing.T, got, want rss.TimelineFilter) {
	t.Helper()
	got, want = sortedFilter(got), sortedFilter(want)
	if got.OnlyIncluded != want.OnlyIncluded || got.StarredOnly != want.StarredOnly ||
		!slices.Equal(got.IncludeTagIDs, want.IncludeTagIDs) || !slices.Equal(got.IncludeFeedIDs, want.IncludeFeedIDs) ||
		!slices.Equal(got.ExcludeTagIDs, want.ExcludeTagIDs) || !slices.Equal(got.ExcludeFeedIDs, want.ExcludeFeedIDs) {
		t.Errorf("filter = %+v, want %+v", got, want)
	}
}

// newHomeTimeline inserts userID's Home row, as muting will (#362).
func newHomeTimeline(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`INSERT INTO timelines (user_id, home) VALUES ($1, true) RETURNING id`, userID).Scan(&id); err != nil {
		t.Fatalf("insert Home: %v", err)
	}
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineCreate(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	t.Run("positions increase and sources round-trip", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 3)
		ai, sports := mustCreateTag(t, tags, user, "AI"), mustCreateTag(t, tags, user, "Sports")

		filter := rss.TimelineFilter{
			OnlyIncluded:   true,
			IncludeTagIDs:  []string{ai.ID},
			IncludeFeedIDs: []string{feeds[0], feeds[1]},
			ExcludeTagIDs:  []string{sports.ID},
			ExcludeFeedIDs: []string{feeds[2]},
		}
		first := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "AI", Description: "Models", Filter: filter})
		second := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "All"})

		got := mustGetTimeline(t, repo, user, first)
		if got.ID != first || got.Home || got.Name != "AI" || got.Description != "Models" || got.ShareToken != "" || got.CreatedAt.IsZero() {
			t.Errorf("Get = %+v", got)
		}
		assertFilter(t, got.Filter, filter)

		all := mustGetTimeline(t, repo, user, second)
		assertFilter(t, all.Filter, rss.TimelineFilter{})
		if got.Position < 1 || all.Position != got.Position+1 {
			t.Errorf("positions = %d, %d; want increasing from 1", got.Position, all.Position)
		}
	})

	t.Run("an ID both included and excluded is stored as excluded", func(t *testing.T) {
		user := newTestUser(t, db)
		feeds := newTestSubscriptions(t, db, user, 2)
		ai := mustCreateTag(t, tags, user, "AI")
		id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Both", Filter: rss.TimelineFilter{
			OnlyIncluded:   true,
			IncludeTagIDs:  []string{ai.ID},
			IncludeFeedIDs: []string{feeds[0], feeds[1]},
			ExcludeTagIDs:  []string{ai.ID},
			ExcludeFeedIDs: []string{feeds[1]},
		}})
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{
			OnlyIncluded:   true,
			IncludeFeedIDs: []string{feeds[0]},
			ExcludeTagIDs:  []string{ai.ID},
			ExcludeFeedIDs: []string{feeds[1]},
		})
	})

	t.Run("starred_only round-trips", func(t *testing.T) {
		user := newTestUser(t, db)
		id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Starred", Filter: rss.TimelineFilter{StarredOnly: true}})
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{StarredOnly: true})

		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Everything"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{})

		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Starred", Filter: rss.TimelineFilter{StarredOnly: true}}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{StarredOnly: true})
	})

	t.Run("the 21st is ErrTooManyTimelines", func(t *testing.T) {
		user := newTestUser(t, db)
		// Home doesn't count.
		newHomeTimeline(t, db, user)
		for i := range MaxTimelines {
			mustCreateTimeline(t, repo, user, rss.Timeline{Name: fmt.Sprintf("T%d", i)})
		}
		if _, err := repo.Create(ctx, user, rss.Timeline{Name: "One more"}); !errors.Is(err, rss.ErrTooManyTimelines) {
			t.Errorf("err = %v, want ErrTooManyTimelines", err)
		}
	})

	t.Run("another user's tag is ErrNoRecord", func(t *testing.T) {
		user, other := newTestUser(t, db), newTestUser(t, db)
		theirs := mustCreateTag(t, tags, other, "AI")
		_, err := repo.Create(ctx, user, rss.Timeline{Name: "X", Filter: rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{theirs.ID}}})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
		if list, _ := repo.List(ctx, user); len(list) != 0 {
			t.Errorf("timeline stored anyway: %+v", list)
		}
	})

	t.Run("a feed the user doesn't subscribe to is ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)
		_, err := repo.Create(ctx, user, rss.Timeline{Name: "X", Filter: rss.TimelineFilter{ExcludeFeedIDs: []string{feed.ID}}})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineGet(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	ctx := t.Context()

	user, other := newTestUser(t, db), newTestUser(t, db)
	id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Mine"})

	t.Run("another user's is ErrNoRecord", func(t *testing.T) {
		if _, err := repo.Get(ctx, other, id); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("Home is returned as Home", func(t *testing.T) {
		home := newHomeTimeline(t, db, user)
		if got := mustGetTimeline(t, repo, user, home); !got.Home {
			t.Errorf("Get(Home) = %+v, want Home", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineUpdate(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	user, other := newTestUser(t, db), newTestUser(t, db)
	feeds := newTestSubscriptions(t, db, user, 2)
	ai, news := mustCreateTag(t, tags, user, "AI"), mustCreateTag(t, tags, user, "News")
	id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Old", Filter: rss.TimelineFilter{
		OnlyIncluded:   true,
		IncludeTagIDs:  []string{ai.ID},
		ExcludeFeedIDs: []string{feeds[0]},
	}})

	t.Run("replaces name, description and sources", func(t *testing.T) {
		filter := rss.TimelineFilter{IncludeFeedIDs: nil, ExcludeTagIDs: []string{news.ID}, ExcludeFeedIDs: []string{feeds[1]}}
		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "New", Description: "Now all", Filter: filter}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got := mustGetTimeline(t, repo, user, id)
		if got.Name != "New" || got.Description != "Now all" || !got.UpdatedAt.After(got.CreatedAt) {
			t.Errorf("Get = %+v", got)
		}
		assertFilter(t, got.Filter, filter)
	})

	t.Run("another user's is ErrNoRecord and unchanged", func(t *testing.T) {
		if err := repo.Update(ctx, other, rss.Timeline{ID: id, Name: "Theirs"}); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
		if got := mustGetTimeline(t, repo, user, id); got.Name != "New" {
			t.Errorf("name = %q, want New", got.Name)
		}
	})

	t.Run("Home is ErrNoRecord", func(t *testing.T) {
		home := newHomeTimeline(t, db, user)
		if err := repo.Update(ctx, user, rss.Timeline{ID: home, Name: "Home"}); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("a foreign source is ErrNoRecord and nothing changes", func(t *testing.T) {
		theirs := mustCreateTag(t, tags, other, "Theirs")
		err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Bad", Filter: rss.TimelineFilter{ExcludeTagIDs: []string{theirs.ID}}})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
		got := mustGetTimeline(t, repo, user, id)
		if got.Name != "New" {
			t.Errorf("name = %q, want New", got.Name)
		}
		assertFilter(t, got.Filter, rss.TimelineFilter{ExcludeTagIDs: []string{news.ID}, ExcludeFeedIDs: []string{feeds[1]}})
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	ctx := t.Context()

	user, other := newTestUser(t, db), newTestUser(t, db)
	newHomeTimeline(t, db, user)
	b := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "B", Description: "Bee"})
	a := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "A"})
	c := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "C"})
	mustCreateTimeline(t, repo, other, rss.Timeline{Name: "Theirs"})
	// Equal positions fall back to created_at.
	if _, err := db.Exec(`UPDATE timelines SET position = 0 WHERE id = $1`, c); err != nil {
		t.Fatalf("set position: %v", err)
	}

	got, err := repo.List(ctx, user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var ids []string
	for _, tl := range got {
		ids = append(ids, tl.ID)
	}
	if want := []string{c, b, a}; !slices.Equal(ids, want) {
		t.Errorf("List IDs = %v, want %v (by position, no Home, no other user's)", ids, want)
	}
	if got[1].Name != "B" || got[1].Description != "Bee" {
		t.Errorf("List[1] = %+v", got[1])
	}

	t.Run("none is empty", func(t *testing.T) {
		got, err := repo.List(ctx, newTestUser(t, db))
		if err != nil || len(got) != 0 {
			t.Errorf("List = %v, %v; want none", got, err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineDelete(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	user, other := newTestUser(t, db), newTestUser(t, db)
	feeds := newTestSubscriptions(t, db, user, 1)
	ai := mustCreateTag(t, tags, user, "AI")
	id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "X", Filter: rss.TimelineFilter{
		OnlyIncluded: true, IncludeTagIDs: []string{ai.ID}, IncludeFeedIDs: feeds,
	}})

	if err := repo.Delete(ctx, other, id); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Delete(other) = %v, want ErrNoRecord", err)
	}
	if err := repo.Delete(ctx, user, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, user, id); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Get after Delete = %v, want ErrNoRecord", err)
	}
	var n int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM timeline_tags WHERE timeline_id = $1) + (SELECT count(*) FROM timeline_feeds WHERE timeline_id = $1)`, id).Scan(&n); err != nil || n != 0 {
		t.Errorf("sources left = %d (%v), want 0", n, err)
	}
	if err := repo.Delete(ctx, user, id); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("second Delete = %v, want ErrNoRecord", err)
	}

	t.Run("Home is ErrNoRecord", func(t *testing.T) {
		home := newHomeTimeline(t, db, user)
		if err := repo.Delete(ctx, user, home); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineCascades(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	tags := NewTagRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	user := newTestUser(t, db)
	feeds := newTestSubscriptions(t, db, user, 2)
	ai, news := mustCreateTag(t, tags, user, "AI"), mustCreateTag(t, tags, user, "News")
	id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "X", Filter: rss.TimelineFilter{
		OnlyIncluded:   true,
		IncludeTagIDs:  []string{ai.ID},
		IncludeFeedIDs: []string{feeds[0]},
		ExcludeTagIDs:  []string{news.ID},
		ExcludeFeedIDs: []string{feeds[1]},
	}})

	if err := tags.Delete(ctx, user, ai.ID); err != nil {
		t.Fatalf("Delete tag: %v", err)
	}
	if err := subs.Unsubscribe(ctx, user, feeds[1]); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{
		OnlyIncluded:   true,
		IncludeFeedIDs: []string{feeds[0]},
		ExcludeTagIDs:  []string{news.ID},
	})
}
