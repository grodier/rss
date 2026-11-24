package psql

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
)

// Requires a test database; runs in its own schema (newMigrateDB), since
// Rekey reads every feed and the one site_key_rules row.
func TestSiteRepositoryRekey(t *testing.T) {
	t.Parallel()
	db := newMigrateDB(t)
	mustMigrate(t, db)
	sites := NewSiteRepository(db)
	ctx := t.Context()

	addSite := func(host string) string {
		t.Helper()
		id, err := upsertSite(ctx, db, rss.Site{Host: host, URL: "https://" + host + "/", Title: host}, true)
		if err != nil {
			t.Fatalf("Upsert site %s: %v", host, err)
		}
		return id
	}
	addFeed := func(siteID, url, siteURL string) string {
		t.Helper()
		id, err := upsertFeed(ctx, db, rss.Feed{SiteID: siteID, Url: url, SiteUrl: siteURL, Title: url})
		if err != nil {
			t.Fatalf("Upsert feed %s: %v", url, err)
		}
		return id
	}
	siteOf := func(feedID string) rss.Site {
		t.Helper()
		var s rss.Site
		err := db.QueryRow(`SELECT s.id, s.host, s.url FROM feeds f JOIN sites s ON s.id = f.site_id WHERE f.id = $1`, feedID).
			Scan(&s.ID, &s.Host, &s.URL)
		if err != nil {
			t.Fatalf("site of feed %s: %v", feedID, err)
		}
		return s
	}
	siteExists := func(id string) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	type lookup struct {
		url, status string
		priority    int
		homePage    bool
	}
	lookupOf := func(key string) (lookup, bool) {
		t.Helper()
		var l lookup
		err := db.QueryRow(`SELECT url, status, priority, home_page FROM lookups WHERE site_key = $1`, key).
			Scan(&l.url, &l.status, &l.priority, &l.homePage)
		if errors.Is(err, sql.ErrNoRows) {
			return lookup{}, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return l, true
	}
	rekey := func(version int) RekeyResult {
		t.Helper()
		res, err := sites.Rekey(ctx, version, discovery.FeedSiteKey)
		if err != nil {
			t.Fatalf("Rekey(%d): %v", version, err)
		}
		return res
	}

	// A merged platform site: three authors, one by its link, one by its
	// URL only, one whose site already exists, and the platform's own feed.
	shared := addSite("medium.example")
	alice := addFeed(shared, "https://medium.example/feed/@alice", "https://medium.example/@alice?source=rss")
	bob := addFeed(shared, "https://medium.example/@bob.rss", "")
	cyID := addSite("medium.example/@cy")
	cy := addFeed(shared, "https://medium.example/@cy/feed.xml", "https://medium.example/@cy")
	own := addFeed(shared, "https://medium.example/feed", "https://medium.example/")
	// A merged site whose feeds all move; its lookup keeps its row.
	solo := addSite("solo.example")
	if _, err := db.Exec(`INSERT INTO lookups (site_key, url, status, site_id) VALUES ('solo.example', 'https://solo.example/', 'done', $1)`, solo); err != nil {
		t.Fatal(err)
	}
	dee := addFeed(solo, "https://solo.example/@dee.rss", "")
	// An existing lookup for a key that gets a site.
	if _, err := db.Exec(`INSERT INTO lookups (site_key, url, status) VALUES ('solo.example/@eve', 'https://solo.example/@eve/x', 'failed')`); err != nil {
		t.Fatal(err)
	}
	eve := addFeed(solo, "https://solo.example/@eve.rss", "")
	// Feeds that stay: an ordinary site's, one already on its site with a
	// path, and one whose link is on another host.
	plain := addFeed(addSite("plain.example"), "https://plain.example/feed.xml", "https://plain.example/")
	pathSite := addSite("solo.example/@fay")
	fay := addFeed(pathSite, "https://solo.example/@fay.rss", "")
	elsewhere := addFeed(addSite("feeds.other.example"), "https://feeds.other.example/x.xml", "https://other.example/@gus")

	res := rekey(1)
	if want := (RekeyResult{Feeds: 5, Created: 4, Deleted: 1}); res != want {
		t.Errorf("Rekey = %+v, want %+v", res, want)
	}

	t.Run("each author's feed is on its own site", func(t *testing.T) {
		for _, tt := range []struct{ feed, host, url string }{
			{alice, "medium.example/@alice", "https://medium.example/@alice"},
			{bob, "medium.example/@bob", "https://medium.example/@bob"},
			{cy, "medium.example/@cy", "https://medium.example/@cy/"}, // the existing site keeps its URL
			{dee, "solo.example/@dee", "https://solo.example/@dee"},
			{eve, "solo.example/@eve", "https://solo.example/@eve"},
		} {
			s := siteOf(tt.feed)
			if s.Host != tt.host || s.URL != tt.url {
				t.Errorf("feed %s: site %s at %s, want %s at %s", tt.feed, s.Host, s.URL, tt.host, tt.url)
			}
		}
		if s := siteOf(cy); s.ID != cyID {
			t.Errorf("cy's feed is on site %s, want the existing %s", s.ID, cyID)
		}
	})

	t.Run("new sites' lookups are queued at the seed's priority", func(t *testing.T) {
		for _, key := range []string{"medium.example/@alice", "medium.example/@bob", "medium.example/@cy", "solo.example/@dee"} {
			l, ok := lookupOf(key)
			root := "https://" + key
			if !ok || l != (lookup{url: root, status: "pending", priority: 1, homePage: true}) {
				t.Errorf("lookup of %s = %+v (found %v), want pending at priority 1 on %s", key, l, ok, root)
			}
		}
		if l, _ := lookupOf("solo.example/@eve"); l.status != "failed" || l.url != "https://solo.example/@eve/x" {
			t.Errorf("an existing lookup changed: %+v", l)
		}
	})

	t.Run("a bare site keeps its own feeds, or goes when it has none", func(t *testing.T) {
		if s := siteOf(own); s.ID != shared {
			t.Errorf("the platform's own feed moved to %s", s.Host)
		}
		if siteExists(solo) {
			t.Error("the emptied bare site wasn't deleted")
		}
		var siteID sql.NullString
		if err := db.QueryRow(`SELECT site_id FROM lookups WHERE site_key = 'solo.example'`).Scan(&siteID); err != nil || siteID.Valid {
			t.Errorf("the deleted site's lookup: site_id %v, err %v; want NULL", siteID, err)
		}
	})

	t.Run("other feeds stay", func(t *testing.T) {
		for feed, host := range map[string]string{plain: "plain.example", fay: "solo.example/@fay", elsewhere: "feeds.other.example"} {
			if s := siteOf(feed); s.Host != host {
				t.Errorf("feed on %s moved to %s", host, s.Host)
			}
		}
	})

	t.Run("the version gate", func(t *testing.T) {
		// Merged again after the re-key: the same version leaves it, a
		// newer one moves it.
		again := addFeed(shared, "https://medium.example/@hal.rss", "")
		if res := rekey(1); res != (RekeyResult{Skipped: true}) {
			t.Errorf("same version: Rekey = %+v, want skipped", res)
		}
		if s := siteOf(again); s.ID != shared {
			t.Errorf("a skipped re-key moved a feed to %s", s.Host)
		}
		if res := rekey(2); res != (RekeyResult{Feeds: 1, Created: 1}) {
			t.Errorf("newer version: Rekey = %+v, want 1 feed, 1 created", res)
		}
		if res := rekey(3); res != (RekeyResult{}) {
			t.Errorf("nothing to move: Rekey = %+v, want zero", res)
		}
	})
}
