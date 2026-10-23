package psql

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newEnrichJob saves an article missing its image in feed and sets its
// enrichment job's priority and next_attempt_at. It returns the article.
func newEnrichJob(t *testing.T, db *sql.DB, feed rss.Feed, externalID string, priority int, next time.Time) rss.Article {
	t.Helper()
	a := rss.Article{ExternalID: externalID, URL: "https://example.com/" + externalID, Title: "T", Excerpt: "E"}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), feed, []rss.Article{a}); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feed.ID, externalID).Scan(&a.ID); err != nil {
		t.Fatalf("select article: %v", err)
	}
	setEnrichJob(t, db, a.ID, priority, next)
	return a
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

	lonely := newEnrichJob(t, db, unsubscribed, "lonely", 0, longAgo.Add(-time.Hour))
	backlog := newEnrichJob(t, db, subscribed, "backlog", 1, longAgo.Add(-time.Hour))
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

	// Priority 0 comes first, by next_attempt_at; feeds without subscribers
	// and jobs not yet due are skipped.
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
	feed := newTestFeed(t, db)
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
