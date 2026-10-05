package psql

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

const (
	testDoneTTL   = 24 * time.Hour
	testFailedTTL = time.Hour
	missingID     = "00000000-0000-0000-0000-000000000000"
)

// newLookupKey returns a unique site key whose lookup row is deleted when the
// test ends.
func newLookupKey(t *testing.T, repo *LookupRepository) string {
	t.Helper()
	key := fmt.Sprintf("lk-%d.example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := repo.DB.Exec(`DELETE FROM lookups WHERE site_key = $1`, key); err != nil {
			t.Errorf("cleanup lookup: %v", err)
		}
	})
	return key
}

// execSQL runs a statement that sets up test state.
func execSQL(t *testing.T, repo *LookupRepository, stmt string, args ...any) {
	t.Helper()
	if _, err := repo.DB.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func getLookup(t *testing.T, repo *LookupRepository, id string) rss.Lookup {
	t.Helper()
	l, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return l
}

// Requires a migrated database; see psqltest.NewDB.
func TestLookupRepositoryRequest(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewLookupRepository(db)

	t.Run("new lookup is pending and requesting again returns it unchanged", func(t *testing.T) {
		key := newLookupKey(t, repo)
		url := "https://" + key + "/"

		l, err := repo.Request(t.Context(), key, url, testDoneTTL, testFailedTTL)
		if err != nil {
			t.Fatalf("Request: %v", err)
		}
		if l.ID == "" || l.SiteKey != key || l.URL != url || l.Status != rss.LookupPending {
			t.Errorf("got %+v; want a pending lookup for %q", l, key)
		}
		if l.Attempts != 0 || l.SiteID != "" || l.Error != "" || l.RequestedAt.IsZero() || !l.StartedAt.IsZero() || !l.FinishedAt.IsZero() {
			t.Errorf("got %+v; want a fresh lookup", l)
		}

		again, err := repo.Request(t.Context(), key, url, testDoneTTL, testFailedTTL)
		if err != nil {
			t.Fatalf("Request again: %v", err)
		}
		if again.ID != l.ID || again.Status != rss.LookupPending || !again.RequestedAt.Equal(l.RequestedAt) {
			t.Errorf("got %+v; want unchanged %+v", again, l)
		}

		byKey, err := repo.GetBySiteKey(t.Context(), key)
		if err != nil {
			t.Fatalf("GetBySiteKey: %v", err)
		}
		if byKey.ID != l.ID {
			t.Errorf("GetBySiteKey id = %q; want %q", byKey.ID, l.ID)
		}
	})

	tests := []struct {
		name       string
		status     rss.LookupStatus
		finishedAt string
		wantStatus rss.LookupStatus
	}{
		{"done past doneTTL is reset", rss.LookupDone, "now() - interval '2 days'", rss.LookupPending},
		{"done within doneTTL is kept", rss.LookupDone, "now() - interval '1 minute'", rss.LookupDone},
		{"failed past failedTTL is reset", rss.LookupFailed, "now() - interval '2 hours'", rss.LookupPending},
		{"failed within failedTTL is kept", rss.LookupFailed, "now() - interval '1 minute'", rss.LookupFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := newLookupKey(t, repo)
			siteID := newTestSite(t, repo.DB)
			l, err := repo.Request(t.Context(), key, "https://"+key+"/", testDoneTTL, testFailedTTL)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			execSQL(t, repo, `UPDATE lookups SET status = $2, site_id = $3, error = 'boom', attempts = 2,
				started_at = `+tt.finishedAt+`, finished_at = `+tt.finishedAt+`,
				requested_at = now() - interval '3 days' WHERE id = $1`,
				l.ID, tt.status, siteID)
			before := getLookup(t, repo, l.ID)

			newURL := "https://www." + key + "/"
			got, err := repo.Request(t.Context(), key, newURL, testDoneTTL, testFailedTTL)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			if got.ID != l.ID || got.Status != tt.wantStatus {
				t.Fatalf("got id %q status %q; want id %q status %q", got.ID, got.Status, l.ID, tt.wantStatus)
			}
			if got.SiteID != siteID {
				t.Errorf("site_id = %q; want %q kept", got.SiteID, siteID)
			}

			if tt.wantStatus != rss.LookupPending {
				if got != before {
					t.Errorf("got %+v; want unchanged %+v", got, before)
				}
				return
			}
			if got.URL != newURL || got.Attempts != 0 || got.Error != "" || !got.StartedAt.IsZero() || !got.FinishedAt.IsZero() {
				t.Errorf("got %+v; want reset with url %q", got, newURL)
			}
			if !got.RequestedAt.After(before.RequestedAt) {
				t.Errorf("requested_at = %v; want after %v", got.RequestedAt, before.RequestedAt)
			}
		})
	}

	t.Run("Get missing returns ErrNoRecord", func(t *testing.T) {
		if _, err := repo.GetByID(t.Context(), missingID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByID err = %v; want ErrNoRecord", err)
		}
		if _, err := repo.GetBySiteKey(t.Context(), "missing.invalid"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetBySiteKey err = %v; want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestLookupRepositoryClaimNext(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewLookupRepository(db)

	t.Run("claims the oldest pending lookup", func(t *testing.T) {
		key := newLookupKey(t, repo)
		l, err := repo.Request(t.Context(), key, "https://"+key+"/", testDoneTTL, testFailedTTL)
		if err != nil {
			t.Fatalf("Request: %v", err)
		}
		// Other tests share the database, so make this row the oldest.
		execSQL(t, repo, `UPDATE lookups SET requested_at = '2000-01-01' WHERE id = $1`, l.ID)

		got, err := repo.ClaimNext(t.Context())
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if got.ID != l.ID {
			t.Fatalf("claimed %q; want %q", got.ID, l.ID)
		}
		if got.Status != rss.LookupRunning || got.Attempts != 1 || got.StartedAt.IsZero() {
			t.Errorf("got %+v; want running with attempts 1 and started_at set", got)
		}
	})

	t.Run("concurrent claims never return the same lookup", func(t *testing.T) {
		var ids []string
		for range 2 {
			key := newLookupKey(t, repo)
			l, err := repo.Request(t.Context(), key, "https://"+key+"/", testDoneTTL, testFailedTTL)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			execSQL(t, repo, `UPDATE lookups SET requested_at = '2000-01-01' WHERE id = $1`, l.ID)
			ids = append(ids, l.ID)
		}

		var wg sync.WaitGroup
		got := make([]rss.Lookup, 2)
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
		if got[0].ID == got[1].ID {
			t.Errorf("both claims returned %q", got[0].ID)
		}
		// Hand back anything claimed that this test didn't create, so other
		// tests' rows aren't left running.
		for _, l := range got {
			if l.ID != ids[0] && l.ID != ids[1] {
				execSQL(t, repo, `UPDATE lookups SET status = 'pending', attempts = attempts - 1, started_at = NULL WHERE id = $1`, l.ID)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestLookupRepositoryFinishAndFail(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewLookupRepository(db)

	newRunning := func(t *testing.T) rss.Lookup {
		t.Helper()
		key := newLookupKey(t, repo)
		l, err := repo.Request(t.Context(), key, "https://"+key+"/", testDoneTTL, testFailedTTL)
		if err != nil {
			t.Fatalf("Request: %v", err)
		}
		execSQL(t, repo, `UPDATE lookups SET status = 'running', started_at = now(), attempts = 1 WHERE id = $1`, l.ID)
		return l
	}

	t.Run("Finish with a site sets done and site_id", func(t *testing.T) {
		l := newRunning(t)
		siteID := newTestSite(t, repo.DB)
		if err := repo.Finish(t.Context(), l.ID, siteID); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		got := getLookup(t, repo, l.ID)
		if got.Status != rss.LookupDone || got.SiteID != siteID || got.FinishedAt.IsZero() {
			t.Errorf("got %+v; want done with site_id %q and finished_at set", got, siteID)
		}
	})

	t.Run("Finish without a site keeps an existing site_id", func(t *testing.T) {
		l := newRunning(t)
		siteID := newTestSite(t, repo.DB)
		execSQL(t, repo, `UPDATE lookups SET site_id = $2, error = 'old' WHERE id = $1`, l.ID, siteID)
		if err := repo.Finish(t.Context(), l.ID, ""); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		got := getLookup(t, repo, l.ID)
		if got.Status != rss.LookupDone || got.SiteID != siteID || got.Error != "" {
			t.Errorf("got %+v; want done with site_id %q kept and error cleared", got, siteID)
		}
	})

	t.Run("Finish without a site leaves site_id empty", func(t *testing.T) {
		l := newRunning(t)
		if err := repo.Finish(t.Context(), l.ID, ""); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if got := getLookup(t, repo, l.ID); got.Status != rss.LookupDone || got.SiteID != "" {
			t.Errorf("got %+v; want done without site_id", got)
		}
	})

	t.Run("Fail sets failed and the error", func(t *testing.T) {
		l := newRunning(t)
		if err := repo.Fail(t.Context(), l.ID, "fetch: timeout"); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		got := getLookup(t, repo, l.ID)
		if got.Status != rss.LookupFailed || got.Error != "fetch: timeout" || got.FinishedAt.IsZero() {
			t.Errorf("got %+v; want failed with error and finished_at set", got)
		}
	})

	t.Run("Fail truncates long messages", func(t *testing.T) {
		l := newRunning(t)
		if err := repo.Fail(t.Context(), l.ID, strings.Repeat("é", maxLookupErrorLen)); err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if got := getLookup(t, repo, l.ID); len(got.Error) != maxLookupErrorLen {
			t.Errorf("len(error) = %d; want %d", len(got.Error), maxLookupErrorLen)
		}
	})

	t.Run("missing ID returns ErrNoRecord", func(t *testing.T) {
		if err := repo.Finish(t.Context(), missingID, ""); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Finish err = %v; want ErrNoRecord", err)
		}
		if err := repo.Fail(t.Context(), missingID, "x"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Fail err = %v; want ErrNoRecord", err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestLookupRepositoryResetStale(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewLookupRepository(db)

	newRunning := func(t *testing.T, startedAt string, attempts int) string {
		t.Helper()
		key := newLookupKey(t, repo)
		l, err := repo.Request(t.Context(), key, "https://"+key+"/", testDoneTTL, testFailedTTL)
		if err != nil {
			t.Fatalf("Request: %v", err)
		}
		execSQL(t, repo, `UPDATE lookups SET status = 'running', started_at = `+startedAt+`, attempts = $2 WHERE id = $1`, l.ID, attempts)
		return l.ID
	}

	retry := newRunning(t, "now() - interval '10 minutes'", 1)
	giveUp := newRunning(t, "now() - interval '10 minutes'", 3)
	fresh := newRunning(t, "now() - interval '1 minute'", 1)

	n, err := repo.ResetStale(t.Context(), 5*time.Minute, 3)
	if err != nil {
		t.Fatalf("ResetStale: %v", err)
	}
	// Rows from other tests may also be reset, so only check a lower bound.
	if n < 2 {
		t.Errorf("ResetStale changed %d rows; want at least 2", n)
	}

	if got := getLookup(t, repo, retry); got.Status != rss.LookupPending || got.Attempts != 1 || !got.StartedAt.IsZero() {
		t.Errorf("retry: got %+v; want pending with attempts 1 and started_at cleared", got)
	}
	if got := getLookup(t, repo, giveUp); got.Status != rss.LookupFailed || got.Error == "" || got.FinishedAt.IsZero() {
		t.Errorf("give up: got %+v; want failed with an error and finished_at set", got)
	}
	if got := getLookup(t, repo, fresh); got.Status != rss.LookupRunning {
		t.Errorf("fresh: got %+v; want still running", got)
	}
}
