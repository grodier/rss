package psql

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newEnrichJob saves an article missing its image in feed and gives it an
// enrichment job with priority and next_attempt_at, inserting one if the
// save didn't queue it (a feed that isn't wanted gets none, so such a job
// is one an earlier version left). It returns the article.
func newEnrichJob(t *testing.T, db *sql.DB, feed rss.Feed, externalID string, priority int, next time.Time) rss.Article {
	t.Helper()
	a := rss.Article{ExternalID: externalID, URL: "https://example.com/" + externalID, Title: "T", Excerpt: "E"}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, []rss.Article{a}, feed.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feed.ID, externalID).Scan(&a.ID); err != nil {
		t.Fatalf("select article: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO enrich_jobs (article_id, priority, next_attempt_at) VALUES ($1, $2, $3)
		ON CONFLICT (article_id) DO NOTHING`, a.ID, priority, next); err != nil {
		t.Fatalf("insert enrich_jobs: %v", err)
	}
	setEnrichJob(t, db, a.ID, priority, next)
	return a
}

// putInCatalog puts feedID in Discover's catalog, under the first section,
// as the admin does, without SetDiscoverSection's other effects.
func putInCatalog(t *testing.T, db *sql.DB, feedID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO discover_feeds (feed_id, section_id)
		SELECT $1, id FROM discover_sections ORDER BY position LIMIT 1`, feedID); err != nil {
		t.Fatalf("insert discover_feeds: %v", err)
	}
}

func setEnrichJob(t *testing.T, db *sql.DB, articleID string, priority int, next time.Time) {
	t.Helper()
	res, err := db.Exec(`UPDATE enrich_jobs SET priority = $2, next_attempt_at = $3 WHERE article_id = $1`, articleID, priority, next)
	if err != nil {
		t.Fatalf("update enrich_jobs: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("article %s has no enrichment job", articleID)
	}
}

// enrichJob is an enrich_jobs row.
type enrichJob struct {
	attempts  int
	next      time.Time
	lastError string
}

// getEnrichJob returns articleID's job, and false if it has none.
func getEnrichJob(t *testing.T, db *sql.DB, articleID string) (enrichJob, bool) {
	t.Helper()
	var j enrichJob
	err := db.QueryRow(`SELECT attempts, next_attempt_at, last_error FROM enrich_jobs WHERE article_id = $1`, articleID).Scan(&j.attempts, &j.next, &j.lastError)
	if errors.Is(err, sql.ErrNoRows) {
		return enrichJob{}, false
	}
	if err != nil {
		t.Fatalf("select enrich_jobs: %v", err)
	}
	return j, true
}

// Requires a migrated database; see psqltest.NewDB.
func TestEnrichRepositoryClaimNext(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewEnrichRepository(db)
	userID := newTestUser(t, db)
	const lease = 2 * time.Minute
	// Other tests share the database, so make this test's jobs due longest.
	longAgo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	subscribed := newSubscribedFeed(t, db, userID)
	unsubscribed := newTestFeed(t, db)
	catalog := newTestFeed(t, db)
	putInCatalog(t, db, catalog.ID)

	lonely := newEnrichJob(t, db, unsubscribed, "lonely", 0, longAgo.Add(-time.Hour))
	backlog := newEnrichJob(t, db, subscribed, "backlog", 1, longAgo.Add(-time.Hour))
	catalogBacklog := newEnrichJob(t, db, catalog, "catalog-backlog", 1, longAgo.Add(-30*time.Minute))
	notDue := newEnrichJob(t, db, subscribed, "not-due", 0, time.Now().Add(time.Hour))
	first := newEnrichJob(t, db, subscribed, "first", 0, longAgo)
	second := newEnrichJob(t, db, subscribed, "second", 0, longAgo.Add(time.Minute))

	claim := func(t *testing.T) rss.EnrichJob {
		t.Helper()
		job, err := repo.ClaimNext(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		return job
	}

	// Priority 0 comes first, by next_attempt_at; a catalog feed's job is
	// claimed like a followed one's; feeds neither followed nor in the
	// catalog, and jobs not yet due, are skipped.
	got := claim(t)
	if got.ArticleID != first.ID || got.URL != first.URL || got.Attempts != 1 {
		t.Errorf("first claim = %+v; want %s, %s, attempt 1", got, first.ID, first.URL)
	}
	if got := claim(t); got.ArticleID != second.ID {
		t.Errorf("second claim = %s; want %s (second)", got.ArticleID, second.ID)
	}
	if got := claim(t); got.ArticleID != backlog.ID {
		t.Errorf("third claim = %s; want %s (backlog)", got.ArticleID, backlog.ID)
	}
	if got := claim(t); got.ArticleID != catalogBacklog.ID {
		t.Errorf("fourth claim = %s; want %s (a catalog feed's)", got.ArticleID, catalogBacklog.ID)
	}

	j, _ := getEnrichJob(t, db, first.ID)
	if j.attempts != 1 {
		t.Errorf("attempts = %d; want 1", j.attempts)
	}
	if now := dbNow(t, db); j.next.Before(now.Add(lease-time.Minute)) || j.next.After(now.Add(lease+time.Minute)) {
		t.Errorf("next_attempt_at = %v; want about now + %v", j.next, lease)
	}

	for _, a := range []rss.Article{lonely, notDue} {
		if j, _ := getEnrichJob(t, db, a.ID); j.attempts != 0 {
			t.Errorf("job %s attempts = %d; want 0 (never claimed)", a.ExternalID, j.attempts)
		}
	}

	// A claim after the lease ran out counts the next attempt.
	setEnrichJob(t, db, first.ID, 0, longAgo)
	if got := claim(t); got.ArticleID != first.ID || got.Attempts != 2 {
		t.Errorf("reclaim = %+v; want %s, attempt 2", got, first.ID)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestEnrichRepositoryRecord(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewEnrichRepository(db)
	articles := NewArticleRepository(db)
	// Followed, since later saves of a feed nobody follows queue no jobs.
	feed := newSubscribedFeed(t, db, newTestUser(t, db))
	ctx := t.Context()

	t.Run("SavePage", func(t *testing.T) {
		a := newEnrichJob(t, db, feed, "save", 0, time.Now())
		page := rss.ArticlePage{ImageURL: "https://example.com/p.jpg", Title: "Page title", Description: "Page description"}
		if err := repo.SavePage(ctx, a.ID, page); err != nil {
			t.Fatalf("SavePage: %v", err)
		}
		got, err := articles.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.PageImageURL != page.ImageURL || got.PageTitle != page.Title || got.PageDescription != page.Description {
			t.Errorf("page columns = %q, %q, %q; want %+v", got.PageImageURL, got.PageTitle, got.PageDescription, page)
		}
		if got.Title != "T" || got.Excerpt != "E" {
			t.Errorf("feed's values changed: title %q, excerpt %q", got.Title, got.Excerpt)
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("job still exists after SavePage")
		}
	})

	t.Run("SavePage of a missing article", func(t *testing.T) {
		err := repo.SavePage(ctx, "00000000-0000-0000-0000-000000000000", rss.ArticlePage{Title: "x"})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SavePage error = %v; want rss.ErrNoRecord", err)
		}
	})

	t.Run("Retry", func(t *testing.T) {
		a := newEnrichJob(t, db, feed, "retry", 0, time.Now())
		next := time.Now().Add(time.Hour).Truncate(time.Microsecond)
		if err := repo.Retry(ctx, a.ID, "status 503", next); err != nil {
			t.Fatalf("Retry: %v", err)
		}
		j, ok := getEnrichJob(t, db, a.ID)
		if !ok {
			t.Fatal("job gone after Retry")
		}
		if j.lastError != "status 503" || !j.next.Equal(next) {
			t.Errorf("job = %+v; want last_error %q, next %v", j, "status 503", next)
		}
	})

	t.Run("GiveUp", func(t *testing.T) {
		a := newEnrichJob(t, db, feed, "give-up", 0, time.Now())
		if err := repo.GiveUp(ctx, a.ID, "status 404"); err != nil {
			t.Fatalf("GiveUp: %v", err)
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("job still exists after GiveUp")
		}
		got, err := articles.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.PageImageURL != "" || got.PageTitle != "" || got.PageDescription != "" {
			t.Errorf("page columns = %q, %q, %q; want empty", got.PageImageURL, got.PageTitle, got.PageDescription)
		}
	})

	t.Run("Retry and GiveUp without a job", func(t *testing.T) {
		const id = "00000000-0000-0000-0000-000000000000"
		if err := repo.Retry(ctx, id, "x", time.Now()); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Retry error = %v; want rss.ErrNoRecord", err)
		}
		if err := repo.GiveUp(ctx, id, "x"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GiveUp error = %v; want rss.ErrNoRecord", err)
		}
	})

	t.Run("deleting the article deletes its job", func(t *testing.T) {
		a := newEnrichJob(t, db, feed, "cascade", 0, time.Now())
		if _, err := db.Exec(`DELETE FROM articles WHERE id = $1`, a.ID); err != nil {
			t.Fatalf("delete article: %v", err)
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("job still exists after its article was deleted")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestReportImageFailed(t *testing.T) {
	db := psqltest.NewDB(t)
	articles := NewArticleRepository(db)
	feeds := NewFeedRepository(db)
	wanted := newSubscribedFeed(t, db, newTestUser(t, db))
	ctx := t.Context()

	// save saves a in feed and returns it as stored.
	save := func(t *testing.T, feed rss.Feed, a rss.Article) rss.Article {
		t.Helper()
		if _, err := feeds.SaveFetch(ctx, feed, []rss.Article{a}, feed.NextFetch); err != nil {
			t.Fatalf("SaveFetch: %v", err)
		}
		var id string
		if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feed.ID, a.ExternalID).Scan(&id); err != nil {
			t.Fatalf("select article: %v", err)
		}
		got, err := articles.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return got
	}
	// complete is an article with everything from its feed, which saving
	// never queues.
	complete := func(externalID string) rss.Article {
		return rss.Article{ExternalID: externalID, URL: "https://example.com/" + externalID,
			ImageURL: "https://example.com/" + externalID + ".jpg", Title: "T", Excerpt: "E"}
	}
	report := func(t *testing.T, a rss.Article) rss.Article {
		t.Helper()
		if err := articles.ReportImageFailed(ctx, a.ID); err != nil {
			t.Fatalf("ReportImageFailed: %v", err)
		}
		got, err := articles.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return got
	}

	t.Run("queues a never-enriched article of a wanted feed", func(t *testing.T) {
		a := save(t, wanted, complete("queued"))
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Fatal("a complete article was queued when saved")
		}
		got := report(t, a)
		if got.ImageFailedAt.IsZero() {
			t.Error("image_failed_at not set")
		}
		j, ok := getEnrichJob(t, db, a.ID)
		if !ok {
			t.Fatal("no job after the report")
		}
		if j.attempts != 0 || j.next.After(time.Now()) {
			t.Errorf("job = %+v; want a new job, due now", j)
		}
		var priority int
		if err := db.QueryRow(`SELECT priority FROM enrich_jobs WHERE article_id = $1`, a.ID).Scan(&priority); err != nil {
			t.Fatalf("select priority: %v", err)
		}
		if priority != enrichPriorityNew {
			t.Errorf("priority = %d; want %d", priority, enrichPriorityNew)
		}
	})

	t.Run("only the first report counts", func(t *testing.T) {
		a := report(t, save(t, wanted, complete("again")))
		// As if enrichment gave up, which leaves the page columns empty.
		if err := NewEnrichRepository(db).GiveUp(ctx, a.ID, "status 403"); err != nil {
			t.Fatalf("GiveUp: %v", err)
		}
		got := report(t, a)
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("a second report queued a job")
		}
		if !got.ImageFailedAt.Equal(a.ImageFailedAt) {
			t.Errorf("image_failed_at = %v; want the first report's %v", got.ImageFailedAt, a.ImageFailedAt)
		}
	})

	t.Run("an enriched article is marked but not queued", func(t *testing.T) {
		a := save(t, wanted, complete("enriched"))
		if _, err := db.Exec(`UPDATE articles SET page_title = 'Page' WHERE id = $1`, a.ID); err != nil {
			t.Fatalf("update article: %v", err)
		}
		if got := report(t, a); got.ImageFailedAt.IsZero() {
			t.Error("image_failed_at not set")
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("an enriched article was queued")
		}
	})

	t.Run("an article of a feed nobody wants is marked but not queued", func(t *testing.T) {
		a := save(t, newTestFeed(t, db), complete("unwanted"))
		if got := report(t, a); got.ImageFailedAt.IsZero() {
			t.Error("image_failed_at not set")
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("an article of an unwanted feed was queued")
		}
	})

	t.Run("a report on a feed nobody wants is queued when it becomes wanted", func(t *testing.T) {
		feed := newTestFeed(t, db)
		a := report(t, save(t, feed, complete("later")))
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Fatal("an article of an unwanted feed was queued")
		}
		if err := NewSubscriptionRepository(db).Subscribe(ctx, newTestUser(t, db), feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if _, ok := getEnrichJob(t, db, a.ID); !ok {
			t.Error("no job once the feed was followed")
		}
	})

	t.Run("an article in the catalog is queued", func(t *testing.T) {
		feed := newTestFeed(t, db)
		putInCatalog(t, db, feed.ID)
		a := save(t, feed, complete("catalog"))
		report(t, a)
		if _, ok := getEnrichJob(t, db, a.ID); !ok {
			t.Error("no job after the report")
		}
	})

	t.Run("an article without a URL is marked but not queued", func(t *testing.T) {
		in := complete("no-url")
		in.URL = ""
		a := save(t, wanted, in)
		if got := report(t, a); got.ImageFailedAt.IsZero() {
			t.Error("image_failed_at not set")
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("an article without a URL was queued")
		}
	})

	t.Run("an article without a feed image is left as it is", func(t *testing.T) {
		in := complete("no-image")
		in.ImageURL = ""
		a := save(t, newTestFeed(t, db), in)
		if got := report(t, a); !got.ImageFailedAt.IsZero() {
			t.Errorf("image_failed_at = %v; want unset", got.ImageFailedAt)
		}
		if _, ok := getEnrichJob(t, db, a.ID); ok {
			t.Error("an article without a feed image was queued")
		}
	})

	t.Run("an existing job is kept", func(t *testing.T) {
		in := complete("has-job")
		in.Title = "" // queued when saved
		a := save(t, wanted, in)
		next := time.Now().Add(time.Hour).Truncate(time.Microsecond)
		setEnrichJob(t, db, a.ID, enrichPriorityBacklog, next)
		report(t, a)
		j, ok := getEnrichJob(t, db, a.ID)
		if !ok {
			t.Fatal("job gone after the report")
		}
		if !j.next.Equal(next) {
			t.Errorf("next_attempt_at = %v; want %v, unchanged", j.next, next)
		}
	})

	t.Run("a missing article", func(t *testing.T) {
		err := articles.ReportImageFailed(ctx, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("ReportImageFailed error = %v; want rss.ErrNoRecord", err)
		}
	})

	t.Run("a changed feed image clears the report", func(t *testing.T) {
		in := complete("changed")
		report(t, save(t, wanted, in))
		in.ImageURL = "https://example.com/fixed.jpg"
		if got := save(t, wanted, in); !got.ImageFailedAt.IsZero() {
			t.Errorf("image_failed_at = %v after the image changed; want unset", got.ImageFailedAt)
		}
	})

	t.Run("an update that keeps the feed image keeps the report", func(t *testing.T) {
		in := complete("kept")
		a := report(t, save(t, wanted, in))
		in.Title = "New title"
		got := save(t, wanted, in)
		if got.Title != "New title" {
			t.Fatalf("title = %q; the article wasn't updated", got.Title)
		}
		if !got.ImageFailedAt.Equal(a.ImageFailedAt) {
			t.Errorf("image_failed_at = %v; want %v, kept", got.ImageFailedAt, a.ImageFailedAt)
		}
	})
}
