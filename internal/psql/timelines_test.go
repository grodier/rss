package psql

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/lib/pq"
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
		OnlyIncluded:    f.OnlyIncluded,
		IncludeTagIDs:   s(f.IncludeTagIDs),
		IncludeFeedIDs:  s(f.IncludeFeedIDs),
		ExcludeTagIDs:   s(f.ExcludeTagIDs),
		ExcludeFeedIDs:  s(f.ExcludeFeedIDs),
		StarredOnly:     f.StarredOnly,
		MaxPostsPerWeek: f.MaxPostsPerWeek,
		MediaOnly:       f.MediaOnly,
		IncludeWords:    s(f.IncludeWords),
		ExcludeWords:    s(f.ExcludeWords),
	}
}

func assertFilter(t *testing.T, got, want rss.TimelineFilter) {
	t.Helper()
	got, want = sortedFilter(got), sortedFilter(want)
	if got.OnlyIncluded != want.OnlyIncluded || got.StarredOnly != want.StarredOnly ||
		got.MaxPostsPerWeek != want.MaxPostsPerWeek || got.MediaOnly != want.MediaOnly ||
		!slices.Equal(got.IncludeTagIDs, want.IncludeTagIDs) || !slices.Equal(got.IncludeFeedIDs, want.IncludeFeedIDs) ||
		!slices.Equal(got.ExcludeTagIDs, want.ExcludeTagIDs) || !slices.Equal(got.ExcludeFeedIDs, want.ExcludeFeedIDs) ||
		!slices.Equal(got.IncludeWords, want.IncludeWords) || !slices.Equal(got.ExcludeWords, want.ExcludeWords) {
		t.Errorf("filter = %+v, want %+v", got, want)
	}
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
		if got.ID != first || got.Name != "AI" || got.Description != "Models" || got.ShareToken != "" || got.CreatedAt.IsZero() {
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

	t.Run("max_posts_per_week and media_only round-trip", func(t *testing.T) {
		user := newTestUser(t, db)
		quiet := rss.TimelineFilter{MaxPostsPerWeek: 2, MediaOnly: true}
		id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Quiet podcasts", Filter: quiet})
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, quiet)

		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Everything"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{})

		busy := rss.TimelineFilter{MaxPostsPerWeek: 100}
		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Busy", Filter: busy}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, busy)
	})

	t.Run("include_words and exclude_words round-trip", func(t *testing.T) {
		user := newTestUser(t, db)
		ai := rss.TimelineFilter{IncludeWords: []string{"AI", "C++", "machine learning"}, ExcludeWords: []string{"Sponsored"}}
		id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "AI", Filter: ai})
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, ai)

		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "Everything"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, rss.TimelineFilter{})

		deals := rss.TimelineFilter{ExcludeWords: []string{"deal", "Café"}}
		if err := repo.Update(ctx, user, rss.Timeline{ID: id, Name: "No deals", Filter: deals}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertFilter(t, mustGetTimeline(t, repo, user, id).Filter, deals)
	})

	t.Run("the 21st is ErrTooManyTimelines", func(t *testing.T) {
		user := newTestUser(t, db)
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
		t.Errorf("List IDs = %v, want %v (by position, no other user's)", ids, want)
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
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineMove(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	ctx := t.Context()

	// order returns userID's timeline IDs in tab order, and fails unless
	// their positions are 0…n−1.
	order := func(t *testing.T, userID string) []string {
		t.Helper()
		rows, err := db.Query(`SELECT id, position FROM timelines WHERE user_id = $1 ORDER BY position, created_at`, userID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			var pos int
			if err := rows.Scan(&id, &pos); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if pos != len(ids) {
				t.Errorf("timeline %d has position %d", len(ids), pos)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return ids
	}
	move := func(t *testing.T, userID, id string, up bool) {
		t.Helper()
		if err := repo.Move(ctx, userID, id, up); err != nil {
			t.Fatalf("Move(%s, up=%v): %v", id, up, err)
		}
	}

	user, other := newTestUser(t, db), newTestUser(t, db)
	a := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "A"})
	b := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "B"})
	c := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "C"})
	theirs1 := mustCreateTimeline(t, repo, other, rss.Timeline{Name: "Theirs 1"})
	theirs2 := mustCreateTimeline(t, repo, other, rss.Timeline{Name: "Theirs 2"})
	// Ties and gaps: A and B share a position (created_at breaks the tie), C
	// is far after them.
	if _, err := db.Exec(`UPDATE timelines SET position = CASE id WHEN $1 THEN 5 WHEN $2 THEN 5 ELSE 9 END WHERE id IN ($1, $2, $3)`, a, b, c); err != nil {
		t.Fatalf("set positions: %v", err)
	}
	var theirPositions []int64
	if err := db.QueryRow(`SELECT array_agg(position ORDER BY created_at) FROM timelines WHERE user_id = $1`, other).Scan(pq.Array(&theirPositions)); err != nil {
		t.Fatalf("their positions: %v", err)
	}

	steps := []struct {
		name string
		id   string
		up   bool
		want []string
	}{
		{"down swaps with the next", a, false, []string{b, a, c}},
		{"up swaps with the previous", c, true, []string{b, c, a}},
		{"up on the first does nothing", b, true, []string{b, c, a}},
		{"down on the last does nothing", a, false, []string{b, c, a}},
		{"down from the middle", c, false, []string{b, a, c}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			move(t, user, st.id, st.up)
			if got := order(t, user); !slices.Equal(got, st.want) {
				t.Errorf("order = %v, want %v", got, st.want)
			}
		})
	}

	t.Run("another user's timeline is ErrNoRecord", func(t *testing.T) {
		if err := repo.Move(ctx, user, theirs1, false); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Move(their timeline) = %v, want ErrNoRecord", err)
		}
		if err := repo.Move(ctx, other, a, true); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Move(as other) = %v, want ErrNoRecord", err)
		}
		if got := order(t, user); !slices.Equal(got, []string{b, a, c}) {
			t.Errorf("user's order = %v, changed", got)
		}
		var got []int64
		var ids []string
		if err := db.QueryRow(`SELECT array_agg(position ORDER BY created_at), array_agg(id::text ORDER BY created_at) FROM timelines WHERE user_id = $1`, other).Scan(pq.Array(&got), pq.Array(&ids)); err != nil {
			t.Fatalf("their positions: %v", err)
		}
		if !slices.Equal(got, theirPositions) || !slices.Equal(ids, []string{theirs1, theirs2}) {
			t.Errorf("their positions = %v (%v), want %v untouched", got, ids, theirPositions)
		}
	})

	t.Run("unknown timeline is ErrNoRecord", func(t *testing.T) {
		if err := repo.Move(ctx, user, "00000000-0000-4000-8000-000000000000", true); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Move = %v, want ErrNoRecord", err)
		}
	})
}

// shareToken returns a unique 43-character token for a test.
func shareToken(t *testing.T) string {
	t.Helper()
	tok, err := token.Random()
	if err != nil {
		t.Fatalf("token.Random: %v", err)
	}
	return tok
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineSharing(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewTimelineRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	user, other := newTestUser(t, db), newTestUser(t, db)
	ai := mustCreateTag(t, tags, user, "AI")
	filter := rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{ai.ID}, StarredOnly: true}
	id := mustCreateTimeline(t, repo, user, rss.Timeline{Name: "Picks", Filter: filter})

	first := shareToken(t)
	t.Run("Share sets a token once", func(t *testing.T) {
		got, err := repo.Share(ctx, user, id, first)
		if err != nil || got != first {
			t.Fatalf("Share = %q, %v; want %q", got, err, first)
		}
		got, err = repo.Share(ctx, user, id, shareToken(t))
		if err != nil || got != first {
			t.Errorf("second Share = %q, %v; want the first token %q", got, err, first)
		}
		if tl := mustGetTimeline(t, repo, user, id); tl.ShareToken != first {
			t.Errorf("Get ShareToken = %q, want %q", tl.ShareToken, first)
		}
	})

	t.Run("GetShared finds it with its filter and owner", func(t *testing.T) {
		tl, owner, err := repo.GetShared(ctx, first)
		if err != nil {
			t.Fatalf("GetShared: %v", err)
		}
		if tl.ID != id || tl.Name != "Picks" || owner != user {
			t.Errorf("GetShared = %+v, %q; want %s owned by %s", tl, owner, id, user)
		}
		assertFilter(t, tl.Filter, filter)
	})

	t.Run("an unknown token is ErrNoRecord", func(t *testing.T) {
		if _, _, err := repo.GetShared(ctx, shareToken(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("another user can't share, replace or unshare it", func(t *testing.T) {
		if _, err := repo.Share(ctx, other, id, shareToken(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Share(other) = %v, want ErrNoRecord", err)
		}
		if err := repo.NewShareLink(ctx, other, id, shareToken(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("NewShareLink(other) = %v, want ErrNoRecord", err)
		}
		if err := repo.Unshare(ctx, other, id); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Unshare(other) = %v, want ErrNoRecord", err)
		}
		if tl := mustGetTimeline(t, repo, user, id); tl.ShareToken != first {
			t.Errorf("ShareToken = %q, want it unchanged (%q)", tl.ShareToken, first)
		}
	})

	second := shareToken(t)
	t.Run("NewShareLink replaces the token", func(t *testing.T) {
		if err := repo.NewShareLink(ctx, user, id, second); err != nil {
			t.Fatalf("NewShareLink: %v", err)
		}
		if _, _, err := repo.GetShared(ctx, first); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetShared(old token) = %v, want ErrNoRecord", err)
		}
		if tl, _, err := repo.GetShared(ctx, second); err != nil || tl.ID != id {
			t.Errorf("GetShared(new token) = %+v, %v", tl, err)
		}
	})

	t.Run("Unshare clears it, and twice is fine", func(t *testing.T) {
		for range 2 {
			if err := repo.Unshare(ctx, user, id); err != nil {
				t.Fatalf("Unshare: %v", err)
			}
		}
		if _, _, err := repo.GetShared(ctx, second); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetShared after Unshare = %v, want ErrNoRecord", err)
		}
		if tl := mustGetTimeline(t, repo, user, id); tl.ShareToken != "" {
			t.Errorf("ShareToken = %q, want none", tl.ShareToken)
		}
	})

	t.Run("NewShareLink on an unshared timeline is ErrNoRecord", func(t *testing.T) {
		if err := repo.NewShareLink(ctx, user, id, shareToken(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
		if tl := mustGetTimeline(t, repo, user, id); tl.ShareToken != "" {
			t.Errorf("ShareToken = %q, want none", tl.ShareToken)
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

func TestWordsPattern(t *testing.T) {
	const before, after = `(?<![[:alnum:]])(?:`, `)(?![[:alnum:]])`
	tests := []struct {
		words []string
		want  string // between before and after; "" for no pattern
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"AI"}, `AI`},
		{[]string{"AI", "LLM", "GPT"}, `AI|LLM|GPT`},
		{[]string{"C++"}, `C\+\+`},
		{[]string{"C#", ".NET"}, `C\#|\.NET`},
		{[]string{"(x)", "a|b", `\`, "[x]"}, `\(x\)|a\|b|\\|\[x\]`},
		{[]string{"*", "?", "^", "$", "{2}"}, `\*|\?|\^|\$|\{2\}`},
		{[]string{"machine learning", "a \t\n b"}, `machine\s+learning|a\s+b`},
		{[]string{"café", "日本"}, `café|日本`},
	}
	for _, tt := range tests {
		want := ""
		if tt.want != "" {
			want = before + tt.want + after
		}
		if got := wordsPattern(tt.words); got != want {
			t.Errorf("wordsPattern(%q) = %q, want %q", tt.words, got, want)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB. Checks wordsPattern's
// patterns with Postgres's regular expressions and the database's locale.
func TestWordsPatternMatches(t *testing.T) {
	db := psqltest.NewDB(t)

	tests := []struct {
		term     string
		match    []string
		mismatch []string
	}{
		{"AI", []string{"New AI model", "AI's", "#AI", "(AI)", "ai at the start", "at the end ai"}, []string{"said", "AIDS", "OpenAI"}},
		{"C++", []string{"C++ 20", "learn C++.", "(C++)"}, []string{"C++20", "XC++"}},
		{"C#", []string{"in C#"}, []string{"C#9"}},
		{"F#", []string{"F# rocks"}, []string{"F#9"}},
		{".NET", []string{".NET 9", "the .NET runtime"}, []string{"ASP.NET"}},
		{"node.js", []string{"node.js", "Node.js 22"}, []string{"nodexjs"}},
		{"machine learning", []string{"machine  learning", "Machine\nlearning"}, []string{"machinelearning"}},
		{"café", []string{"Café culture"}, []string{"cafés"}},
		{"C", []string{"C++", "Plan C"}, []string{"CSS"}},
		{`a|b`, []string{"x a|b y"}, []string{"a", "b"}},
		{"²", []string{"x ² y"}, []string{"x"}},
	}
	for _, tt := range tests {
		pattern := wordsPattern([]string{tt.term})
		for _, text := range append(tt.match, tt.mismatch...) {
			want := slices.Contains(tt.match, text)
			var got bool
			if err := db.QueryRowContext(t.Context(), `SELECT $1 ~* $2`, text, pattern).Scan(&got); err != nil {
				t.Fatalf("%q ~* %q: %v", text, pattern, err)
			}
			if got != want {
				t.Errorf("%q ~* wordsPattern(%q) = %v, want %v", text, tt.term, got, want)
			}
		}
	}
}
