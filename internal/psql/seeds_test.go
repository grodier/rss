package psql

import (
	"reflect"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
)

func seedEntries(keys ...string) []seed.Entry {
	entries := make([]seed.Entry, len(keys))
	for i, key := range keys {
		entries[i] = seed.Entry{URL: "https://" + key + "/", SiteKey: key}
	}
	return entries
}

// Requires a migrated database; see psqltest.NewDB.
func TestSeedRepositoryQueue(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSeedRepository(db)
	lookups := NewLookupRepository(db)
	ctx := t.Context()

	t.Run("new sites are queued behind readers, once", func(t *testing.T) {
		a, b := newLookupKey(t, lookups), newLookupKey(t, lookups)
		res, err := repo.Queue(ctx, seedEntries(a, b), testFailedTTL)
		if err != nil {
			t.Fatal(err)
		}
		if want := (seed.Result{Queued: 2}); !reflect.DeepEqual(res, want) {
			t.Errorf("first Queue = %+v; want %+v", res, want)
		}
		for _, key := range []string{a, b} {
			got, err := lookups.GetBySiteKey(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != rss.LookupPending || got.URL != "https://"+key+"/" {
				t.Errorf("%s: status %q, url %q; want pending, https://%s/", key, got.Status, got.URL, key)
			}
			if p := lookupPriority(t, db, key); p != 1 {
				t.Errorf("%s: priority %d; want 1", key, p)
			}
		}

		res, err = repo.Queue(ctx, seedEntries(a, b), testFailedTTL)
		if err != nil {
			t.Fatal(err)
		}
		if want := (seed.Result{}); !reflect.DeepEqual(res, want) {
			t.Errorf("second Queue = %+v; want nothing queued", res)
		}
	})

	t.Run("failed lookups are retried after retryAfter, others left alone", func(t *testing.T) {
		oldFailed, oldEmpty, recentEmpty := newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
		found, foundThenFailed, running := newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
		site := newTestSite(t, db)

		// Each lookup's outcome, as the runner records it, by a reader
		// (priority 0) and with another address.
		set := func(key, status, siteID, finishedAgo string) {
			t.Helper()
			execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, site_id, attempts, started_at, finished_at)
				VALUES ($1, 'https://' || $1 || '/old', $2, NULLIF($3, '')::uuid, 1, now() - $4::interval,
					CASE WHEN $2 = 'running' THEN NULL ELSE now() - $4::interval END)`, key, status, siteID, finishedAgo)
		}
		set(oldFailed, "failed", "", "2 hours")
		set(oldEmpty, "done", "", "2 hours")
		set(recentEmpty, "done", "", "10 minutes")
		set(found, "done", site, "2 hours")
		set(foundThenFailed, "failed", site, "2 hours")
		set(running, "running", "", "2 hours")
		keys := []string{oldFailed, oldEmpty, recentEmpty, found, foundThenFailed, running}
		before := map[string]rss.Lookup{}
		for _, key := range keys {
			l, err := lookups.GetBySiteKey(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			before[key] = l
		}

		res, err := repo.Queue(ctx, seedEntries(keys...), testFailedTTL)
		if err != nil {
			t.Fatal(err)
		}
		if res.Queued != 0 {
			t.Errorf("Queued = %d; want 0", res.Queued)
		}
		retried := slices.Clone(res.Retried)
		slices.Sort(retried)
		want := []string{oldFailed, oldEmpty}
		slices.Sort(want)
		if !reflect.DeepEqual(retried, want) {
			t.Errorf("Retried = %v; want %v", res.Retried, want)
		}
		if want := []string{recentEmpty}; !reflect.DeepEqual(res.Missing, want) {
			t.Errorf("Missing = %v; want %v", res.Missing, want)
		}

		for _, key := range []string{oldFailed, oldEmpty} {
			got, err := lookups.GetBySiteKey(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != before[key].ID || got.Status != rss.LookupPending || got.URL != "https://"+key+"/" || got.Attempts != 0 || got.Error != "" {
				t.Errorf("%s: %+v; want the same row pending with the seed's address, no attempts and no error", key, got)
			}
			if p := lookupPriority(t, db, key); p != 1 {
				t.Errorf("%s: priority %d; want 1", key, p)
			}
		}
		for _, key := range []string{recentEmpty, found, foundThenFailed, running} {
			got, err := lookups.GetBySiteKey(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != before[key].Status || got.URL != before[key].URL || !got.RequestedAt.Equal(before[key].RequestedAt) {
				t.Errorf("%s: %+v; want it unchanged: %+v", key, got, before[key])
			}
			if p := lookupPriority(t, db, key); p != 0 {
				t.Errorf("%s: priority %d; want 0 (a reader's)", key, p)
			}
		}
	})

	t.Run("an empty list does nothing", func(t *testing.T) {
		res, err := repo.Queue(ctx, nil, testFailedTTL)
		if err != nil || !reflect.DeepEqual(res, seed.Result{}) {
			t.Errorf("Queue(nil) = %+v, %v; want nothing", res, err)
		}
	})
}
