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
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, items, feed.NextFetch); err != nil {
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
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, []rss.Article{item}, feed.NextFetch); err != nil {
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

// timelineLineRow reads userID's timeline_lines row for timelineID ("" for
// Home); ok is false if there's none.
func timelineLineRow(t *testing.T, db *sql.DB, userID, timelineID string) (seenUpTo time.Time, dividerAt sql.NullTime, ok bool) {
	t.Helper()
	err := db.QueryRow(`SELECT seen_up_to, divider_at FROM timeline_lines
		WHERE user_id = $1 AND timeline_id IS NOT DISTINCT FROM $2::uuid`, userID, nullUUID(timelineID)).Scan(&seenUpTo, &dividerAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, sql.NullTime{}, false
	}
	if err != nil {
		t.Fatalf("select timeline_lines: %v", err)
	}
	return seenUpTo, dividerAt, true
}

// newTestTimeline creates a custom timeline for userID, deleted with the user.
func newTestTimeline(t *testing.T, db *sql.DB, userID, name string) string {
	t.Helper()
	id, err := NewTimelineRepository(db).Create(t.Context(), userID, rss.Timeline{Name: name})
	if err != nil {
		t.Fatalf("create timeline: %v", err)
	}
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestTimelineLine(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()

	t.Run("zero without a mark", func(t *testing.T) {
		user := newTestUser(t, db)
		for _, id := range []string{"", newTestTimeline(t, db, user, "Unseen")} {
			m, err := repo.TimelineLine(ctx, user, id)
			if err != nil {
				t.Fatalf("TimelineLine(%q): %v", id, err)
			}
			if m != (rss.TimelineMark{}) {
				t.Errorf("TimelineLine(%q) = %+v, want zero", id, m)
			}
		}
	})

	t.Run("a timeline never seen has the user's SeenAt and no SeenUpTo", func(t *testing.T) {
		user := newTestUser(t, db)
		unseen := newTestTimeline(t, db, user, "Unseen")
		if err := repo.MarkSeen(ctx, user, "", time.Now()); err != nil {
			t.Fatalf("MarkSeen: %v", err)
		}
		m, err := repo.TimelineLine(ctx, user, unseen)
		if err != nil {
			t.Fatalf("TimelineLine: %v", err)
		}
		if m.SeenAt.IsZero() || !m.SeenUpTo.IsZero() || !m.DividerAt.IsZero() {
			t.Errorf("TimelineLine = %+v, want SeenAt set and zero SeenUpTo and DividerAt", m)
		}
	})

	t.Run("Home and a custom timeline each return their own", func(t *testing.T) {
		user := newTestUser(t, db)
		custom := newTestTimeline(t, db, user, "Custom")
		base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		if err := repo.MarkSeen(ctx, user, "", base); err != nil {
			t.Fatalf("MarkSeen Home: %v", err)
		}
		if err := repo.MarkSeen(ctx, user, custom, base.Add(-10*time.Minute)); err != nil {
			t.Fatalf("MarkSeen custom: %v", err)
		}
		if _, err := db.Exec(`UPDATE timeline_lines SET divider_at = seen_up_to - interval '1 hour' WHERE user_id = $1`, user); err != nil {
			t.Fatalf("set divider_at: %v", err)
		}
		mark, _, _ := timelineMarkRow(t, db, user)

		for _, tt := range []struct {
			name, id string
			upTo     time.Time
		}{
			{"Home", "", base},
			{"custom", custom, base.Add(-10 * time.Minute)},
		} {
			m, err := repo.TimelineLine(ctx, user, tt.id)
			if err != nil {
				t.Fatalf("TimelineLine %s: %v", tt.name, err)
			}
			want := rss.TimelineMark{SeenUpTo: tt.upTo, SeenAt: mark.SeenAt, DividerAt: tt.upTo.Add(-time.Hour)}
			if !m.SeenUpTo.Equal(want.SeenUpTo) || !m.SeenAt.Equal(want.SeenAt) || !m.DividerAt.Equal(want.DividerAt) {
				t.Errorf("TimelineLine %s = %+v, want %+v", tt.name, m, want)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkSeenLines(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)

	markSeen := func(t *testing.T, user, timelineID string, upTo time.Time) {
		t.Helper()
		if err := repo.MarkSeen(ctx, user, timelineID, upTo); err != nil {
			t.Fatalf("MarkSeen(%q, %v): %v", timelineID, upTo, err)
		}
	}
	wantLine := func(t *testing.T, user, timelineID string, upTo time.Time, dividerAt time.Time) {
		t.Helper()
		got, divider, ok := timelineLineRow(t, db, user, timelineID)
		if !ok {
			t.Fatalf("no line for timeline %q", timelineID)
		}
		if !got.Equal(upTo) || divider.Valid != !dividerAt.IsZero() || !divider.Time.Equal(dividerAt) {
			t.Errorf("line for %q: seen_up_to %v, divider_at %v (valid %v); want %v, %v", timelineID, got, divider.Time, divider.Valid, upTo, dividerAt)
		}
	}

	t.Run("a report records its timeline's line and raises the user's mark", func(t *testing.T) {
		user := newTestUser(t, db)
		custom := newTestTimeline(t, db, user, "Custom")
		markSeen(t, user, custom, base)
		wantLine(t, user, custom, base, time.Time{})
		if m, _, ok := timelineMarkRow(t, db, user); !ok || !m.SeenUpTo.Equal(base) {
			t.Errorf("mark seen_up_to = %v (row: %v), want %v", m.SeenUpTo, ok, base)
		}
		if _, _, ok := timelineLineRow(t, db, user, ""); ok {
			t.Errorf("a report on a custom timeline wrote Home's line")
		}

		// Its line rises and never falls, under the same cap as the mark's.
		markSeen(t, user, custom, base.Add(time.Minute))
		markSeen(t, user, custom, base)
		wantLine(t, user, custom, base.Add(time.Minute), time.Time{})
		markSeen(t, user, custom, time.Now().Add(24*time.Hour))
		if got, _, _ := timelineLineRow(t, db, user, custom); got.After(time.Now()) {
			t.Errorf("after a future report: seen_up_to = %v, in the future", got)
		}
	})

	t.Run("Home's and a custom timeline's lines are separate", func(t *testing.T) {
		user := newTestUser(t, db)
		custom := newTestTimeline(t, db, user, "Custom")
		markSeen(t, user, "", base)
		markSeen(t, user, custom, base.Add(time.Minute))
		// A report on one timeline doesn't move another's.
		wantLine(t, user, "", base, time.Time{})
		wantLine(t, user, custom, base.Add(time.Minute), time.Time{})

		markSeen(t, user, "", base.Add(2*time.Minute))
		wantLine(t, user, "", base.Add(2*time.Minute), time.Time{})
		wantLine(t, user, custom, base.Add(time.Minute), time.Time{})
		if m, _, _ := timelineMarkRow(t, db, user); !m.SeenUpTo.Equal(base.Add(2 * time.Minute)) {
			t.Errorf("mark seen_up_to = %v, want the newest on any timeline, %v", m.SeenUpTo, base.Add(2*time.Minute))
		}
	})

	t.Run("a new visit moves every line's divider to its own seen_up_to first", func(t *testing.T) {
		user := newTestUser(t, db)
		custom := newTestTimeline(t, db, user, "Custom")
		unopened := newTestTimeline(t, db, user, "Unopened")
		markSeen(t, user, "", base)
		markSeen(t, user, custom, base.Add(-time.Hour))
		markSeen(t, user, unopened, base.Add(-2*time.Hour))

		if _, err := db.Exec(`UPDATE timeline_marks SET seen_at = now() - interval '1 hour' WHERE user_id = $1`, user); err != nil {
			t.Fatalf("set seen_at: %v", err)
		}
		markSeen(t, user, custom, base.Add(30*time.Minute))
		wantLine(t, user, "", base, base)
		wantLine(t, user, custom, base.Add(30*time.Minute), base.Add(-time.Hour))
		wantLine(t, user, unopened, base.Add(-2*time.Hour), base.Add(-2*time.Hour))

		// A visit going on keeps them.
		markSeen(t, user, "", base.Add(40*time.Minute))
		wantLine(t, user, "", base.Add(40*time.Minute), base)
		wantLine(t, user, custom, base.Add(30*time.Minute), base.Add(-time.Hour))
	})

	t.Run("another user's timeline records the user's mark only", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		theirs := newTestTimeline(t, db, other, "Theirs")
		markSeen(t, user, theirs, base)
		if m, _, ok := timelineMarkRow(t, db, user); !ok || !m.SeenUpTo.Equal(base) {
			t.Errorf("mark seen_up_to = %v (row: %v), want %v", m.SeenUpTo, ok, base)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM timeline_lines WHERE user_id = $1 OR timeline_id = $2`, user, theirs).Scan(&n); err != nil {
			t.Fatalf("count timeline_lines: %v", err)
		}
		if n != 0 {
			t.Errorf("%d timeline_lines rows, want none", n)
		}
	})

	t.Run("deleting the timeline deletes its line", func(t *testing.T) {
		user := newTestUser(t, db)
		custom := newTestTimeline(t, db, user, "Custom")
		markSeen(t, user, custom, base)
		if _, err := db.Exec(`DELETE FROM timelines WHERE id = $1`, custom); err != nil {
			t.Fatalf("delete timeline: %v", err)
		}
		if _, _, ok := timelineLineRow(t, db, user, custom); ok {
			t.Errorf("the line outlived its timeline")
		}
	})

	t.Run("deleting the user deletes their lines", func(t *testing.T) {
		user := newTestUser(t, db)
		markSeen(t, user, "", base)
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, user); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		if _, _, ok := timelineLineRow(t, db, user, ""); ok {
			t.Errorf("the line outlived its user")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkSeen(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	ctx := t.Context()
	user := newTestUser(t, db)
	base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)

	// The first report stores the position and when, with no divider yet.
	if err := repo.MarkSeen(ctx, user, "", base); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	m, dividerNull, ok := timelineMarkRow(t, db, user)
	if !ok || !m.SeenUpTo.Equal(base) || m.SeenAt.IsZero() || !dividerNull {
		t.Fatalf("after the first report: %+v (divider NULL: %v, row: %v), want seen_up_to %v, seen_at set, divider_at NULL", m, dividerNull, ok, base)
	}
	got, err := repo.TimelineLine(ctx, user, "")
	if err != nil {
		t.Fatalf("TimelineLine: %v", err)
	}
	if !got.SeenUpTo.Equal(m.SeenUpTo) || !got.SeenAt.Equal(m.SeenAt) || !got.DividerAt.IsZero() {
		t.Errorf("TimelineLine = %+v, want %+v", got, m)
	}

	// A later position in the same visit raises seen_up_to and keeps divider_at.
	if _, err := db.Exec(`UPDATE timeline_marks SET divider_at = $2 WHERE user_id = $1`, user, base.Add(-time.Hour)); err != nil {
		t.Fatalf("set divider_at: %v", err)
	}
	later := base.Add(10 * time.Minute)
	if err := repo.MarkSeen(ctx, user, "", later); err != nil {
		t.Fatalf("MarkSeen later: %v", err)
	}
	m, _, _ = timelineMarkRow(t, db, user)
	if !m.SeenUpTo.Equal(later) || !m.DividerAt.Equal(base.Add(-time.Hour)) {
		t.Errorf("after a later report: %+v, want seen_up_to %v and divider_at %v", m, later, base.Add(-time.Hour))
	}

	// An earlier position doesn't lower seen_up_to.
	if err := repo.MarkSeen(ctx, user, "", base); err != nil {
		t.Fatalf("MarkSeen earlier: %v", err)
	}
	if m, _, _ = timelineMarkRow(t, db, user); !m.SeenUpTo.Equal(later) {
		t.Errorf("after an earlier report: seen_up_to = %v, want %v", m.SeenUpTo, later)
	}

	// A position in the future is stored as at most now.
	if err := repo.MarkSeen(ctx, user, "", time.Now().Add(24*time.Hour)); err != nil {
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

	if err := repo.MarkSeen(ctx, user, "", old); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if _, err := db.Exec(`UPDATE timeline_marks SET seen_at = now() - interval '1 hour' WHERE user_id = $1`, user); err != nil {
		t.Fatalf("set seen_at: %v", err)
	}
	if err := repo.MarkSeen(ctx, user, "", old.Add(time.Hour)); err != nil {
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
	if err := NewReadRepository(db).MarkSeen(t.Context(), user, "", time.Now()); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, user); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, _, ok := timelineMarkRow(t, db, user); ok {
		t.Errorf("the mark outlived its user")
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestMarkSeenTrimsReaderPositions(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewReadRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()
	feed := newTestFeed(t, db)
	// Rows are a second apart, the newest at base.
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	second := func(i int) time.Time { return base.Add(-time.Duration(i) * time.Second) }

	// newReader returns a new user following feed, with n rows for articles
	// of it at base, base - 1s, … (their articles at the same positions).
	newReader := func(t *testing.T, n int) string {
		t.Helper()
		user := newTestUser(t, db)
		if err := subs.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		_, err := db.Exec(`WITH a AS (
			    INSERT INTO articles (feed_id, external_id, title, timeline_at, created_at)
			    SELECT $1, $2::text || '-' || g, 'Article', $3::timestamptz - g * interval '1 second', $3::timestamptz - g * interval '1 second'
			    FROM generate_series(0, $4 - 1) g
			    RETURNING id, timeline_at
			)
			INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at)
			SELECT $2::uuid, id, $1, timeline_at FROM a`, feed.ID, user, base, n)
		if err != nil {
			t.Fatalf("insert %d rows: %v", n, err)
		}
		return user
	}
	// setMark gives user a mark reported ago, with dividerAt (NULL if zero).
	setMark := func(t *testing.T, user string, ago time.Duration, dividerAt time.Time) {
		t.Helper()
		divider := sql.NullTime{Time: dividerAt, Valid: !dividerAt.IsZero()}
		if _, err := db.Exec(`INSERT INTO timeline_marks (user_id, seen_up_to, seen_at, divider_at)
			VALUES ($1, $2, now() - $3 * interval '1 microsecond', $4)`, user, base, ago.Microseconds(), divider); err != nil {
			t.Fatalf("insert timeline_marks: %v", err)
		}
	}
	// wantNewest checks user's rows are exactly the newest n of those
	// newReader inserted.
	wantNewest := func(t *testing.T, user string, n int) {
		t.Helper()
		var count int
		var newest, oldest sql.NullTime
		if err := db.QueryRow(`SELECT count(*), max(timeline_at), min(timeline_at) FROM timeline_entries WHERE user_id = $1`, user).Scan(&count, &newest, &oldest); err != nil {
			t.Fatalf("count timeline_entries: %v", err)
		}
		if count != n || !newest.Time.Equal(base) || !oldest.Time.Equal(second(n-1)) {
			t.Errorf("%d rows from %v to %v, want the newest %d: from %v to %v", count, newest.Time, oldest.Time, n, base, second(n-1))
		}
	}
	markSeen := func(t *testing.T, user string) {
		t.Helper()
		if err := repo.MarkSeen(ctx, user, "", time.Now()); err != nil {
			t.Fatalf("MarkSeen: %v", err)
		}
	}

	t.Run("the first report trims nothing", func(t *testing.T) {
		user := newReader(t, 600)
		markSeen(t, user)
		wantNewest(t, user, 600)
	})

	t.Run("a report during a visit trims nothing", func(t *testing.T) {
		user := newReader(t, 600)
		setMark(t, user, 10*time.Minute, second(1000))
		markSeen(t, user)
		wantNewest(t, user, 600)
	})

	t.Run("a new visit keeps every row above the previous visit's line", func(t *testing.T) {
		user := newReader(t, 500)
		// Rows 0–299 are above the line; row 300 is at it.
		setMark(t, user, time.Hour, second(300))
		markSeen(t, user)
		wantNewest(t, user, 300)
	})

	t.Run("at least MinEntries", func(t *testing.T) {
		user := newReader(t, 250)
		setMark(t, user, time.Hour, base) // the newest row is at the line
		markSeen(t, user)
		wantNewest(t, user, rss.MinEntries)
	})

	t.Run("at most MaxEntries", func(t *testing.T) {
		user := newReader(t, 600)
		setMark(t, user, time.Hour, second(1000))
		markSeen(t, user)
		wantNewest(t, user, rss.MaxEntries)
	})

	t.Run("the second visit keeps the newest MaxEntries", func(t *testing.T) {
		user := newReader(t, 600)
		setMark(t, user, time.Hour, time.Time{})
		markSeen(t, user)
		wantNewest(t, user, rss.MaxEntries)
	})

	t.Run("other readers' rows are untouched", func(t *testing.T) {
		user := newReader(t, 600)
		other := newReader(t, 600)
		setMark(t, user, time.Hour, base)
		markSeen(t, user)
		wantNewest(t, user, rss.MinEntries)
		wantNewest(t, other, 600)
	})
}
