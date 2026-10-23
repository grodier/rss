package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestImport creates an import for userID with a pending feed per URL;
// deleting the user on cleanup deletes it.
func newTestImport(t *testing.T, repo *ImportRepository, userID string, urls ...string) string {
	t.Helper()
	feeds := make([]rss.ImportFeed, len(urls))
	for i, u := range urls {
		feeds[i] = rss.ImportFeed{URL: u, Title: fmt.Sprintf("Feed %d", i)}
	}
	id, err := repo.Create(t.Context(), userID, "subs.opml", feeds)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

// setImportCreatedAt sets import id's created_at; other tests share the
// database, so claim tests make their imports the oldest.
func setImportCreatedAt(t *testing.T, db *sql.DB, id, at string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE imports SET created_at = $2 WHERE id = $1`, id, at); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func getImportFeed(t *testing.T, repo *ImportRepository, importID string, position int) rss.ImportFeed {
	t.Helper()
	_, feeds, err := repo.Get(t.Context(), importID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if position >= len(feeds) {
		t.Fatalf("import %s has %d feeds; want position %d", importID, len(feeds), position)
	}
	return feeds[position]
}

// Requires a migrated database; see psqltest.NewDB.
func TestImportRepositoryCreateAndGet(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)
	userID := newTestUser(t, db)

	id, err := repo.Create(t.Context(), userID, "my feeds.opml", []rss.ImportFeed{
		{URL: "https://a.example.com/feed", Title: "A", Position: 7},
		{URL: "https://b.example.com/feed", Title: ""},
		{URL: "https://c.example.com/feed", Title: "C"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	imp, feeds, err := repo.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if imp.ID != id || imp.UserID != userID || imp.Filename != "my feeds.opml" || imp.CreatedAt.IsZero() {
		t.Errorf("import = %+v; want id %s, user %s, filename and created_at", imp, id, userID)
	}
	want := []rss.ImportFeed{
		{ImportID: id, UserID: userID, Position: 0, URL: "https://a.example.com/feed", Title: "A", Status: rss.ImportPending},
		{ImportID: id, UserID: userID, Position: 1, URL: "https://b.example.com/feed", Title: "", Status: rss.ImportPending},
		{ImportID: id, UserID: userID, Position: 2, URL: "https://c.example.com/feed", Title: "C", Status: rss.ImportPending},
	}
	if !reflect.DeepEqual(feeds, want) {
		t.Errorf("feeds = %+v; want %+v", feeds, want)
	}

	t.Run("an import with no feeds", func(t *testing.T) {
		id, err := repo.Create(t.Context(), userID, "empty.opml", nil)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, feeds, err := repo.Get(t.Context(), id)
		if err != nil || len(feeds) != 0 {
			t.Errorf("Get = %v, %v; want no feeds", feeds, err)
		}
	})

	t.Run("missing import", func(t *testing.T) {
		if _, _, err := repo.Get(t.Context(), missingID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Get = %v; want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestImportRepositoryClaimNext(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)

	// unclaim hands back a claimed row this test didn't create, so other
	// tests' rows aren't left running.
	unclaim := func(t *testing.T, f rss.ImportFeed) {
		t.Helper()
		if _, err := db.Exec(`UPDATE import_feeds SET status = 'pending', attempts = attempts - 1, started_at = NULL
			WHERE import_id = $1 AND position = $2`, f.ImportID, f.Position); err != nil {
			t.Fatalf("unclaim: %v", err)
		}
	}

	t.Run("takes the oldest import first, then the lowest position", func(t *testing.T) {
		userID := newTestUser(t, db)
		newer := newTestImport(t, repo, userID, "https://n.example.com/feed")
		older := newTestImport(t, repo, userID, "https://o0.example.com/feed", "https://o1.example.com/feed")
		setImportCreatedAt(t, db, newer, "2000-01-02")
		setImportCreatedAt(t, db, older, "2000-01-01")

		var got []rss.ImportFeed
		for range 3 {
			f, err := repo.ClaimNext(t.Context())
			if err != nil {
				t.Fatalf("ClaimNext: %v", err)
			}
			got = append(got, f)
		}

		want := []struct {
			importID string
			position int
		}{{older, 0}, {older, 1}, {newer, 0}}
		for i, w := range want {
			if got[i].ImportID != w.importID || got[i].Position != w.position {
				t.Errorf("claim %d = %s/%d; want %s/%d", i, got[i].ImportID, got[i].Position, w.importID, w.position)
			}
		}
		if f := got[0]; f.UserID != userID || f.Status != rss.ImportRunning || f.Attempts != 1 || f.URL != "https://o0.example.com/feed" {
			t.Errorf("claimed %+v; want running with attempts 1, the user and the URL", f)
		}
		var startedAt sql.NullTime
		if err := db.QueryRow(`SELECT started_at FROM import_feeds WHERE import_id = $1 AND position = 0`, older).Scan(&startedAt); err != nil {
			t.Fatal(err)
		}
		if !startedAt.Valid {
			t.Error("started_at not set")
		}
	})

	t.Run("concurrent claims never return the same row", func(t *testing.T) {
		userID := newTestUser(t, db)
		id := newTestImport(t, repo, userID, "https://x0.example.com/feed", "https://x1.example.com/feed")
		setImportCreatedAt(t, db, id, "2000-01-01")

		var wg sync.WaitGroup
		got := make([]rss.ImportFeed, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() {
				got[i], errs[i] = repo.ClaimNext(t.Context())
			})
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("ClaimNext %d: %v", i, err)
			}
		}
		if got[0].ImportID == got[1].ImportID && got[0].Position == got[1].Position {
			t.Errorf("both claims returned %s/%d", got[0].ImportID, got[0].Position)
		}
		for _, f := range got {
			if f.ImportID != id {
				unclaim(t, f)
			}
		}
	})

	t.Run("a row held by another transaction is skipped", func(t *testing.T) {
		userID := newTestUser(t, db)
		id := newTestImport(t, repo, userID, "https://h0.example.com/feed", "https://h1.example.com/feed")
		setImportCreatedAt(t, db, id, "2000-01-01")

		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT 1 FROM import_feeds WHERE import_id = $1 AND position = 0 FOR UPDATE`, id); err != nil {
			t.Fatal(err)
		}

		f, err := repo.ClaimNext(t.Context())
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if f.ImportID != id || f.Position != 1 {
			t.Errorf("claimed %s/%d; want %s/1", f.ImportID, f.Position, id)
			if f.ImportID != id {
				unclaim(t, f)
			}
		}
	})

	t.Run("nothing pending", func(t *testing.T) {
		// Other tests' pending rows may exist; another transaction holding
		// all of them leaves nothing to claim.
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT 1 FROM import_feeds WHERE status = 'pending' FOR UPDATE`); err != nil {
			t.Fatal(err)
		}

		if f, err := repo.ClaimNext(t.Context()); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("ClaimNext = %+v, %v; want ErrNoRecord", f, err)
			if err == nil {
				unclaim(t, f)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestImportRepositoryFinish(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)
	userID := newTestUser(t, db)
	feed := newTestFeed(t, db)
	id := newTestImport(t, repo, userID, "https://a.example.com/feed", "https://b.example.com/feed")

	subscribed := rss.ImportFeed{ImportID: id, Position: 0, Status: rss.ImportSubscribed, FeedID: feed.ID}
	if err := repo.Finish(t.Context(), subscribed); err != nil {
		t.Fatalf("Finish subscribed: %v", err)
	}
	failed := rss.ImportFeed{ImportID: id, Position: 1, Status: rss.ImportFailed, Failure: rss.ImportUnreachable, Error: "dial tcp: timeout"}
	if err := repo.Finish(t.Context(), failed); err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if got := getImportFeed(t, repo, id, 0); got.Status != rss.ImportSubscribed || got.FeedID != feed.ID || got.FeedTitle != feed.Title || got.Failure != "" || got.Error != "" {
		t.Errorf("subscribed: got %+v; want subscribed to %s, titled %q", got, feed.ID, feed.Title)
	}
	if got := getImportFeed(t, repo, id, 1); got.Status != rss.ImportFailed || got.Failure != rss.ImportUnreachable || got.FeedID != "" || got.FeedTitle != "" || got.Error != "dial tcp: timeout" {
		t.Errorf("failed: got %+v; want failed, unreachable, with the error", got)
	}
	var unfinished int
	if err := db.QueryRow(`SELECT count(*) FROM import_feeds WHERE import_id = $1 AND finished_at IS NULL`, id).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if unfinished != 0 {
		t.Errorf("%d rows without finished_at", unfinished)
	}

	t.Run("missing row", func(t *testing.T) {
		err := repo.Finish(t.Context(), rss.ImportFeed{ImportID: id, Position: 9, Status: rss.ImportSubscribed})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Finish = %v; want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestImportRepositoryListByUser(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)
	userID := newTestUser(t, db)
	otherID := newTestUser(t, db)
	feed := newTestFeed(t, db)

	oldest := newTestImport(t, repo, userID, "https://a.example.com/feed")
	setImportCreatedAt(t, db, oldest, "2026-01-01T00:00:00Z")
	middle := newTestImport(t, repo, userID, "https://a.example.com/feed", "https://b.example.com/feed", "https://c.example.com/feed", "https://d.example.com/feed")
	setImportCreatedAt(t, db, middle, "2026-01-02T00:00:00Z")
	newest := newTestImport(t, repo, userID, "https://a.example.com/feed", "https://b.example.com/feed")
	setImportCreatedAt(t, db, newest, "2026-01-03T00:00:00Z")
	newTestImport(t, repo, otherID, "https://a.example.com/feed")

	// middle: one subscribed, one failed, one running, one pending.
	finish := func(f rss.ImportFeed) {
		t.Helper()
		if err := repo.Finish(t.Context(), f); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	finish(rss.ImportFeed{ImportID: middle, Position: 0, Status: rss.ImportSubscribed, FeedID: feed.ID})
	finish(rss.ImportFeed{ImportID: middle, Position: 1, Status: rss.ImportFailed, Failure: rss.ImportNotFeed})
	if _, err := db.Exec(`UPDATE import_feeds SET status = 'running', started_at = now() WHERE import_id = $1 AND position = 2`, middle); err != nil {
		t.Fatal(err)
	}
	// newest: both done, one failed.
	finish(rss.ImportFeed{ImportID: newest, Position: 0, Status: rss.ImportAlreadySubscribed, FeedID: feed.ID})
	finish(rss.ImportFeed{ImportID: newest, Position: 1, Status: rss.ImportFailed, Failure: rss.ImportUnreachable})

	got, err := repo.ListByUser(t.Context(), userID, 10)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	type counts struct {
		ID                     string
		Total, Pending, Failed int
	}
	var gotCounts []counts
	for _, s := range got {
		if s.Import.UserID != userID || s.Import.Filename != "subs.opml" || s.Import.CreatedAt.IsZero() {
			t.Errorf("import = %+v; want user %s's, with filename and created_at", s.Import, userID)
		}
		gotCounts = append(gotCounts, counts{s.Import.ID, s.Total, s.Pending, s.Failed})
	}
	want := []counts{
		{newest, 2, 0, 1},
		{middle, 4, 2, 1},
		{oldest, 1, 1, 0},
	}
	if !reflect.DeepEqual(gotCounts, want) {
		t.Errorf("ListByUser = %+v; want %+v", gotCounts, want)
	}

	t.Run("limit", func(t *testing.T) {
		got, err := repo.ListByUser(t.Context(), userID, 2)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 2 || got[0].Import.ID != newest || got[1].Import.ID != middle {
			t.Errorf("ListByUser(2) = %+v; want newest, middle", got)
		}
	})

	t.Run("no imports", func(t *testing.T) {
		got, err := repo.ListByUser(t.Context(), newTestUser(t, db), 5)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("ListByUser = %v, %v; want empty", got, err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestImportRepositoryResetStale(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)
	userID := newTestUser(t, db)
	id := newTestImport(t, repo, userID, "https://retry.example.com/feed", "https://giveup.example.com/feed", "https://fresh.example.com/feed")

	setRunning := func(position int, startedAt string, attempts int) {
		t.Helper()
		if _, err := db.Exec(`UPDATE import_feeds SET status = 'running', started_at = `+startedAt+`, attempts = $3
			WHERE import_id = $1 AND position = $2`, id, position, attempts); err != nil {
			t.Fatal(err)
		}
	}
	setRunning(0, "now() - interval '10 minutes'", 1)
	setRunning(1, "now() - interval '10 minutes'", 3)
	setRunning(2, "now() - interval '1 minute'", 1)

	n, err := repo.ResetStale(t.Context(), 5*time.Minute, 3)
	if err != nil {
		t.Fatalf("ResetStale: %v", err)
	}
	// Rows from other tests may also be reset, so only check a lower bound.
	if n < 2 {
		t.Errorf("ResetStale changed %d rows; want at least 2", n)
	}

	if got := getImportFeed(t, repo, id, 0); got.Status != rss.ImportPending || got.Failure != "" || got.Attempts != 1 {
		t.Errorf("retry: got %+v; want pending", got)
	}
	if got := getImportFeed(t, repo, id, 1); got.Status != rss.ImportFailed || got.Failure != rss.ImportUnreachable || got.Error == "" {
		t.Errorf("give up: got %+v; want failed, unreachable, with an error", got)
	}
	if got := getImportFeed(t, repo, id, 2); got.Status != rss.ImportRunning {
		t.Errorf("fresh: got %+v; want still running", got)
	}
	var startedAt, finishedAt sql.NullTime
	if err := db.QueryRow(`SELECT started_at, finished_at FROM import_feeds WHERE import_id = $1 AND position = 0`, id).Scan(&startedAt, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if startedAt.Valid || finishedAt.Valid {
		t.Errorf("retry: started_at %v, finished_at %v; want both cleared", startedAt, finishedAt)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestDeletingUserDeletesImports(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewImportRepository(db)
	userID := newTestUser(t, db)
	id := newTestImport(t, repo, userID, "https://a.example.com/feed")

	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, _, err := repo.Get(t.Context(), id); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Get = %v; want ErrNoRecord", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM import_feeds WHERE import_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d import_feeds rows left", n)
	}
}
