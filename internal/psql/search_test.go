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
	ctx := t.Context()

	tok := fmt.Sprintf("zq%d", time.Now().UnixNano())
	t.Cleanup(func() {
		db.Exec(`DELETE FROM feeds WHERE url LIKE $1`, "%"+tok+"%")
		db.Exec(`DELETE FROM sites WHERE host LIKE $1`, "%"+tok+"%")
	})

	addSite := func(host, title string) string {
		t.Helper()
		id, err := upsertSite(ctx, db, rss.Site{Host: host, URL: "https://" + host + "/", Title: title}, true)
		if err != nil {
			t.Fatalf("Upsert site: %v", err)
		}
		return id
	}
	addFeed := func(siteID, url, title string) {
		t.Helper()
		if _, err := upsertFeed(ctx, db, rss.Feed{SiteID: siteID, Url: url, Title: title}); err != nil {
			t.Fatalf("Upsert feed: %v", err)
		}
	}
	siteHosts := func(res []rss.SiteWithFeeds) []string {
		var hs []string
		for _, s := range res {
			hs = append(hs, s.Site.Host)
		}
		return hs
	}
	search := func(q string, limit int) []rss.SiteWithFeeds {
		t.Helper()
		res, _, err := repo.Search(ctx, q, "", "", limit)
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
		if n := len(res[0].Feeds); n != 1 {
			t.Errorf("got %d feeds; want 1", n)
		}

		// Matches only the "Main" feed's URL; both feeds still come back, by title.
		res = search(hostA+"/feed.xml", 10)
		if len(res) != 1 || len(res[0].Feeds) != 2 {
			t.Fatalf("got %+v; want one site with 2 feeds", res)
		}
		if f := res[0].Feeds; f[0].Title != "Comments" || f[1].Title != "Main" {
			t.Errorf("got feed titles %q, %q; want Comments, Main", f[0].Title, f[1].Title)
		}
	})

	t.Run("by feed url", func(t *testing.T) {
		res := search("/comments.xml", 100)
		found := false
		for _, s := range res {
			found = found || s.Site.ID == siteA
		}
		if !found {
			t.Errorf("site %s not found in %v", hostA, siteHosts(res))
		}
	})

	t.Run("by site description", func(t *testing.T) {
		hostF := "f-" + tok + ".example"
		if _, err := upsertSite(ctx, db, rss.Site{Host: hostF, URL: "https://" + hostF + "/", Title: "Untitled", Description: "Weekly notes on " + tok + "gardening"}, true); err != nil {
			t.Fatal(err)
		}
		if got := siteHosts(search(tok+"GARDENING", 10)); len(got) != 1 || got[0] != hostF {
			t.Errorf("got sites %v; want [%s]", got, hostF)
		}
	})

	t.Run("by feed description", func(t *testing.T) {
		hostG := "g-" + tok + ".example"
		siteG := addSite(hostG, "Untitled")
		addFeed(siteG, "https://"+hostG+"/other.xml", "Other")
		if _, err := upsertFeed(ctx, db, rss.Feed{SiteID: siteG, Url: "https://" + hostG + "/feed.xml", Title: "Main", Description: "Episodes about " + tok + "beekeeping"}); err != nil {
			t.Fatal(err)
		}
		res := search(tok+"beekeeping", 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostG {
			t.Fatalf("got sites %v; want [%s]", got, hostG)
		}
		if n := len(res[0].Feeds); n != 2 {
			t.Errorf("got %d feeds; want the site's 2", n)
		}
	})

	t.Run("case-insensitive", func(t *testing.T) {
		res := search(strings.ToUpper("blog "+tok), 10)
		if got := siteHosts(res); len(got) != 1 || got[0] != hostA {
			t.Errorf("got sites %v; want [%s]", got, hostA)
		}
	})

	t.Run("site whose host is the site key is first", func(t *testing.T) {
		// A third site whose title sorts before the others and contains the host.
		hostC := "c-" + tok + ".example.io"
		addSite(hostC, "AAA "+hostA)
		res, _, err := repo.Search(ctx, hostA, hostA, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		got := siteHosts(res)
		if len(got) != 2 || got[0] != hostA || got[1] != hostC {
			t.Errorf("got sites %v; want [%s %s]", got, hostA, hostC)
		}
	})

	t.Run("full address found by its site key", func(t *testing.T) {
		hostE := "e-" + tok + ".example"
		addSite(hostE, "AAA "+tok)
		q := "https://www." + hostE + "/some/path"
		res, _, err := repo.Search(ctx, q, hostE, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if got := siteHosts(res); len(got) != 1 || got[0] != hostE {
			t.Errorf("got sites %v; want [%s]", got, hostE)
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

	t.Run("ordered by how well the name matches, then by title", func(t *testing.T) {
		w := "mq" + tok
		inside := addSiteHost(t, addSite, "o1-"+tok+".example", "A"+w+"x")
		word := addSiteHost(t, addSite, "o2-"+tok+".example", "B "+w)
		prefix := addSiteHost(t, addSite, "o3-"+tok+".example", w+" Z")
		exact := addSiteHost(t, addSite, "o4-"+tok+".example", strings.ToUpper(w))
		got := siteHosts(search(w, 10))
		if want := []string{exact, prefix, word, inside}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("got %v; want exact, prefix, word start, inside: %v", got, want)
		}
	})

	t.Run("limit", func(t *testing.T) {
		if res := search(tok, 1); len(res) != 1 {
			t.Errorf("got %d sites; want 1", len(res))
		}
	})

	t.Run("sites on a shared host are grouped", func(t *testing.T) {
		searchAll := func(q, siteKey, host string, limit int) ([]string, []rss.SiteGroup) {
			t.Helper()
			res, groups, err := repo.Search(ctx, q, siteKey, host, limit)
			if err != nil {
				t.Fatalf("Search(%q, %q, %q): %v", q, siteKey, host, err)
			}
			return siteHosts(res), groups
		}

		// Five profiles matched only by their keys.
		shared := "sh-" + tok + ".example"
		for i := range 5 {
			addSite(fmt.Sprintf("%s/@user%d", shared, i), fmt.Sprintf("User %d (@user%d)", i, i))
		}
		sites, groups := searchAll(shared, "", "", 10)
		if len(sites) != 0 || len(groups) != 1 || groups[0] != (rss.SiteGroup{Host: shared, Count: 5}) {
			t.Errorf("by key: got sites %v, groups %+v; want one group of 5", sites, groups)
		}

		t.Run("narrowed to the host, none grouped", func(t *testing.T) {
			sites, groups := searchAll(shared, "", shared, 10)
			if len(sites) != 5 || len(groups) != 0 {
				t.Errorf("got sites %v, groups %+v; want the 5 sites", sites, groups)
			}
			sites, groups = searchAll(shared, "", "nope-"+tok+".example", 10)
			if len(sites) != 0 || len(groups) != 0 {
				t.Errorf("another host: got sites %v, groups %+v; want nothing", sites, groups)
			}
		})

		t.Run("profiles matched by their feeds' platform are grouped", func(t *testing.T) {
			// Titles are stored without the platform (#728); a profile's
			// feed often names it: "Stories by Ann on Medium".
			platform := "Plat" + tok
			host := "pf-" + tok + ".example"
			for _, name := range []string{"ann", "bo", "cy"} {
				id := addSite(host+"/@"+name, name)
				addFeed(id, "https://"+host+"/@"+name+".rss", "Stories by "+name+" on "+platform)
			}
			sites, groups := searchAll(platform, "", "", 10)
			if len(sites) != 0 || len(groups) != 1 || groups[0] != (rss.SiteGroup{Host: host, Count: 3}) {
				t.Errorf("got sites %v, groups %+v; want one group of 3", sites, groups)
			}
		})

		w := "kw" + tok
		named := "nm-" + tok + ".example"
		first := named + "/@" + w + "-1"
		addSite(first, "Zed – Platform")
		addSite(named+"/@"+w+"-2", "Yan – Platform")
		byName := addSiteHost(t, addSite, named+"/@"+w+"-3", w+" Writes – Platform")
		addSite(named+"/@"+w+"-4", "The "+w+" – Platform")

		t.Run("a site whose own name matches isn't grouped", func(t *testing.T) {
			sites, groups := searchAll(w, "", "", 10)
			if len(sites) != 2 || sites[0] != byName || len(groups) != 1 || groups[0] != (rss.SiteGroup{Host: named, Count: 2}) {
				t.Errorf("got sites %v, groups %+v; want [%s, the word start] and a group of 2", sites, groups, byName)
			}
		})

		t.Run("the limit counts sites only", func(t *testing.T) {
			sites, groups := searchAll(w, "", "", 1)
			if len(sites) != 1 || sites[0] != byName || len(groups) != 1 || groups[0].Count != 2 {
				t.Errorf("got sites %v, groups %+v; want [%s] and a group of 2", sites, groups, byName)
			}
		})

		t.Run("the exact address comes first and no group has one site", func(t *testing.T) {
			sites, groups := searchAll(w, first, "", 10)
			if len(sites) != 4 || sites[0] != first || len(groups) != 0 {
				t.Errorf("got sites %v, groups %+v; want all 4, %s first", sites, groups, first)
			}
		})

		t.Run("sites without a path are never grouped", func(t *testing.T) {
			q := "np-" + tok
			for i := range 3 {
				addSite(fmt.Sprintf("%s-%d.example", q, i), "Other")
			}
			sites, groups := searchAll(q, "", "", 10)
			if len(sites) != 3 || len(groups) != 0 {
				t.Errorf("got sites %v, groups %+v; want the 3 sites", sites, groups)
			}
		})
	})

	t.Run("empty query", func(t *testing.T) {
		for _, q := range []string{"", "  \t"} {
			if res := search(q, 10); len(res) != 0 {
				t.Errorf("Search(%q) = %+v; want empty", q, res)
			}
		}
	})
}

// addSiteHost adds a site with addSite and returns its host.
func addSiteHost(t *testing.T, addSite func(host, title string) string, host, title string) string {
	t.Helper()
	addSite(host, title)
	return host
}

func TestMatchPatterns(t *testing.T) {
	if got, want := prefixPattern(`50%_off`), `50\%\_off%`; got != want {
		t.Errorf("prefixPattern = %q; want %q", got, want)
	}
	if got, want := wordPattern(`50%_off`), `% 50\%\_off%`; got != want {
		t.Errorf("wordPattern = %q; want %q", got, want)
	}
}
