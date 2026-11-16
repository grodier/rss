package psql

import (
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
)

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositorySeedList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	lookups := NewLookupRepository(db)

	// Created first, so it's deleted after the feeds under it.
	sec := newTestSection(t, db, "Seed", 1)
	siteID, host := adminFeedsSite(t, db)
	adminFeed(t, db, siteID, "In Discover", "")
	adminFeed(t, db, siteID, "Not in Discover", "")
	inDiscover := adminFeed(t, db, siteID, "Also in Discover", "")
	putInSection(t, db, inDiscover, sec)

	found, failed, empty, running, pending, never := host, newLookupKey(t, lookups), newLookupKey(t, lookups),
		newLookupKey(t, lookups), newLookupKey(t, lookups), newLookupKey(t, lookups)
	set := func(key, status, siteID, errText string) {
		t.Helper()
		execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, site_id, error, attempts, started_at, finished_at)
			VALUES ($1, 'https://' || $1 || '/', $2, NULLIF($3, '')::uuid, $4, 1, now(),
				CASE WHEN $2 IN ('done', 'failed') THEN now() END)`, key, status, siteID, errText)
	}
	set(found, "done", siteID, "")
	set(failed, "failed", "", "no such host")
	set(empty, "done", "", "")
	set(running, "running", "", "")
	set(pending, "pending", "", "")

	// The list's order, not the keys'.
	entries := []seed.Entry{
		{URL: "https://" + never + "/", SiteKey: never},
		{URL: "https://" + found + "/feed.xml", SiteKey: found},
		{URL: "https://" + failed + "/", SiteKey: failed},
		{URL: "https://" + empty + "/", SiteKey: empty},
		{URL: "https://" + running + "/", SiteKey: running},
		{URL: "https://" + pending + "/", SiteKey: pending},
	}
	got, err := repo.SeedList(t.Context(), entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(entries) {
		t.Fatalf("SeedList returned %d entries, want %d", len(got), len(entries))
	}
	want := []struct {
		state            string
		host             string
		feeds, discover  int
		lookupID, errMsg bool
	}{
		{state: rss.SeedWaiting},
		{state: rss.SeedFound, host: host, feeds: 3, discover: 1, lookupID: true},
		{state: rss.SeedFailed, lookupID: true, errMsg: true},
		{state: rss.SeedNoFeeds, lookupID: true},
		{state: rss.SeedRunning, lookupID: true},
		{state: rss.SeedWaiting, lookupID: true},
	}
	for i, w := range want {
		e := got[i]
		if e.SiteKey != entries[i].SiteKey || e.URL != entries[i].URL {
			t.Errorf("entry %d = %s (%s), want %s (%s)", i, e.SiteKey, e.URL, entries[i].SiteKey, entries[i].URL)
		}
		if e.State() != w.state || e.SiteHost != w.host || e.Feeds != w.feeds || e.InDiscover != w.discover {
			t.Errorf("%s: state %s, host %q, %d feeds, %d in Discover; want %s, %q, %d, %d",
				e.SiteKey, e.State(), e.SiteHost, e.Feeds, e.InDiscover, w.state, w.host, w.feeds, w.discover)
		}
		if (e.Lookup.ID != "") != w.lookupID || (e.Lookup.Error != "") != w.errMsg {
			t.Errorf("%s: lookup %+v; want an ID %v, an error %v", e.SiteKey, e.Lookup, w.lookupID, w.errMsg)
		}
	}
	if got[1].Lookup.SiteID != siteID || got[1].Lookup.FinishedAt.IsZero() {
		t.Errorf("found entry's lookup = %+v, want site %s, finished", got[1].Lookup, siteID)
	}

	if got, err := repo.SeedList(t.Context(), nil); err != nil || len(got) != 0 {
		t.Errorf("SeedList(nil) = %v, %v; want none", got, err)
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
		e := seed.Entry{URL: "https://" + key + "/feed.xml", SiteKey: key}
		l, err := repo.QueueSeedLookup(t.Context(), e, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupPending || l.URL != e.URL || l.SiteKey != key {
			t.Errorf("lookup = %+v, want pending of %s", l, e.URL)
		}
		if p := lookupPriority(t, db, key); p != 0 {
			t.Errorf("priority %d, want a reader's, 0", p)
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "seed.lookup" || events[0].TargetType != "seed" || events[0].TargetID != key ||
			events[0].After["lookup_id"] != l.ID || events[0].After["url"] != e.URL || events[0].After["status"] != "pending" {
			t.Errorf("audit = %+v, want one seed.lookup of %s, lookup %s", events, key, l.ID)
		}
	})

	t.Run("failed a minute ago runs again", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, error, attempts, priority, finished_at)
			VALUES ($1, 'https://' || $1 || '/', 'failed', 'boom', 2, 1, now() - interval '1 minute')`, key)
		l, err := repo.QueueSeedLookup(t.Context(), seed.Entry{URL: "https://" + key + "/", SiteKey: key}, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupPending || l.Error != "" || l.Attempts != 0 {
			t.Errorf("lookup = %+v, want pending again", l)
		}
	})

	t.Run("running is left as it is", func(t *testing.T) {
		key := newLookupKey(t, lookups)
		execSQL(t, lookups, `INSERT INTO lookups (site_key, url, status, attempts, started_at)
			VALUES ($1, 'https://' || $1 || '/', 'running', 1, now())`, key)
		l, err := repo.QueueSeedLookup(t.Context(), seed.Entry{URL: "https://" + key + "/", SiteKey: key}, by)
		if err != nil {
			t.Fatal(err)
		}
		if l.Status != rss.LookupRunning {
			t.Errorf("lookup = %+v, want still running", l)
		}
	})
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
	if len(mine[1].Feeds) != 1 || mine[1].Feeds[0].ID != c.ID {
		t.Errorf("Second's feeds = %+v, want %s", mine[1].Feeds, c.ID)
	}

	// By title, any case.
	fs := mine[0].Feeds
	if len(fs) != 2 || fs[0].ID != a.ID || fs[1].ID != b.ID {
		t.Fatalf("First's feeds = %+v, want A feed, b feed", fs)
	}
	if !fs[0].LatestPost.IsZero() || fs[0].Subscribers != 0 {
		t.Errorf("A feed = %+v, want no post, no subscribers", fs[0])
	}
	f := fs[1]
	if !f.LatestPost.Equal(posted) || f.Subscribers != 1 || f.PostsPerWeek != 2.5 || f.Title != "b feed" || f.URL != b.Url || f.SiteID != b.SiteID || f.SiteHost == "" {
		t.Errorf("b feed = %+v, want posted %v, 1 subscriber, 2.5 a week, its URL and site", f, posted)
	}
}
