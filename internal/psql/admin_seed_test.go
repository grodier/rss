package psql

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
	"github.com/lib/pq"
)

// seedSite adds key to the seed list with address url, added at, and
// deletes it when the test ends.
func seedSite(t *testing.T, db *sql.DB, key, url string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO seed_sites (site_key, url, added_at) VALUES ($1, $2, $3)`, key, url, at); err != nil {
		t.Fatalf("insert seed site: %v", err)
	}
	cleanupExec(t, db, `DELETE FROM seed_sites WHERE site_key = $1`, key)
}

// setLookup gives key a lookup with status, the site siteID ("" for none)
// and error errText, finished now when it's done or failed.
func setLookup(t *testing.T, lookups *LookupRepository, key, status, siteID, errText string) {
	t.Helper()
	execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, site_id, error, attempts, started_at, finished_at)
		VALUES ($1, 'https://' || $1 || '/lookup', $2, NULLIF($3, '')::uuid, $4, 1, now(),
			CASE WHEN $2 IN ('done', 'failed') THEN now() END)`, key, status, siteID, errText)
}

// seedPages reads every page of the seed list under filter, limit at a
// time, following the cursor, and returns the entries whose site keys are
// in keys, in order.
func seedPages(t *testing.T, repo *AdminFeedRepository, filter string, limit int, keys []string) []rss.AdminSeedEntry {
	t.Helper()
	var out []rss.AdminSeedEntry
	seen := map[string]bool{}
	after := ""
	for {
		page, err := repo.SeedList(t.Context(), rss.AdminSeedQuery{Filter: filter, After: after, Limit: limit})
		if err != nil {
			t.Fatalf("SeedList(%s, after %q): %v", filter, after, err)
		}
		if len(page) > limit {
			t.Fatalf("SeedList returned %d entries, over the limit %d", len(page), limit)
		}
		for _, e := range page {
			if seen[e.SiteKey] {
				t.Fatalf("SeedList(%s) repeats %s", filter, e.SiteKey)
			}
			seen[e.SiteKey] = true
			if slices.Contains(keys, e.SiteKey) {
				out = append(out, e)
			}
		}
		if len(page) < limit {
			return out
		}
		after = page[len(page)-1].SiteKey
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositorySeedList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)

	// Created first, so it's deleted after the feeds under it.
	sec := newTestSection(t, db, "Seed", 1)
	foundID, found := adminFeedsSite(t, db)
	adminFeed(t, db, foundID, "In Discover", "")
	putInSection(t, db, adminFeed(t, db, foundID, "Also in Discover", ""), sec)
	notInID, notIn := adminFeedsSite(t, db)
	adminFeed(t, db, notInID, "Not in Discover", "")

	never, pending, running, failed, empty := newLookupKey(t, lookups), newLookupKey(t, lookups),
		newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
	setLookup(t, lookups, found, "done", foundID, "")
	setLookup(t, lookups, notIn, "done", notInID, "")
	setLookup(t, lookups, pending, "pending", "", "")
	setLookup(t, lookups, running, "running", "", "")
	setLookup(t, lookups, failed, "failed", "", "no such host")
	setLookup(t, lookups, empty, "done", "", "")

	// Added in the future, an hour apart, so they're the newest entries;
	// pending and running share a time, so their site keys order them.
	base := time.Now().Add(1000 * time.Hour).Truncate(time.Second)
	at := func(h int) time.Time { return base.Add(-time.Duration(h) * time.Hour) }
	seedSite(t, db, never, "https://"+never+"/", at(0))
	seedSite(t, db, found, "https://"+found+"/feed.xml", at(1))
	seedSite(t, db, failed, "https://"+failed+"/", at(2))
	seedSite(t, db, pending, "https://"+pending+"/", at(3))
	seedSite(t, db, running, "https://"+running+"/", at(3))
	seedSite(t, db, empty, "https://"+empty+"/", at(4))
	seedSite(t, db, notIn, "https://"+notIn+"/", at(5))
	tied := []string{pending, running}
	slices.Sort(tied)
	slices.Reverse(tied) // descending
	order := []string{never, found, failed, tied[0], tied[1], empty, notIn}

	t.Run("newest first, every page", func(t *testing.T) {
		for _, limit := range []int{1, 2, 3, 100} {
			got := seedPages(t, repo, rss.AdminSeedAll, limit, order)
			var keys []string
			for _, e := range got {
				keys = append(keys, e.SiteKey)
			}
			if !slices.Equal(keys, order) {
				t.Errorf("limit %d: entries %v, want %v", limit, keys, order)
			}
		}
		// The first page starts with the newest.
		page, err := repo.SeedList(t.Context(), rss.AdminSeedQuery{Filter: rss.AdminSeedAll, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 || page[0].SiteKey != never || page[1].SiteKey != found {
			t.Errorf("first page = %+v, want %s, %s", page, never, found)
		}
		// After the last entry there's nothing of the test's.
		page, err = repo.SeedList(t.Context(), rss.AdminSeedQuery{Filter: rss.AdminSeedAll, After: notIn, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			if slices.Contains(order, e.SiteKey) {
				t.Errorf("after the last entry: %s", e.SiteKey)
			}
		}
		// A cursor that isn't on the list matches nothing.
		page, err = repo.SeedList(t.Context(), rss.AdminSeedQuery{Filter: rss.AdminSeedAll, After: "nope", Limit: 100})
		if err != nil || len(page) != 0 {
			t.Errorf("after nope = %v, %v; want none", page, err)
		}
	})

	t.Run("filters", func(t *testing.T) {
		want := map[string][]string{
			rss.AdminSeedAll:           order,
			rss.AdminSeedNotFound:      {failed, empty},
			rss.AdminSeedWaiting:       {never, tied[0], tied[1]},
			rss.AdminSeedNotInDiscover: {notIn},
		}
		for filter, w := range want {
			var keys []string
			for _, e := range seedPages(t, repo, filter, 2, order) {
				keys = append(keys, e.SiteKey)
			}
			if !slices.Equal(keys, w) {
				t.Errorf("%s: entries %v, want %v", filter, keys, w)
			}
		}
	})

	t.Run("entries", func(t *testing.T) {
		got := seedPages(t, repo, rss.AdminSeedAll, 100, order)
		byKey := map[string]rss.AdminSeedEntry{}
		for _, e := range got {
			byKey[e.SiteKey] = e
		}
		e := byKey[found]
		if e.URL != "https://"+found+"/feed.xml" || !e.AddedAt.Equal(at(1)) {
			t.Errorf("found entry = %+v, want its address and added at %v", e, at(1))
		}
		if e.State() != rss.SeedFound || e.SiteHost != found || e.Feeds != 2 || e.InDiscover != 1 {
			t.Errorf("found entry: state %s, host %q, %d feeds, %d in Discover; want found, %s, 2, 1",
				e.State(), e.SiteHost, e.Feeds, e.InDiscover, found)
		}
		if e.Lookup.ID == "" || e.Lookup.SiteID != foundID || e.Lookup.SiteKey != found || e.Lookup.FinishedAt.IsZero() {
			t.Errorf("found entry's lookup = %+v, want site %s, finished", e.Lookup, foundID)
		}
		if e := byKey[notIn]; e.State() != rss.SeedFound || e.Feeds != 1 || e.InDiscover != 0 {
			t.Errorf("not in Discover: state %s, %d feeds, %d in Discover; want found, 1, 0", e.State(), e.Feeds, e.InDiscover)
		}
		if e := byKey[never]; e.Lookup.ID != "" || e.Lookup.SiteKey != "" || e.State() != rss.SeedWaiting || e.Feeds != 0 || e.InDiscover != 0 || e.SiteHost != "" {
			t.Errorf("never looked up = %+v, want no lookup and zeros", e)
		}
		if e := byKey[failed]; e.State() != rss.SeedFailed || e.Lookup.Error != "no such host" {
			t.Errorf("failed = %+v, want failed with its error", e)
		}
		states := map[string]string{empty: rss.SeedNoFeeds, pending: rss.SeedWaiting, running: rss.SeedRunning}
		for key, state := range states {
			if byKey[key].State() != state || byKey[key].Lookup.ID == "" {
				t.Errorf("%s: state %s, lookup %q; want %s with a lookup", key, byKey[key].State(), byKey[key].Lookup.ID, state)
			}
		}
	})

	if _, err := repo.SeedList(t.Context(), rss.AdminSeedQuery{Filter: "bogus", Limit: 10}); err == nil {
		t.Error("SeedList with an unknown filter succeeded")
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryCountSeed(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)

	before, err := repo.CountSeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	sec := newTestSection(t, db, "Seed count", 1)
	foundID, found := adminFeedsSite(t, db)
	putInSection(t, db, adminFeed(t, db, foundID, "In Discover", ""), sec)
	notInID, notIn := adminFeedsSite(t, db)
	adminFeed(t, db, notInID, "Not in Discover", "")
	never, pending, running, failed, empty := newLookupKey(t, lookups), newLookupKey(t, lookups),
		newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
	setLookup(t, lookups, found, "done", foundID, "")
	setLookup(t, lookups, notIn, "done", notInID, "")
	setLookup(t, lookups, pending, "pending", "", "")
	setLookup(t, lookups, running, "running", "", "")
	setLookup(t, lookups, failed, "failed", "", "boom")
	setLookup(t, lookups, empty, "done", "", "")
	for _, key := range []string{never, pending, running, failed, empty, found, notIn} {
		seedSite(t, db, key, "https://"+key+"/", time.Now())
	}

	got, err := repo.CountSeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	added := map[string]int{rss.AdminSeedAll: 7, rss.AdminSeedNotFound: 2, rss.AdminSeedWaiting: 3, rss.AdminSeedNotInDiscover: 1}
	for filter, n := range added {
		if got[filter]-before[filter] != n {
			t.Errorf("%s: %d more, want %d (before %d, after %d)", filter, got[filter]-before[filter], n, before[filter], got[filter])
		}
	}
	if len(got) != len(added) {
		t.Errorf("CountSeed = %v, want a count for each filter", got)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryQueueSeedLookup(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)
	by := testActor(t, db)

	t.Run("never looked up", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		u := "https://" + key + "/feed.xml"
		seedSite(t, db, key, u, time.Now())
		l, err := repo.QueueSeedLookup(t.Context(), key, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupPending || l.URL != u || l.SiteKey != key {
			t.Errorf("lookup = %+v, want pending of %s", l, u)
		}
		if p := lookupPriority(t, db, key); p != 0 {
			t.Errorf("priority %d, want a reader's, 0", p)
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "seed.lookup" || events[0].TargetType != "seed" || events[0].TargetID != key ||
			events[0].After["lookup_id"] != l.ID || events[0].After["url"] != u || events[0].After["status"] != "pending" {
			t.Errorf("audit = %+v, want one seed.lookup of %s, lookup %s", events, key, l.ID)
		}
	})

	t.Run("failed a minute ago runs again, with the entry's address", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		u := "https://" + key + "/news"
		seedSite(t, db, key, u, time.Now())
		execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, error, attempts, priority, finished_at)
			VALUES ($1, 'https://' || $1 || '/', 'failed', 'boom', 2, 1, now() - interval '1 minute')`, key)
		l, err := repo.QueueSeedLookup(t.Context(), key, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupPending || l.Error != "" || l.Attempts != 0 || l.URL != u {
			t.Errorf("lookup = %+v, want pending again of %s", l, u)
		}
	})

	t.Run("running is left as it is", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		seedSite(t, db, key, "https://"+key+"/", time.Now())
		execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, attempts, started_at)
			VALUES ($1, 'https://' || $1 || '/', 'running', 1, now())`, key)
		l, err := repo.QueueSeedLookup(t.Context(), key, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupRunning {
			t.Errorf("lookup = %+v, want still running", l)
		}
	})

	t.Run("not on the list", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		before := len(auditBy(t, db, by))
		if _, err := repo.QueueSeedLookup(t.Context(), key, by); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want rss.ErrNoRecord", err)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM lookups WHERE site_key = $1`, key).Scan(&n); err != nil || n != 0 {
			t.Errorf("lookups of %s = %d, %v; want none", key, n, err)
		}
		if after := len(auditBy(t, db, by)); after != before {
			t.Errorf("audit grew from %d to %d, want nothing recorded", before, after)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryRetrySeedLookups(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)
	by := testActor(t, db)

	foundID, found := adminFeedsSite(t, db)
	refailedID, refailed := adminFeedsSite(t, db)
	never, pending, running, failed, empty, unlisted := newLookupKey(t, lookups), newLookupKey(t, lookups),
		newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
	setLookup(t, lookups, found, "done", foundID, "")
	setLookup(t, lookups, refailed, "failed", refailedID, "timeout")
	setLookup(t, lookups, pending, "pending", "", "")
	setLookup(t, lookups, running, "running", "", "")
	setLookup(t, lookups, failed, "failed", "", "boom")
	setLookup(t, lookups, empty, "done", "", "")
	setLookup(t, lookups, unlisted, "failed", "", "boom")

	before, err := repo.CountSeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{never, pending, running, failed, empty, found, refailed} {
		seedSite(t, db, key, "https://"+key+"/seed", time.Now())
	}

	n, err := repo.RetrySeedLookups(t.Context(), by)
	if err != nil {
		t.Fatal(err)
	}
	// The failed, no-feeds and failed-after-finding entries, and any other
	// test's not-found entries listed before this one's.
	if want := 3 + before[rss.AdminSeedNotFound]; n != want {
		t.Errorf("RetrySeedLookups = %d, want %d", n, want)
	}
	for _, key := range []string{failed, empty, refailed} {
		var status, u, errText string
		var priority, attempts int
		var finished sql.NullTime
		if err := db.QueryRow(`SELECT status, url, error, priority, attempts, finished_at FROM lookups WHERE site_key = $1`, key).
			Scan(&status, &u, &errText, &priority, &attempts, &finished); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || u != "https://"+key+"/seed" || errText != "" || priority != 1 || attempts != 0 || finished.Valid {
			t.Errorf("%s: %s of %s (error %q), priority %d, %d attempts, finished %v; want pending of the entry's address at 1",
				key, status, u, errText, priority, attempts, finished.Valid)
		}
	}
	for key, status := range map[string]string{found: "done", pending: "pending", running: "running", unlisted: "failed"} {
		var got, u string
		if err := db.QueryRow(`SELECT status, url FROM lookups WHERE site_key = $1`, key).Scan(&got, &u); err != nil {
			t.Fatal(err)
		}
		if got != status || u != "https://"+key+"/lookup" {
			t.Errorf("%s: %s of %s, want untouched (%s)", key, got, u, status)
		}
	}
	var neverLookups int
	if err := db.QueryRow(`SELECT count(*) FROM lookups WHERE site_key = $1`, never).Scan(&neverLookups); err != nil || neverLookups != 0 {
		t.Errorf("lookups of the entry never looked up = %d, %v; want none", neverLookups, err)
	}
	events := auditBy(t, db, by)
	if len(events) != 1 || events[0].Action != "seed.retry" || events[0].TargetType != "seed" || events[0].TargetID != "" ||
		events[0].After["count"] != float64(n) {
		t.Errorf("audit = %+v, want one seed.retry with the count %d", events, n)
	}

	// Nothing left to retry.
	n, err = repo.RetrySeedLookups(t.Context(), by)
	if err != nil || n != 0 {
		t.Errorf("second RetrySeedLookups = %d, %v; want 0", n, err)
	}
	if events := auditBy(t, db, by); len(events) != 1 {
		t.Errorf("audit = %+v, want still one entry", events)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryCatalog(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)

	second := newTestSection(t, db, "Second", 2)
	first := newTestSection(t, db, "First", 1)
	empty := newTestSection(t, db, "Empty", 3)
	posted := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	b := newCatalogFeed(t, db, first, posted)
	if _, err := db.Exec(`UPDATE feeds SET title = 'b feed', posts_per_week = 2.5 WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	a := newCatalogFeed(t, db, first)
	if _, err := db.Exec(`UPDATE feeds SET title = 'A feed' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	user := newTestUser(t, db)
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), user, b.ID); err != nil {
		t.Fatal(err)
	}
	c := newCatalogFeed(t, db, second)
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Every section, in their order; the test's are after the others.
	var mine []rss.AdminCatalogSection
	for _, sec := range got {
		if sec.ID == first.ID || sec.ID == second.ID || sec.ID == empty.ID {
			mine = append(mine, sec)
		}
	}
	if len(mine) != 3 || mine[0].ID != first.ID || mine[1].ID != second.ID || mine[2].ID != empty.ID {
		t.Fatalf("test sections = %+v, want First, Second, Empty", mine)
	}
	if mine[0].Name != first.Name || mine[0].Slug != first.Slug {
		t.Errorf("section = %+v, want %+v", mine[0].Section, first)
	}
	if n := len(mine[2].Feeds); n != 0 {
		t.Errorf("Empty has %d feeds, want 0", n)
	}
	// A gone feed is listed, marked, though Discover hides it.
	if len(mine[1].Feeds) != 1 || mine[1].Feeds[0].ID != c.ID || mine[1].Feeds[0].GoneAt.IsZero() {
		t.Errorf("Second's feeds = %+v, want %s, gone", mine[1].Feeds, c.ID)
	}

	// By title, any case.
	fs := mine[0].Feeds
	if len(fs) != 2 || fs[0].ID != a.ID || fs[1].ID != b.ID {
		t.Fatalf("First's feeds = %+v, want A feed, b feed", fs)
	}
	if !fs[0].LatestPost.IsZero() || fs[0].Subscribers != 0 || !fs[0].GoneAt.IsZero() {
		t.Errorf("A feed = %+v, want no post, no subscribers", fs[0])
	}
	f := fs[1]
	if !f.LatestPost.Equal(posted) || f.Subscribers != 1 || f.PostsPerWeek != 2.5 || f.Title != "b feed" || f.URL != b.Url || f.SiteID != b.SiteID || f.SiteHost == "" {
		t.Errorf("b feed = %+v, want posted %v, 1 subscriber, 2.5 a week, its URL and site", f, posted)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositorySeedOnList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)

	on, off := newLookupKey(t, lookups), newLookupKey(t, lookups)
	seedSite(t, db, on, "https://"+on+"/", time.Now())

	got, err := repo.SeedOnList(t.Context(), []string{on, off})
	if err != nil {
		t.Fatal(err)
	}
	if !got[on] || got[off] || len(got) != 1 {
		t.Errorf("SeedOnList = %v, want only %s", got, on)
	}
	if got, err := repo.SeedOnList(t.Context(), nil); err != nil || len(got) != 0 {
		t.Errorf("SeedOnList(nil) = %v, %v; want none", got, err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryAddSeedSites(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)
	by := testActor(t, db)

	sec := newTestSection(t, db, "Seed add", 1)
	foundID, found := adminFeedsSite(t, db)
	fresh, failed, empty, pending, listed, skipped := newLookupKey(t, lookups), newLookupKey(t, lookups),
		newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
	setLookup(t, lookups, found, "done", foundID, "")
	setLookup(t, lookups, failed, "failed", "", "boom")
	setLookup(t, lookups, empty, "done", "", "")
	setLookup(t, lookups, pending, "pending", "", "")
	seedSite(t, db, listed, "https://"+listed+"/old", time.Now())
	keys := []string{fresh, failed, empty, pending, found}
	cleanupExec(t, db, `DELETE FROM seed_sites WHERE site_key = ANY($1)`, pq.Array(keys))

	line := func(key, sectionID string) seed.Line {
		return seed.Line{Text: key, Entry: seed.Entry{URL: "https://" + key + "/seed", SiteKey: key}, SectionID: sectionID, Outcome: seed.LineAdd}
	}
	lines := []seed.Line{
		line(fresh, sec.ID), line(failed, ""), line(empty, ""), line(pending, ""), line(found, ""),
		// Already on the list, perhaps added since the preview.
		line(listed, sec.ID),
		// Not an add.
		{Text: skipped, Entry: seed.Entry{URL: "https://" + skipped + "/", SiteKey: skipped}, Outcome: seed.LineBadSection},
	}
	n, err := repo.AddSeedSites(t.Context(), lines, "waitlist", by)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("AddSeedSites = %d, want 5", n)
	}

	// Each added with its hint and the source.
	entries := seedPages(t, repo, rss.AdminSeedAll, 100, append(slices.Clone(keys), listed, skipped))
	got := map[string]rss.AdminSeedEntry{}
	for _, e := range entries {
		got[e.SiteKey] = e
	}
	if len(got) != 6 {
		t.Errorf("seed list has %d of the keys, want 6 (not the skipped line)", len(got))
	}
	if e := got[fresh]; e.URL != "https://"+fresh+"/seed" || e.SectionName != sec.Name || e.Source != "waitlist" {
		t.Errorf("%s: %+v, want its address, section %q and source waitlist", fresh, e, sec.Name)
	}
	if e := got[failed]; e.SectionName != "" || e.Source != "waitlist" {
		t.Errorf("%s: section %q, source %q; want none and waitlist", failed, e.SectionName, e.Source)
	}
	if e := got[listed]; e.URL != "https://"+listed+"/old" || e.SectionName != "" || e.Source != "" {
		t.Errorf("%s already on the list changed: %+v", listed, e)
	}

	// New and not-found lookups queued at 1 with the entry's address; found
	// and pending ones left as they are.
	for _, key := range []string{fresh, failed, empty} {
		var status, u, errText string
		var priority, attempts int
		var finished sql.NullTime
		if err := db.QueryRow(`SELECT status, url, error, priority, attempts, finished_at FROM lookups WHERE site_key = $1`, key).
			Scan(&status, &u, &errText, &priority, &attempts, &finished); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if status != "pending" || u != "https://"+key+"/seed" || errText != "" || priority != 1 || attempts != 0 || finished.Valid {
			t.Errorf("%s: %s of %s (error %q), priority %d, %d attempts, finished %v; want pending of the entry's address at 1",
				key, status, u, errText, priority, attempts, finished.Valid)
		}
	}
	for key, status := range map[string]string{found: "done", pending: "pending"} {
		var got, u string
		var priority int
		if err := db.QueryRow(`SELECT status, url, priority FROM lookups WHERE site_key = $1`, key).Scan(&got, &u, &priority); err != nil {
			t.Fatal(err)
		}
		if got != status || u != "https://"+key+"/lookup" || priority != 0 {
			t.Errorf("%s: %s of %s at %d, want untouched (%s)", key, got, u, priority, status)
		}
	}
	var listedLookups int
	if err := db.QueryRow(`SELECT count(*) FROM lookups WHERE site_key = ANY($1)`, pq.Array([]string{listed, skipped})).Scan(&listedLookups); err != nil || listedLookups != 0 {
		t.Errorf("lookups of the lines not added = %d, %v; want none", listedLookups, err)
	}

	// One audit entry with the keys added, in the upload's order.
	events := auditBy(t, db, by)
	if len(events) != 1 || events[0].Action != "seed.add" || events[0].TargetType != "seed" || events[0].TargetID != "" ||
		events[0].After["source"] != "waitlist" || events[0].After["count"] != float64(5) {
		t.Fatalf("audit = %+v, want one seed.add from waitlist with the count 5", events)
	}
	var auditKeys []string
	for _, k := range events[0].After["site_keys"].([]any) {
		auditKeys = append(auditKeys, k.(string))
	}
	if !slices.Equal(auditKeys, keys) {
		t.Errorf("audit's site keys = %v, want %v", auditKeys, keys)
	}

	// Nothing to add records nothing.
	for _, ls := range [][]seed.Line{lines, lines[6:], nil} {
		if n, err := repo.AddSeedSites(t.Context(), ls, "", by); err != nil || n != 0 {
			t.Errorf("AddSeedSites again = %d, %v; want 0", n, err)
		}
	}
	if events := auditBy(t, db, by); len(events) != 1 {
		t.Errorf("audit = %+v, want still one entry", events)
	}
}
