package psql

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Enrichment jobs exist only for wanted feeds, followed or in Discover's
// catalog (#708): these tests change what makes a feed wanted and check its
// jobs.

// unwantedFeedWithArticles saves, in a feed nobody follows (so no jobs are
// queued), 25 articles that need enrichment, n00 the oldest and n24 the
// latest, then "complete" (nothing to enrich), "no-url" (nothing to fetch)
// and "enriched" (already enriched, though it would need it), all older.
func unwantedFeedWithArticles(t *testing.T) (rss.Feed, func() map[string]int) {
	t.Helper()
	db := psqltest.NewDB(t)
	feed := newTestFeed(t, db)
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []rss.Article{
		{ExternalID: "complete", URL: "https://example.com/complete", ImageURL: "https://example.com/i.jpg", Title: "T", Excerpt: "E", PublishedAt: date},
		{ExternalID: "no-url", Title: "T", PublishedAt: date},
		{ExternalID: "enriched", URL: "https://example.com/enriched", Title: "T", PublishedAt: date},
	}
	for i := range 25 {
		items = append(items, rss.Article{
			ExternalID:  fmt.Sprintf("n%02d", i),
			URL:         fmt.Sprintf("https://example.com/%s/%d", feed.ID, i),
			Title:       "Title",
			Excerpt:     "Excerpt",
			PublishedAt: date.Add(time.Duration(i+1) * time.Hour),
		})
	}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, items, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if _, err := db.Exec(`UPDATE articles SET page_title = 'Page' WHERE feed_id = $1 AND external_id = 'enriched'`, feed.ID); err != nil {
		t.Fatalf("mark enriched: %v", err)
	}
	jobs := func() map[string]int { t.Helper(); return enrichJobs(t, db, feed.ID) }
	if got := jobs(); len(got) != 0 {
		t.Fatalf("a feed nobody follows has jobs after its save: %v", got)
	}
	return feed, jobs
}

// latestBacklog is the backlog a feed from unwantedFeedWithArticles gets
// when it becomes wanted: its 20 latest that need it, at priority 1.
func latestBacklog() map[string]int {
	want := map[string]int{}
	for i := 25 - enrichFirstWanted; i < 25; i++ {
		want[fmt.Sprintf("n%02d", i)] = enrichPriorityBacklog
	}
	return want
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscribeQueuesEnrichment(t *testing.T) {
	db := psqltest.NewDB(t)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	t.Run("the first follower queues the backlog", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		u1, u2 := newTestUser(t, db), newTestUser(t, db)
		if err := subs.Subscribe(ctx, u1, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if got, want := jobs(), latestBacklog(); !maps.Equal(got, want) {
			t.Errorf("jobs = %v, want %v", got, want)
		}

		// An article's existing job keeps its priority; a second follower
		// and following again queue nothing new.
		if _, err := db.Exec(`UPDATE enrich_jobs j SET priority = 0 FROM articles a
			WHERE a.id = j.article_id AND a.feed_id = $1 AND a.external_id = 'n24'`, feed.ID); err != nil {
			t.Fatalf("set priority: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM enrich_jobs j USING articles a
			WHERE a.id = j.article_id AND a.feed_id = $1 AND a.external_id = 'n23'`, feed.ID); err != nil {
			t.Fatalf("delete a job: %v", err)
		}
		for _, u := range []string{u2, u1} {
			if err := subs.Subscribe(ctx, u, feed.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
		want := latestBacklog()
		want["n24"] = 0
		delete(want, "n23")
		if got := jobs(); !maps.Equal(got, want) {
			t.Errorf("after more follows: jobs = %v, want %v", got, want)
		}
	})

	t.Run("a catalog feed's first follower queues nothing", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		putInCatalog(t, db, feed.ID)
		if err := subs.Subscribe(ctx, newTestUser(t, db), feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if got := jobs(); len(got) != 0 {
			t.Errorf("jobs = %v, want none: the feed was already wanted", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestUnsubscribeDropsEnrichment(t *testing.T) {
	db := psqltest.NewDB(t)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	t.Run("the last follower leaving drops the jobs", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		u1, u2 := newTestUser(t, db), newTestUser(t, db)
		for _, u := range []string{u1, u2} {
			if err := subs.Subscribe(ctx, u, feed.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
		if err := subs.Unsubscribe(ctx, u1, feed.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if got := jobs(); !maps.Equal(got, latestBacklog()) {
			t.Errorf("with a follower left: jobs = %v, want the backlog kept", got)
		}
		if err := subs.Unsubscribe(ctx, u2, feed.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if got := jobs(); len(got) != 0 {
			t.Errorf("with no follower left: jobs = %v, want none", got)
		}

		// Following again queues the backlog, skipping what was enriched
		// meanwhile.
		if _, err := db.Exec(`UPDATE articles SET page_image_url = 'https://example.com/p.jpg' WHERE feed_id = $1 AND external_id = 'n24'`, feed.ID); err != nil {
			t.Fatalf("mark enriched: %v", err)
		}
		if err := subs.Subscribe(ctx, u1, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		want := latestBacklog()
		delete(want, "n24")
		want[fmt.Sprintf("n%02d", 25-enrichFirstWanted-1)] = enrichPriorityBacklog
		if got := jobs(); !maps.Equal(got, want) {
			t.Errorf("following again: jobs = %v, want %v", got, want)
		}
	})

	t.Run("a catalog feed keeps its jobs", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		u := newTestUser(t, db)
		if err := subs.Subscribe(ctx, u, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		putInCatalog(t, db, feed.ID)
		if err := subs.Unsubscribe(ctx, u, feed.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if got := jobs(); !maps.Equal(got, latestBacklog()) {
			t.Errorf("jobs = %v, want the backlog kept: the feed is still wanted", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestDeleteUserDropsEnrichment(t *testing.T) {
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	const password = "correct-horse-battery"
	email := fmt.Sprintf("test-delete-enrich-%d@example.com", psqltest.Unique())
	gone, _, err := users.Create(ctx, "Leaving", email, password, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = $1`, gone) })
	stays := newTestUser(t, db)

	onlyTheirs, onlyTheirsJobs := unwantedFeedWithArticles(t)
	shared, sharedJobs := unwantedFeedWithArticles(t)
	for _, f := range []string{onlyTheirs.ID, shared.ID} {
		if err := subs.Subscribe(ctx, gone, f); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	if err := subs.Subscribe(ctx, stays, shared.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if _, err := users.Delete(ctx, gone, password); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := onlyTheirsJobs(); len(got) != 0 {
		t.Errorf("a feed only they followed: jobs = %v, want none", got)
	}
	if got := sharedJobs(); !maps.Equal(got, latestBacklog()) {
		t.Errorf("a feed another reader follows: jobs = %v, want the backlog kept", got)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestSetDiscoverSectionEnrichment(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()
	by := testActor(t, db)

	sections, err := repo.DiscoverSections(ctx)
	if err != nil || len(sections) < 2 {
		t.Fatalf("DiscoverSections = %v, %v; want at least 2", sections, err)
	}
	cleanupAudit := func(feedID string) {
		t.Cleanup(func() { db.Exec(`DELETE FROM admin_audit WHERE target_id = $1`, feedID) })
	}

	t.Run("an unfollowed feed entering and leaving the catalog", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		cleanupAudit(feed.ID)
		if err := repo.SetDiscoverSection(ctx, feed.ID, sections[0].ID, by); err != nil {
			t.Fatalf("SetDiscoverSection: %v", err)
		}
		if got := jobs(); !maps.Equal(got, latestBacklog()) {
			t.Errorf("entering: jobs = %v, want the backlog", got)
		}
		// A section change queues nothing more.
		if _, err := db.Exec(`DELETE FROM enrich_jobs j USING articles a
			WHERE a.id = j.article_id AND a.feed_id = $1 AND a.external_id = 'n24'`, feed.ID); err != nil {
			t.Fatalf("delete a job: %v", err)
		}
		if err := repo.SetDiscoverSection(ctx, feed.ID, sections[1].ID, by); err != nil {
			t.Fatalf("SetDiscoverSection: %v", err)
		}
		if got := jobs(); len(got) != enrichFirstWanted-1 {
			t.Errorf("after a section change: %d jobs, want %d", len(got), enrichFirstWanted-1)
		}
		if err := repo.SetDiscoverSection(ctx, feed.ID, "", by); err != nil {
			t.Fatalf("SetDiscoverSection out: %v", err)
		}
		if got := jobs(); len(got) != 0 {
			t.Errorf("leaving: jobs = %v, want none", got)
		}
	})

	t.Run("a followed feed", func(t *testing.T) {
		feed, jobs := unwantedFeedWithArticles(t)
		cleanupAudit(feed.ID)
		if err := subs.Subscribe(ctx, newTestUser(t, db), feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM enrich_jobs j USING articles a
			WHERE a.id = j.article_id AND a.feed_id = $1 AND a.external_id = 'n24'`, feed.ID); err != nil {
			t.Fatalf("delete a job: %v", err)
		}
		before := slices.Sorted(maps.Keys(jobs()))
		if err := repo.SetDiscoverSection(ctx, feed.ID, sections[0].ID, by); err != nil {
			t.Fatalf("SetDiscoverSection: %v", err)
		}
		if got := slices.Sorted(maps.Keys(jobs())); !slices.Equal(got, before) {
			t.Errorf("entering: jobs = %v, want unchanged %v", got, before)
		}
		if err := repo.SetDiscoverSection(ctx, feed.ID, "", by); err != nil {
			t.Fatalf("SetDiscoverSection out: %v", err)
		}
		if got := slices.Sorted(maps.Keys(jobs())); !slices.Equal(got, before) {
			t.Errorf("leaving: jobs = %v, want unchanged %v: someone follows it", got, before)
		}
	})
}
