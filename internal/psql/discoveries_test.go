package psql

import (
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB.
func TestFeedUpsertAndListBySite(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds := NewFeedRepository(db)
	sites := NewSiteRepository(db)
	n := time.Now().UnixNano()

	newSite := func(name string) string {
		t.Helper()
		host := fmt.Sprintf("%s-%d.example.com", name, n)
		id, err := sites.Upsert(t.Context(), rss.Site{Host: host, URL: "https://" + host + "/"})
		if err != nil {
			t.Fatalf("Upsert site: %v", err)
		}
		return id
	}
	siteA, siteB := newSite("a"), newSite("b")
	prefix := fmt.Sprintf("https://feeds-%d.example.com/", n)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("cleanup feeds: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM sites WHERE id IN ($1, $2)`, siteA, siteB); err != nil {
			t.Errorf("cleanup sites: %v", err)
		}
	})

	get := func(t *testing.T, id string) rss.Feed {
		t.Helper()
		f, err := feeds.GetByID(t.Context(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return f
	}

	t.Run("same URL updates title and keeps ID", func(t *testing.T) {
		f := rss.Feed{Url: prefix + "one", SiteUrl: "https://x/", Title: "Old", SiteID: siteA}
		id, err := feeds.Upsert(t.Context(), f)
		if err != nil || id == "" {
			t.Fatalf("Upsert: id %q, err %v", id, err)
		}
		f.Title = "New"
		id2, err := feeds.Upsert(t.Context(), f)
		if err != nil {
			t.Fatalf("Upsert again: %v", err)
		}
		if id2 != id {
			t.Errorf("got id %q; want %q", id2, id)
		}
		if got := get(t, id).Title; got != "New" {
			t.Errorf("got title %q; want New", got)
		}
	})

	t.Run("empty title keeps the old one", func(t *testing.T) {
		f := rss.Feed{Url: prefix + "two", Title: "Keep", Description: "Desc", SiteID: siteA}
		id, err := feeds.Upsert(t.Context(), f)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		f.Title, f.Description = "", ""
		if _, err := feeds.Upsert(t.Context(), f); err != nil {
			t.Fatalf("Upsert again: %v", err)
		}
		if got := get(t, id); got.Title != "Keep" || got.Description != "Desc" {
			t.Errorf("got title %q, description %q; want Keep, Desc", got.Title, got.Description)
		}
	})

	t.Run("existing site is kept", func(t *testing.T) {
		attached := rss.Feed{Url: prefix + "attached", SiteID: siteA}
		id, err := feeds.Upsert(t.Context(), attached)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		attached.SiteID = siteB
		if _, err := feeds.Upsert(t.Context(), attached); err != nil {
			t.Fatalf("Upsert again: %v", err)
		}
		if got := get(t, id).SiteID; got != siteA {
			t.Errorf("got site %q; want %q", got, siteA)
		}
	})

	t.Run("ListBySite returns only that site's feeds in title order", func(t *testing.T) {
		siteC := newSite("c")
		t.Cleanup(func() {
			db.Exec(`DELETE FROM feeds WHERE site_id = $1`, siteC)
			db.Exec(`DELETE FROM sites WHERE id = $1`, siteC)
		})
		for _, f := range []rss.Feed{
			{Url: prefix + "c2", Title: "Zed", SiteID: siteC},
			{Url: prefix + "c1", Title: "Alpha", SiteID: siteC},
			{Url: prefix + "other", Title: "Aaa", SiteID: siteA},
		} {
			if _, err := feeds.Upsert(t.Context(), f); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
		}
		got, err := feeds.ListBySite(t.Context(), siteC)
		if err != nil {
			t.Fatalf("ListBySite: %v", err)
		}
		if len(got) != 2 || got[0].Title != "Alpha" || got[1].Title != "Zed" {
			t.Errorf("got %+v; want Alpha, Zed", got)
		}
	})
}

func TestDiscoveryRepositorySave(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoveryRepository(db)
	n := time.Now().UnixNano()

	host := fmt.Sprintf("disc-%d.example.com", n)
	site := rss.Site{Host: host, URL: "https://" + host + "/", Title: "Disc"}
	feedList := []rss.Feed{
		{Url: "https://" + host + "/a.xml", Title: "A"},
		{Url: "https://" + host + "/b.xml", Title: "B"},
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE url LIKE $1`, "https://"+host+"/%"); err != nil {
			t.Errorf("cleanup feeds: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM sites WHERE host = $1`, host); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
	})

	id, err := repo.Save(t.Context(), site, feedList)
	if err != nil || id == "" {
		t.Fatalf("Save: id %q, err %v", id, err)
	}
	id2, err := repo.Save(t.Context(), site, feedList)
	if err != nil {
		t.Fatalf("Save again: %v", err)
	}
	if id2 != id {
		t.Errorf("got site id %q; want %q", id2, id)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM feeds WHERE site_id = $1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("got %d feeds; want 2", count)
	}

	t.Run("zero feeds is allowed", func(t *testing.T) {
		h := fmt.Sprintf("empty-%d.example.com", n)
		t.Cleanup(func() { db.Exec(`DELETE FROM sites WHERE host = $1`, h) })
		if id, err := repo.Save(t.Context(), rss.Site{Host: h, URL: "https://" + h + "/"}, nil); err != nil || id == "" {
			t.Errorf("Save: id %q, err %v", id, err)
		}
	})
}
