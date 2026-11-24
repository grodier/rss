package psql

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// The queues page's tests read whole queues (Counts, Failures), so none of
// this file's tests is parallel.

// queueLookup inserts a lookup with a unique site key, status, error and
// finished_at (requested_at too), and returns its ID; it's deleted when the
// test ends, unless a test deleted it.
func queueLookup(t *testing.T, db *sql.DB, status, errText string, at time.Time) string {
	t.Helper()
	key := fmt.Sprintf("queues-%d.example.com", psqltest.Unique())
	var id string
	if err := db.QueryRow(`INSERT INTO lookups (site_key, url, status, error, attempts, requested_at, started_at, finished_at)
		VALUES ($1, 'https://' || $1 || '/', $2, $3, 2, $4, $4, CASE WHEN $2 IN ('done', 'failed') THEN $4::timestamptz END)
		RETURNING id`, key, status, errText, at).Scan(&id); err != nil {
		t.Fatalf("insert lookup: %v", err)
	}
	cleanupExec(t, db, `DELETE FROM lookups WHERE id = $1`, id)
	return id
}

// setImportFeed sets import feed position of importID's status, failure,
// error and finished_at.
func setImportFeed(t *testing.T, db *sql.DB, importID string, position int, status, failure, errText string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE import_feeds SET status = $3, failure = $4, error = $5, attempts = 3, started_at = $6, finished_at = $6
		WHERE import_id = $1 AND position = $2`, importID, position, status, failure, errText, at); err != nil {
		t.Fatalf("update import feed: %v", err)
	}
}

// setEnrichFailure sets article articleID's enrichment job's attempts and
// last error.
func setEnrichFailure(t *testing.T, db *sql.DB, articleID string, attempts int, errText string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE enrich_jobs SET attempts = $2, last_error = $3 WHERE article_id = $1`, articleID, attempts, errText); err != nil {
		t.Fatalf("update enrich job: %v", err)
	}
}

// failuresOf returns repo's failures of queue whose IDs are in ids, in
// order.
func failuresOf(t *testing.T, repo *AdminQueuesRepository, queue string, ids ...string) []rss.QueueFailure {
	t.Helper()
	all, err := repo.Failures(t.Context(), queue)
	if err != nil {
		t.Fatalf("Failures(%s): %v", queue, err)
	}
	return slices.DeleteFunc(all, func(f rss.QueueFailure) bool { return !slices.Contains(ids, f.ID) })
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminQueuesRepositoryCounts(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminQueuesRepository(db)
	now := dbNow(t, db)
	longAgo := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	before, err := repo.Counts(t.Context())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}

	// Lookups: pending, running, failed, done.
	queueLookup(t, db, "pending", "", longAgo)
	queueLookup(t, db, "running", "", now)
	queueLookup(t, db, "failed", "status 500", now)
	queueLookup(t, db, "done", "", now)

	// Imports: pending, running, failed, subscribed.
	user := newTestUser(t, db)
	importID := newTestImport(t, NewImportRepository(db), user, "https://a.example/", "https://b.example/", "https://c.example/", "https://d.example/")
	exec(`UPDATE imports SET created_at = $2 WHERE id = $1`, importID, longAgo)
	setImportFeed(t, db, importID, 1, "running", "", "", now)
	setImportFeed(t, db, importID, 2, "failed", "unreachable", "status 500", now)
	setImportFeed(t, db, importID, 3, "subscribed", "", "", now)

	// Enrichment, on a subscribed feed: due, failed and waiting, on its
	// first attempt's lease (not failed), and not due.
	feed := newSubscribedFeed(t, db, user)
	newEnrichJob(t, db, feed, "queues-due", 0, longAgo)
	failed := newEnrichJob(t, db, feed, "queues-failed", 0, later)
	setEnrichFailure(t, db, failed.ID, 2, "status 503")
	leased := newEnrichJob(t, db, feed, "queues-leased", 0, later)
	setEnrichFailure(t, db, leased.ID, 1, "")

	// Icons: never checked and due an hour ago (overdue), never checked
	// and due a minute ago (not overdue).
	exec(`UPDATE sites SET icon_checked_at = NULL, icon_next_check_at = $2 WHERE id = $1`, feed.SiteID, longAgo)
	exec(`UPDATE sites SET icon_checked_at = NULL, icon_next_check_at = now() - interval '1 minute' WHERE id = $1`, newTestSite(t, db))

	// Emails: pending and due, failed, sent.
	addr := fmt.Sprintf("queues-test-%d@example.com", psqltest.Unique())
	exec(`INSERT INTO emails (to_address, subject, text_body, html_body, status, next_attempt_at)
		VALUES ($1, 's', '', '', 'pending', $2), ($1, 's', '', '', 'failed', $2), ($1, 's', '', '', 'sent', $2)`, addr, longAgo)
	cleanupExec(t, db, `DELETE FROM emails WHERE to_address = $1`, addr)

	after, err := repo.Counts(t.Context())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}

	want := []rss.AdminQueueCounts{
		{Name: "lookup", Pending: 1, Running: 1, Failed: 1},
		{Name: "import", Pending: 1, Running: 1, Failed: 1},
		{Name: "enrich", Pending: 1, Failed: 1},
		{Name: "icon", Pending: 2, Overdue: 1},
		{Name: "mail", Pending: 1, Failed: 1},
	}
	if len(before) != len(want) || len(after) != len(want) {
		t.Fatalf("Counts returned %d then %d queues, want %d", len(before), len(after), len(want))
	}
	for i, w := range want {
		b, a := before[i], after[i]
		got := rss.AdminQueueCounts{Name: a.Name, Pending: a.Pending - b.Pending, Running: a.Running - b.Running, Failed: a.Failed - b.Failed, Overdue: a.Overdue - b.Overdue}
		if got != w {
			t.Errorf("queue %d grew by %+v, want %+v", i, got, w)
		}
		if !a.Oldest.Equal(longAgo) {
			t.Errorf("%s's oldest = %v, want %v", w.Name, a.Oldest, longAgo)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB. Other tests' runners
// share job_metrics, so it writes in a day long ago, with outcomes of its
// own.
func TestAdminQueuesRepositoryJobsPerHour(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminQueuesRepository(db)
	now := time.Date(2002, 3, 4, 12, 30, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	tag := fmt.Sprintf("test-%d", psqltest.Unique())
	cleanupExec(t, db, `DELETE FROM job_metrics WHERE outcome = $1`, tag)

	insert := func(at time.Time, instance, runner, resolution string, jobs int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO job_metrics (period_start, resolution, instance, runner, outcome, jobs, duration_ms_sum, buckets)
			VALUES ($1, $2, $3, $4, $5, $6, 0, '{}')`, at, resolution, instance, runner, tag, jobs); err != nil {
			t.Fatalf("insert job_metrics: %v", err)
		}
	}
	insert(now.Add(-time.Minute), "a", "lookup", "minute", 2)
	insert(now.Add(-59*time.Minute), "b", "lookup", "minute", 3)
	insert(now.Add(-61*time.Minute), "a", "lookup", "minute", 4)
	insert(now.Add(-23*time.Hour-59*time.Minute), "a", "enrich", "minute", 5)
	// Outside the window, and an hourly rollup.
	insert(now, "a", "lookup", "minute", 100)
	insert(since.Add(-time.Minute), "a", "lookup", "minute", 100)
	insert(now.Add(-2*time.Hour).Truncate(time.Hour), "", "lookup", "hour", 100)

	got, err := repo.JobsPerHour(t.Context(), since, now)
	if err != nil {
		t.Fatalf("JobsPerHour: %v", err)
	}
	want := []rss.JobsHour{
		{Runner: "enrich", End: now.Add(-23 * time.Hour), Jobs: 5},
		{Runner: "lookup", End: now.Add(-time.Hour), Jobs: 4},
		{Runner: "lookup", End: now, Jobs: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("JobsPerHour = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Runner != want[i].Runner || !got[i].End.Equal(want[i].End) || got[i].Jobs != want[i].Jobs {
			t.Errorf("hour %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminQueuesRepositoryFailures(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminQueuesRepository(db)
	now := dbNow(t, db)

	t.Run("lookups", func(t *testing.T) {
		older := queueLookup(t, db, "failed", "status 500", now.Add(-time.Hour))
		newer := queueLookup(t, db, "failed", "no such host", now)
		done := queueLookup(t, db, "done", "", now)
		site := newTestSite(t, db)
		if _, err := db.Exec(`UPDATE lookups SET site_id = $2 WHERE id = $1`, newer, site); err != nil {
			t.Fatal(err)
		}
		got := failuresOf(t, repo, rss.QueueLookup, older, newer, done)
		if len(got) != 2 || got[0].ID != newer || got[1].ID != older {
			t.Fatalf("failures = %+v, want %s then %s", got, newer, older)
		}
		if f := got[0]; f.Error != "no such host" || f.SiteID != site || !f.At.Equal(now) || f.URL == "" {
			t.Errorf("newer failure = %+v, want its error, site, time and URL", f)
		}
	})

	t.Run("import feeds", func(t *testing.T) {
		user := newTestUser(t, db)
		importID := newTestImport(t, NewImportRepository(db), user, "https://a.example/feed", "https://b.example/", "https://c.example/feed")
		feed := newTestFeed(t, db)
		setImportFeed(t, db, importID, 0, "failed", "unreachable", "status 500", now)
		setImportFeed(t, db, importID, 1, "failed", "not_feed", "", now.Add(-time.Minute))
		if _, err := db.Exec(`UPDATE import_feeds SET feed_id = $2 WHERE import_id = $1 AND position = 1`, importID, feed.ID); err != nil {
			t.Fatal(err)
		}
		var importedAt time.Time
		if err := db.QueryRow(`SELECT created_at FROM imports WHERE id = $1`, importID).Scan(&importedAt); err != nil {
			t.Fatal(err)
		}
		got := failuresOf(t, repo, rss.QueueImport, importID+".0", importID+".1", importID+".2")
		if len(got) != 2 || got[0].ID != importID+".0" || got[1].ID != importID+".1" {
			t.Fatalf("failures = %+v, want positions 0 and 1", got)
		}
		if f := got[0]; f.Failure != "unreachable" || f.Error != "status 500" || f.URL != "https://a.example/feed" || !f.ImportedAt.Equal(importedAt) || f.FeedID != "" {
			t.Errorf("position 0 = %+v, want unreachable, its error, URL and import's date", f)
		}
		if f := got[1]; f.Failure != "not_feed" || f.FeedID != feed.ID {
			t.Errorf("position 1 = %+v, want not_feed with its feed", f)
		}
	})

	t.Run("enrichment jobs", func(t *testing.T) {
		feed := newTestFeed(t, db)
		failed := newEnrichJob(t, db, feed, "failures-failed", 0, now)
		setEnrichFailure(t, db, failed.ID, 2, "status 503")
		leased := newEnrichJob(t, db, feed, "failures-leased", 0, now)
		setEnrichFailure(t, db, leased.ID, 1, "")
		fresh := newEnrichJob(t, db, feed, "failures-fresh", 0, now)
		got := failuresOf(t, repo, rss.QueueEnrich, failed.ID, leased.ID, fresh.ID)
		if len(got) != 1 || got[0].ID != failed.ID || got[0].Error != "status 503" || got[0].FeedID != feed.ID || got[0].URL != failed.URL || got[0].At.IsZero() {
			t.Errorf("failures = %+v, want only %s with its error, feed, URL and time", got, failed.ID)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminQueuesRepositoryRetry(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminQueuesRepository(db)
	now := dbNow(t, db)
	longAgo := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("lookups", func(t *testing.T) {
		by := testActor(t, db)
		a := queueLookup(t, db, "failed", "status 500", longAgo)
		b := queueLookup(t, db, "failed", "status 404", longAgo)
		running := queueLookup(t, db, "running", "", longAgo)
		done := queueLookup(t, db, "done", "", longAgo)

		n, err := repo.Retry(t.Context(), rss.QueueLookup, []string{a, b, running, done}, "status <n>", by)
		if err != nil || n != 2 {
			t.Fatalf("Retry = %d, %v; want 2", n, err)
		}
		for _, id := range []string{a, b} {
			var status, errText string
			var attempts int
			var requested time.Time
			var finished sql.NullTime
			if err := db.QueryRow(`SELECT status, error, attempts, requested_at, finished_at FROM lookups WHERE id = $1`, id).Scan(&status, &errText, &attempts, &requested, &finished); err != nil {
				t.Fatal(err)
			}
			if status != "pending" || errText != "" || attempts != 0 || requested.Before(now) || finished.Valid {
				t.Errorf("retried lookup = %s %q, %d attempts, requested %v, finished %v; want pending, cleared, requested now", status, errText, attempts, requested, finished)
			}
		}
		if s := countRows(t, db, `SELECT count(*) FROM lookups WHERE id IN ($1, $2) AND attempts = 2 AND requested_at = $3`, running, done, longAgo); s != 2 {
			t.Errorf("Retry changed %d of the running and done lookups", 2-s)
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "queue.retry" || events[0].TargetType != "lookup" || events[0].TargetID != "" ||
			events[0].After["count"] != float64(2) || events[0].After["group"] != "status <n>" {
			t.Errorf("audit = %+v, want one queue.retry of the group with the count 2", events)
		}

		// Again: nothing's failed any more, so nothing to record.
		if n, err := repo.Retry(t.Context(), rss.QueueLookup, []string{a, b}, "status <n>", by); err != nil || n != 0 {
			t.Errorf("Retry again = %d, %v; want 0", n, err)
		}
		if got := len(auditBy(t, db, by)); got != 1 {
			t.Errorf("audit entries after retrying nothing = %d, want 1", got)
		}
	})

	t.Run("one import feed", func(t *testing.T) {
		by := testActor(t, db)
		user := newTestUser(t, db)
		importID := newTestImport(t, NewImportRepository(db), user, "https://a.example/", "https://b.example/")
		setImportFeed(t, db, importID, 0, "failed", "unreachable", "status 500", now)
		setImportFeed(t, db, importID, 1, "subscribed", "", "", now)

		n, err := repo.Retry(t.Context(), rss.QueueImport, []string{importID + ".0"}, "", by)
		if err != nil || n != 1 {
			t.Fatalf("Retry = %d, %v; want 1", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM import_feeds WHERE import_id = $1 AND position = 0
			AND status = 'pending' AND attempts = 0 AND failure = '' AND error = '' AND finished_at IS NULL`, importID); got != 1 {
			t.Error("the retried import feed isn't pending with its failure cleared")
		}
		// Not failed, so left alone.
		if n, err := repo.Retry(t.Context(), rss.QueueImport, []string{importID + ".1"}, "", by); err != nil || n != 0 {
			t.Errorf("Retry of a subscribed feed = %d, %v; want 0", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM import_feeds WHERE import_id = $1 AND position = 1 AND status = 'subscribed'`, importID); got != 1 {
			t.Error("Retry changed a subscribed import feed")
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "queue.retry" || events[0].TargetType != "import_feed" || events[0].TargetID != importID+".0" ||
			events[0].After["count"] != float64(1) || events[0].After["group"] != nil {
			t.Errorf("audit = %+v, want one queue.retry of the import feed", events)
		}
		if _, err := repo.Retry(t.Context(), rss.QueueImport, []string{importID}, "", by); err == nil {
			t.Error("Retry of an import feed ID without its position succeeded")
		}
	})

	t.Run("enrichment jobs", func(t *testing.T) {
		by := testActor(t, db)
		feed := newTestFeed(t, db)
		failed := newEnrichJob(t, db, feed, "retry-failed", 0, now.Add(time.Hour))
		setEnrichFailure(t, db, failed.ID, 2, "status 503")
		leased := newEnrichJob(t, db, feed, "retry-leased", 0, now.Add(time.Hour))
		setEnrichFailure(t, db, leased.ID, 1, "")

		n, err := repo.Retry(t.Context(), rss.QueueEnrich, []string{failed.ID, leased.ID}, "status <n>", by)
		if err != nil || n != 1 {
			t.Fatalf("Retry = %d, %v; want 1", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM enrich_jobs WHERE article_id = $1 AND next_attempt_at <= now() AND attempts = 2`, failed.ID); got != 1 {
			t.Error("the retried enrichment job isn't due now")
		}
		if got := countRows(t, db, `SELECT count(*) FROM enrich_jobs WHERE article_id = $1 AND next_attempt_at > now()`, leased.ID); got != 1 {
			t.Error("Retry changed a job on its first attempt's lease")
		}
		if events := auditBy(t, db, by); len(events) != 1 || events[0].TargetType != "enrich_job" || events[0].After["count"] != float64(1) {
			t.Errorf("audit = %+v, want one queue.retry of an enrichment job", events)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminQueuesRepositoryDelete(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminQueuesRepository(db)
	now := dbNow(t, db)

	t.Run("a lookup", func(t *testing.T) {
		by := testActor(t, db)
		failed := queueLookup(t, db, "failed", "status 500", now)
		done := queueLookup(t, db, "done", "", now)

		n, err := repo.Delete(t.Context(), rss.QueueLookup, []string{failed}, "", by)
		if err != nil || n != 1 {
			t.Fatalf("Delete = %d, %v; want 1", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM lookups WHERE id = $1`, failed); got != 0 {
			t.Error("the failed lookup is still there")
		}
		if n, err := repo.Delete(t.Context(), rss.QueueLookup, []string{done}, "", by); err != nil || n != 0 {
			t.Errorf("Delete of a done lookup = %d, %v; want 0", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM lookups WHERE id = $1`, done); got != 1 {
			t.Error("Delete deleted a done lookup")
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "queue.delete" || events[0].TargetType != "lookup" || events[0].TargetID != failed || events[0].After["count"] != float64(1) {
			t.Errorf("audit = %+v, want one queue.delete of the lookup", events)
		}
	})

	t.Run("enrichment jobs", func(t *testing.T) {
		by := testActor(t, db)
		feed := newTestFeed(t, db)
		a := newEnrichJob(t, db, feed, "delete-a", 0, now)
		setEnrichFailure(t, db, a.ID, 2, "status 503")
		b := newEnrichJob(t, db, feed, "delete-b", 0, now)
		setEnrichFailure(t, db, b.ID, 3, "status 502")
		fresh := newEnrichJob(t, db, feed, "delete-fresh", 0, now)

		n, err := repo.Delete(t.Context(), rss.QueueEnrich, []string{a.ID, b.ID, fresh.ID}, "status <n>", by)
		if err != nil || n != 2 {
			t.Fatalf("Delete = %d, %v; want 2", n, err)
		}
		if got := countRows(t, db, `SELECT count(*) FROM enrich_jobs WHERE article_id = ANY(ARRAY[$1, $2, $3]::uuid[])`, a.ID, b.ID, fresh.ID); got != 1 {
			t.Errorf("%d jobs left, want the one not failed", got)
		}
		if got := countRows(t, db, `SELECT count(*) FROM articles WHERE id = $1`, a.ID); got != 1 {
			t.Error("deleting the job deleted its article")
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "queue.delete" || events[0].TargetType != "enrich_job" || events[0].After["group"] != "status <n>" {
			t.Errorf("audit = %+v, want one queue.delete of the group", events)
		}
	})

	t.Run("import feeds can't be", func(t *testing.T) {
		if _, err := repo.Delete(t.Context(), rss.QueueImport, []string{"x.0"}, "", testActor(t, db)); err == nil {
			t.Error("Delete of import feeds succeeded")
		}
	})
}
