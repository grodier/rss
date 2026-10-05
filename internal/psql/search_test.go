package psql

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func TestLikePattern(t *testing.T) {
	got := likePattern(`50%_off\`)
	want := `%50\%\_off\\%`
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestSearchRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSearchRepository(db)
	sites := NewSiteRepository(db)
	feeds := NewFeedRepository(db)
	ctx := t.Context()

	tok := fmt.Sprintf("zq%d", time.Now().UnixNano())
	t.Cleanup(func() {
		db.Exec(`DELETE FROM feeds WHERE url LIKE $1`, "%"+tok+"%")
		db.Exec(`DELETE FROM sites WHERE host LIKE $1`, "%"+tok+"%")
	})

	addSite := func(host, title string) string {
		t.Helper()
		id, err := sites.Upsert(ctx, rss.Site{Host: host, URL: "https://" + host + "/", Title: title})
		if err != nil {
			t.Fatalf("Upsert site: %v", err)
		}
		return id
	}
	addFeed := func(siteID, url, title string) {
		t.Helper()
		if _, err := feeds.Upsert(ctx, rss.Feed{SiteID: siteID, Url: url, Title: title}); err != nil {
			t.Fatalf("Upsert feed: %v", err)
		}
	}
	siteHosts := func(res rss.SearchResults) []string {
		var hs []string
		for _, s := range res.Sites {
			hs = append(hs, s.Site.Host)
		}
		return hs
	}
	search := func(q string, limit int) rss.SearchResults {
		t.Helper()
		res, err := repo.Search(ctx, q, limit)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		return res
	}

	hostA := "a-" + tok + ".example.com"
	hostB := "b-" + tok + ".example.org"
	siteA := addSite(hostA, "Blog "+tok)
	siteB := addSite(hostB, "Other")
	addFeed(siteA, "https://"+hostA+"/feed.xml", "Main")
	addFeed(siteA, "https://"+hostA+"/comments.xml", "Comments")
	addFeed(siteB, "https://"+hostB+"/rss", "Podcast "+tok+"feed")
	loose := "https://loose-" + tok + ".example.net/rss"
	addFeed("", loose, "Loose")

	t.Run("by site title", func(t *testing.T) {
		res := search("Blog "+tok, 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostA {
			t.Errorf("got sites %v; want [%s]", got, hostA)
		}
	})

	t.Run("by host", func(t *testing.T) {
		res := search(hostB, 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostB {
			t.Errorf("got sites %v; want [%s]", got, hostB)
		}
	})

	t.Run("by feed title returns the site with all its feeds", func(t *testing.T) {
		res := search("Podcast "+tok, 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostB {
			t.Fatalf("got sites %v; want [%s]", got, hostB)
		}
		if n := len(res.Sites[0].Feeds); n != 1 {
			t.Errorf("got %d feeds; want 1", n)
		}

		// Matches only the "Main" feed's URL; both feeds still come back, by title.
		res = search(hostA+"/feed.xml", 10)
		if len(res.Sites) != 1 || len(res.Sites[0].Feeds) != 2 {
			t.Fatalf("got %+v; want one site with 2 feeds", res.Sites)
		}
		if f := res.Sites[0].Feeds; f[0].Title != "Comments" || f[1].Title != "Main" {
			t.Errorf("got feed titles %q, %q; want Comments, Main", f[0].Title, f[1].Title)
		}
	})

	t.Run("by feed url", func(t *testing.T) {
		res := search("/comments.xml", 100)
		found := false
		for _, s := range res.Sites {
			found = found || s.Site.ID == siteA
		}
		if !found {
			t.Errorf("site %s not found in %v", hostA, siteHosts(res))
		}
	})

	t.Run("case-insensitive", func(t *testing.T) {
		res := search(strings.ToUpper("blog "+tok), 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostA {
			t.Errorf("got sites %v; want [%s]", got, hostA)
		}
	})

	t.Run("site whose host equals the query is first", func(t *testing.T) {
		// A third site whose title sorts before the others and contains the host.
		hostC := "c-" + tok + ".example.io"
		addSite(hostC, "AAA "+hostA)
		res := search(hostA, 10)
		got := siteHosts(res)
		if len(got) != 2 || got[0] != hostA || got[1] != hostC {
			t.Errorf("got sites %v; want [%s %s]", got, hostA, hostC)
		}
	})

	t.Run("percent matches literally", func(t *testing.T) {
		hostD := "d-" + tok + ".example.dev"
		addSite(hostD, "100% "+tok)
		if got := siteHosts(search("100% "+tok, 10)); len(got) != 1 || got[0] != hostD {
			t.Errorf("got sites %v; want [%s]", got, hostD)
		}
		if got := siteHosts(search("% "+tok, 10)); len(got) != 1 || got[0] != hostD {
			t.Errorf("got sites %v; want only [%s]", got, hostD)
		}
	})

	t.Run("siteless feed", func(t *testing.T) {
		res := search("loose-"+tok, 10)
		if len(res.Sites) != 0 {
			t.Errorf("got sites %v; want none", siteHosts(res))
		}
		if len(res.Feeds) != 1 || res.Feeds[0].Url != loose {
			t.Errorf("got feeds %+v; want [%s]", res.Feeds, loose)
		}
	})

	t.Run("limit", func(t *testing.T) {
		res := search(tok, 1)
		if len(res.Sites) != 1 {
			t.Errorf("got %d sites; want 1", len(res.Sites))
		}
		addFeed("", "https://loose2-"+tok+".example.net/rss", "Loose 2")
		res = search("loose", 1)
		if len(res.Feeds) != 1 {
			t.Errorf("got %d feeds; want 1", len(res.Feeds))
		}
	})

	t.Run("empty query", func(t *testing.T) {
		for _, q := range []string{"", "  \t"} {
			res := search(q, 10)
			if len(res.Sites) != 0 || len(res.Feeds) != 0 {
				t.Errorf("Search(%q) = %+v; want empty", q, res)
			}
		}
	})
}
