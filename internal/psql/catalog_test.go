package psql

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/grodier/rss/internal/catalog"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// keepCatalog snapshots catalog_sites and restores it when the test ends, so
// tests can replace the whole catalog, even on a development database whose
// www stored the real one. It also deletes the lookups of keys when the test
// ends. Tests that use it mustn't run in parallel.
func keepCatalog(t *testing.T, db *sql.DB, keys ...string) {
	t.Helper()
	var saved []catalog.Entry
	rows, err := db.Query(`SELECT site_key, url, topic, position FROM catalog_sites`)
	if err != nil {
		t.Fatalf("snapshot catalog: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e catalog.Entry
		if err := rows.Scan(&e.SiteKey, &e.URL, &e.Topic, &e.Position); err != nil {
			t.Fatalf("snapshot catalog: %v", err)
		}
		saved = append(saved, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot catalog: %v", err)
	}

	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM lookups WHERE site_key = ANY($1)`, pq.Array(keys)); err != nil {
			t.Errorf("cleanup lookups: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM catalog_sites`); err != nil {
			t.Errorf("cleanup catalog: %v", err)
		}
		for _, e := range saved {
			if _, err := db.Exec(`INSERT INTO catalog_sites (site_key, url, topic, position) VALUES ($1, $2, $3, $4)`,
				e.SiteKey, e.URL, e.Topic, e.Position); err != nil {
				t.Errorf("restore catalog: %v", err)
			}
		}
	})
}

// catalogKeys returns n unique site keys.
func catalogKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("cat%d-%d.example.com", i, time.Now().UnixNano())
	}
	return keys
}

func catalogEntry(topic, key string, position int) catalog.Entry {
	return catalog.Entry{Topic: topic, URL: "https://" + key + "/", SiteKey: key, Position: position}
}

// lookupFound marks key's lookup done with siteID, as the lookup runner does
// when it finds the site.
func lookupFound(t *testing.T, db *sql.DB, key, siteID string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE lookups SET status = 'done', site_id = $2, finished_at = now() WHERE site_key = $1`, key, siteID); err != nil {
		t.Fatalf("finish lookup: %v", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestCatalogRepositoryReplace(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewCatalogRepository(db)
	lookups := NewLookupRepository(db)
	ctx := t.Context()

	keys := catalogKeys(4)
	keepCatalog(t, db, keys...)
	looked, first, second, fresh := keys[0], keys[1], keys[2], keys[3]

	// looked was looked up (and failed) just before the catalog listed it.
	old, err := lookups.Request(ctx, looked, "https://"+looked+"/old", true, testDoneTTL, testFailedTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := lookups.Fail(ctx, old.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	old = getLookup(t, lookups, old.ID)

	res, err := repo.Replace(ctx, []catalog.Entry{catalogEntry("A", first, 0), catalogEntry("A", looked, 1)}, time.Hour)
	if err != nil {
		t.Fatalf("first Replace: %v", err)
	}
	want := catalog.Result{Queued: 1, Missing: []string{looked}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("first Replace = %+v; want %+v (looked failed too recently to retry)", res, want)
	}

	res, err = repo.Replace(ctx, []catalog.Entry{
		catalogEntry("B", second, 0),
		catalogEntry("B", looked, 1),
		catalogEntry("C", first, 2),
		catalogEntry("C", fresh, 3),
	}, time.Hour)
	if err != nil {
		t.Fatalf("second Replace: %v", err)
	}
	want = catalog.Result{Queued: 2, Missing: []string{looked}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("second Replace = %+v; want %+v (second and fresh queued)", res, want)
	}

	t.Run("only the second set is stored", func(t *testing.T) {
		rows, err := db.Query(`SELECT site_key, topic, position FROM catalog_sites ORDER BY position`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var key, topic string
			var pos int
			if err := rows.Scan(&key, &topic, &pos); err != nil {
				t.Fatal(err)
			}
			got = append(got, fmt.Sprintf("%d %s %s", pos, topic, key))
		}
		want := []string{"0 B " + second, "1 B " + looked, "2 C " + first, "3 C " + fresh}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("catalog_sites = %v; want %v", got, want)
		}
	})

	t.Run("a lookup that failed recently is left unchanged", func(t *testing.T) {
		got, err := lookups.GetBySiteKey(ctx, looked)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != old.ID || got.Status != rss.LookupFailed || got.URL != old.URL || !got.RequestedAt.Equal(old.RequestedAt) {
			t.Errorf("lookup = %+v; want it unchanged: %+v", got, old)
		}
	})

	t.Run("new entries get a pending lookup of their address", func(t *testing.T) {
		for _, key := range []string{first, second, fresh} {
			got, err := lookups.GetBySiteKey(ctx, key)
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			if got.Status != rss.LookupPending || got.URL != "https://"+key+"/" {
				t.Errorf("%s: status %q, url %q; want pending, https://%s/", key, got.Status, got.URL, key)
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestCatalogRepositoryReplaceRetries(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewCatalogRepository(db)
	lookups := NewLookupRepository(db)
	ctx := t.Context()

	keys := catalogKeys(6)
	keepCatalog(t, db, keys...)
	oldFailed, oldEmpty, recentEmpty, found, foundThenFailed, running := keys[0], keys[1], keys[2], keys[3], keys[4], keys[5]

	var entries []catalog.Entry
	for i, key := range keys {
		entries = append(entries, catalogEntry("A", key, i))
	}
	if _, err := repo.Replace(ctx, entries, time.Hour); err != nil {
		t.Fatal(err)
	}
	site := newCatalogSite(t, db, found, "Found")

	// Each lookup's outcome, as the runner records it, and when it finished.
	set := func(key, status, siteID, finishedAgo string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE lookups SET status = $2, site_id = NULLIF($3, '')::uuid, url = 'https://' || site_key || '/old',
				attempts = 1, started_at = now() - $4::interval, finished_at = CASE WHEN $2 = 'running' THEN NULL ELSE now() - $4::interval END
			WHERE site_key = $1`, key, status, siteID, finishedAgo); err != nil {
			t.Fatalf("set lookup %s: %v", key, err)
		}
	}
	set(oldFailed, "failed", "", "2 hours")
	set(oldEmpty, "done", "", "2 hours")
	set(recentEmpty, "done", "", "10 minutes")
	set(found, "done", site, "2 hours")
	set(foundThenFailed, "failed", site, "2 hours")
	set(running, "running", "", "2 hours")
	before := map[string]rss.Lookup{}
	for _, key := range keys {
		l, err := lookups.GetBySiteKey(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		before[key] = l
	}

	res, err := repo.Replace(ctx, entries, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 0 {
		t.Errorf("Queued = %d; want 0", res.Queued)
	}
	retried := map[string]bool{}
	for _, key := range res.Retried {
		retried[key] = true
	}
	if len(res.Retried) != 2 || !retried[oldFailed] || !retried[oldEmpty] {
		t.Errorf("Retried = %v; want %s and %s", res.Retried, oldFailed, oldEmpty)
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
			t.Errorf("%s: %+v; want the same row pending with the catalog's address, no attempts and no error", key, got)
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
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestCatalogRepositoryBrowse(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewCatalogRepository(db)
	ctx := t.Context()

	keys := catalogKeys(5)
	keepCatalog(t, db, keys...)

	if _, err := repo.Replace(ctx, []catalog.Entry{
		catalogEntry("Science", keys[0], 0),
		catalogEntry("Science", keys[1], 1), // not found yet
		catalogEntry("Science", keys[2], 2),
		catalogEntry("Empty", keys[3], 3), // not found yet
		catalogEntry("News", keys[4], 4),
	}, time.Hour); err != nil {
		t.Fatal(err)
	}

	// The site keys[2]'s lookup found is on another host, as when an
	// address redirects.
	site0 := newCatalogSite(t, db, keys[0], "Zebra")
	site2 := newCatalogSite(t, db, "moved-"+keys[2], "Aardvark")
	site4 := newCatalogSite(t, db, keys[4], "")
	lookupFound(t, db, keys[0], site0)
	lookupFound(t, db, keys[2], site2)
	lookupFound(t, db, keys[4], site4)

	topics, err := repo.Browse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, topic := range topics {
		for _, s := range topic.Sites {
			got = append(got, topic.Name+" "+s.ID)
		}
	}
	want := []string{"Science " + site0, "Science " + site2, "News " + site4}
	if !reflect.DeepEqual(got, want) || len(topics) != 2 {
		t.Errorf("Browse = %v in %d topics; want %v in 2", got, len(topics), want)
	}
	if topics[0].Sites[0].Host != keys[0] || topics[0].Sites[0].Title != "Zebra" {
		t.Errorf("first site = %+v; want host %s, title Zebra", topics[0].Sites[0], keys[0])
	}
}

// newCatalogSite creates a site on host, deleted when the test ends.
func newCatalogSite(t *testing.T, db *sql.DB, host, title string) string {
	t.Helper()
	id, err := NewSiteRepository(db).Upsert(t.Context(), rss.Site{Host: host, URL: "https://" + host + "/", Title: title})
	if err != nil {
		t.Fatalf("Upsert site: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM sites WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
	})
	return id
}
