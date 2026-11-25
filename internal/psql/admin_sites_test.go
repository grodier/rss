package psql

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// adminSitesSite inserts a site whose host is prefix plus name, titled
// title, deleting it and its lookup when the test ends, and returns its ID.
func adminSitesSite(t *testing.T, db *sql.DB, prefix, name, title string) string {
	t.Helper()
	host := prefix + name + ".example.com"
	id, err := upsertSite(t.Context(), db, rss.Site{Host: host, URL: "https://" + host + "/", Title: title}, true)
	if err != nil {
		t.Fatalf("Upsert site: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM sites WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM lookups WHERE site_key = $1`, host); err != nil {
			t.Errorf("cleanup lookup: %v", err)
		}
	})
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryListSites(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	// Hosts start with prefix, so a search for it finds only this test's.
	prefix := fmt.Sprintf("admin-sites-%d-", psqltest.Unique())
	u1, u2 := newTestUser(t, db), newTestUser(t, db)

	// withIcon has an icon, a lookup that found it and one feed u1 follows.
	withIcon := adminSitesSite(t, db, prefix, "icon", "With an icon")
	adminFeed(t, db, withIcon, "Icon feed", ``, u1)
	if _, err := db.Exec(`INSERT INTO site_icons (site_id, source_url, content_type, width, height, data, hash)
		VALUES ($1, 'https://example.com/favicon.png', 'image/png', 64, 48, '\x89', 'abc')`, withIcon); err != nil {
		t.Fatalf("insert icon: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO lookups (site_key, url, status, site_id, attempts, finished_at)
		VALUES ($1, $2, 'done', $3, 1, now())`, prefix+"icon.example.com", "https://"+prefix+"icon.example.com/", withIcon); err != nil {
		t.Fatalf("insert lookup: %v", err)
	}
	// failed has a failed lookup and no feeds; it was found earliest.
	failed := adminSitesSite(t, db, prefix, "failed", "Failed lookup")
	if _, err := db.Exec(`INSERT INTO lookups (site_key, url, status, error, attempts, finished_at)
		VALUES ($1, $2, 'failed', 'status 500', 2, now())`, prefix+"failed.example.com", "https://"+prefix+"failed.example.com/"); err != nil {
		t.Fatalf("insert lookup: %v", err)
	}
	// twoFeeds has two feeds, u1 following both and u2 one: 2 subscribers.
	twoFeeds := adminSitesSite(t, db, prefix, "two", "Two feeds 100%_done")
	adminFeed(t, db, twoFeeds, "Two A", ``, u1, u2)
	adminFeed(t, db, twoFeeds, "Two B", ``, u1)
	// quiet has a feed nobody follows.
	quiet := adminSitesSite(t, db, prefix, "quiet", "")
	adminFeed(t, db, quiet, "Quiet feed", ``)
	for i, id := range []string{failed, withIcon, twoFeeds, quiet} {
		if _, err := db.Exec(`UPDATE sites SET created_at = now() - make_interval(days => $2) WHERE id = $1`, id, 4-i); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
	}

	ids := func(rows []rss.AdminSiteRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	list := func(q rss.AdminSiteQuery) []rss.AdminSiteRow {
		t.Helper()
		if q.Q == "" {
			q.Q = prefix
		}
		if q.Sort == "" {
			q.Sort = rss.AdminSitesBySubscribers
		}
		if q.Limit == 0 {
			q.Limit = 100
		}
		rows, err := repo.ListSites(t.Context(), q)
		if err != nil {
			t.Fatalf("ListSites(%+v): %v", q, err)
		}
		return rows
	}

	t.Run("filters", func(t *testing.T) {
		tests := []struct {
			filter string
			want   []string
		}{
			{rss.AdminSitesAll, []string{withIcon, failed, twoFeeds, quiet}},
			{rss.AdminSitesNoIcon, []string{failed, twoFeeds, quiet}},
			{rss.AdminSitesLookupFailed, []string{failed}},
			{rss.AdminSitesNoSubscribers, []string{failed, quiet}},
		}
		for _, tt := range tests {
			got := ids(list(rss.AdminSiteQuery{Filter: tt.filter}))
			slices.Sort(got)
			want := slices.Sorted(slices.Values(tt.want))
			if !slices.Equal(got, want) {
				t.Errorf("filter %s = %v, want %v", tt.filter, got, want)
			}
		}
	})

	t.Run("counts", func(t *testing.T) {
		got, err := repo.CountSites(t.Context(), prefix)
		if err != nil {
			t.Fatalf("CountSites: %v", err)
		}
		want := map[string]int{rss.AdminSitesAll: 4, rss.AdminSitesNoIcon: 3, rss.AdminSitesLookupFailed: 1, rss.AdminSitesNoSubscribers: 2}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("CountSites = %v, want %v", got, want)
		}
		// Each count is what its filter lists.
		for filter, n := range got {
			if rows := list(rss.AdminSiteQuery{Filter: filter}); len(rows) != n {
				t.Errorf("filter %s lists %d sites, counted %d", filter, len(rows), n)
			}
		}
		// No search counts every site, these included.
		all, err := repo.CountSites(t.Context(), "")
		if err != nil {
			t.Fatalf("CountSites(\"\"): %v", err)
		}
		if all[rss.AdminSitesAll] < 4 {
			t.Errorf("CountSites(\"\") counts %d sites, want at least this test's 4", all[rss.AdminSitesAll])
		}
	})

	t.Run("search", func(t *testing.T) {
		if got := list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Q: prefix + "nothing"}); len(got) != 0 {
			t.Errorf("search for another host = %v, want none", ids(got))
		}
		// A title, any case; LIKE's wildcards are only themselves.
		if got := ids(list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Q: "TWO FEEDS 100%_DONE"})); !slices.Equal(got, []string{twoFeeds}) {
			t.Errorf("search for a title with %% and _ = %v, want [%s]", got, twoFeeds)
		}
		if got := list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Q: "Two feeds 1%done"}); len(got) != 0 {
			t.Errorf("search with %% as a wildcard = %v, want none", ids(got))
		}
		if got := list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Q: prefix[:len(prefix)-1] + "_icon"}); len(got) != 0 {
			t.Errorf("search with _ as a wildcard = %v, want none", ids(got))
		}
	})

	t.Run("columns", func(t *testing.T) {
		rows := map[string]rss.AdminSiteRow{}
		for _, r := range list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll}) {
			rows[r.ID] = r
		}
		if r := rows[twoFeeds]; r.Feeds != 2 || r.Subscribers != 2 {
			t.Errorf("two feeds: %d feeds, %d subscribers; want 2 and 2 (each reader once)", r.Feeds, r.Subscribers)
		}
		if r := rows[quiet]; r.Feeds != 1 || r.Subscribers != 0 {
			t.Errorf("quiet: %d feeds, %d subscribers; want 1 and 0", r.Feeds, r.Subscribers)
		}
		r := rows[withIcon]
		if r.Host != prefix+"icon.example.com" || r.Title != "With an icon" || r.CreatedAt.IsZero() {
			t.Errorf("with icon: site = %+v", r.Site)
		}
		if i := r.Icon; i.SiteID != withIcon || i.ContentType != "image/png" || i.Width != 64 || i.Height != 48 || i.Data != nil || i.FetchedAt.IsZero() {
			t.Errorf("with icon: icon = %+v", i)
		}
		if l := r.Lookup; l.ID == "" || l.Status != rss.LookupDone || l.SiteID != withIcon || l.FinishedAt.IsZero() {
			t.Errorf("with icon: lookup = %+v", l)
		}
		if l := rows[failed].Lookup; l.Status != rss.LookupFailed || l.Error != "status 500" || l.Attempts != 2 || l.SiteID != "" {
			t.Errorf("failed: lookup = %+v", l)
		}
		if r := rows[quiet]; r.Icon.SiteID != "" || r.Lookup != (rss.Lookup{}) {
			t.Errorf("quiet: icon %+v, lookup %+v; want none", r.Icon, r.Lookup)
		}
	})

	t.Run("icon", func(t *testing.T) {
		icon, err := repo.SiteIcon(t.Context(), withIcon)
		if err != nil {
			t.Fatalf("SiteIcon: %v", err)
		}
		if string(icon.Data) != "\x89" || icon.ContentType != "image/png" || icon.Hash != "abc" {
			t.Errorf("SiteIcon = %q, %q, %q; want the stored image", icon.Data, icon.ContentType, icon.Hash)
		}
		if _, err := repo.SiteIcon(t.Context(), quiet); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SiteIcon of a site without one: %v, want rss.ErrNoRecord", err)
		}
	})

	// Each order sorts by its key, then ID in the same direction, and pages
	// of 3 through the cursor give the same rows.
	sorts := []struct {
		sort string
		less func(a, b rss.AdminSiteRow) int
	}{
		{rss.AdminSitesBySubscribers, func(a, b rss.AdminSiteRow) int {
			return cmp.Or(cmp.Compare(b.Subscribers, a.Subscribers), cmp.Compare(b.ID, a.ID))
		}},
		{rss.AdminSitesByFeeds, func(a, b rss.AdminSiteRow) int {
			return cmp.Or(cmp.Compare(b.Feeds, a.Feeds), cmp.Compare(b.ID, a.ID))
		}},
		{rss.AdminSitesByHost, func(a, b rss.AdminSiteRow) int {
			return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.ID, b.ID))
		}},
		{rss.AdminSitesByFound, func(a, b rss.AdminSiteRow) int {
			return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
		}},
	}
	for _, tt := range sorts {
		t.Run("sort "+tt.sort, func(t *testing.T) {
			all := list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: tt.sort})
			if len(all) != 4 {
				t.Fatalf("%d rows, want 4", len(all))
			}
			if !slices.IsSortedFunc(all, tt.less) {
				t.Errorf("not sorted: %v", ids(all))
			}
			var paged []rss.AdminSiteRow
			after := ""
			for range 3 {
				page := list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: tt.sort, After: after, Limit: 3})
				if len(page) == 0 {
					break
				}
				paged = append(paged, page...)
				after = page[len(page)-1].ID
			}
			if !slices.Equal(ids(paged), ids(all)) {
				t.Errorf("pages = %v, want %v", ids(paged), ids(all))
			}
		})
	}
	// By found, the newest first: the order they were made in reverse.
	if got := ids(list(rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: rss.AdminSitesByFound})); !slices.Equal(got, []string{quiet, twoFeeds, withIcon, failed}) {
		t.Errorf("by found = %v, want newest first", got)
	}

	t.Run("unknown filter or order", func(t *testing.T) {
		if _, err := repo.ListSites(t.Context(), rss.AdminSiteQuery{Filter: "bogus", Sort: rss.AdminSitesByHost, Limit: 1}); err == nil {
			t.Error("unknown filter: no error")
		}
		if _, err := repo.ListSites(t.Context(), rss.AdminSiteQuery{Filter: rss.AdminSitesAll, Sort: "bogus", Limit: 1}); err == nil {
			t.Error("unknown order: no error")
		}
	})
}
