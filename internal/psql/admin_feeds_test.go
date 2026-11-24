package psql

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// adminFeedsSite inserts a site whose host is unique to the test, so a
// search for it (AdminFeedQuery.Q) finds only the test's feeds, and
// returns its ID and host.
func adminFeedsSite(t *testing.T, db *sql.DB) (id, host string) {
	t.Helper()
	host = fmt.Sprintf("admin-feeds-%d.example.com", time.Now().UnixNano())
	id, err := upsertSite(t.Context(), db, rss.Site{Host: host, URL: "https://" + host + "/"}, true)
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
	return id, host
}

// adminFeed inserts a feed of siteID titled title, sets its columns with
// set (an UPDATE's SET list, "" for none) and subscribes subscribers to it.
func adminFeed(t *testing.T, db *sql.DB, siteID, title, set string, subscribers ...string) string {
	t.Helper()
	id, err := upsertFeed(t.Context(), db, rss.Feed{
		SiteID: siteID, Title: title,
		Url: fmt.Sprintf("https://example.com/admin-feeds-%d.xml", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("Upsert feed: %v", err)
	}
	if set != "" {
		if _, err := db.Exec(`UPDATE feeds SET `+set+` WHERE id = $1`, id); err != nil {
			t.Fatalf("set %s: %v", set, err)
		}
	}
	for _, u := range subscribers {
		if err := NewSubscriptionRepository(db).Subscribe(t.Context(), u, id); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}
	return id
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryListFeeds(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	siteID, host := adminFeedsSite(t, db)
	u1, u2 := newTestUser(t, db), newTestUser(t, db)

	healthy := adminFeed(t, db, siteID, "Healthy", `last_fetched_at = now(), last_attempt_at = now(), posts_per_week = 5`, u1, u2)
	failing := adminFeed(t, db, siteID, "Failing", `last_fetched_at = now() - interval '2 days', consecutive_failures = 3, posts_per_week = 1`, u1)
	twoFailures := adminFeed(t, db, siteID, "Two failures", `last_fetched_at = now(), consecutive_failures = 2, posts_per_week = 2`, u2)
	gone := adminFeed(t, db, siteID, "Gone", `last_fetched_at = now() - interval '3 days', gone_at = now()`)
	newFeed := adminFeed(t, db, siteID, "New", ``, u1)
	oldNever := adminFeed(t, db, siteID, "Never fetched", `created_at = now() - interval '2 days'`)

	ids := func(rows []rss.AdminFeedRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	list := func(q rss.AdminFeedQuery) []rss.AdminFeedRow {
		t.Helper()
		if q.Q == "" {
			q.Q = host
		}
		if q.Sort == "" {
			q.Sort = rss.AdminFeedsBySubscribers
		}
		if q.Limit == 0 {
			q.Limit = 100
		}
		rows, err := repo.ListFeeds(t.Context(), q)
		if err != nil {
			t.Fatalf("ListFeeds(%+v): %v", q, err)
		}
		return rows
	}

	t.Run("filters", func(t *testing.T) {
		tests := []struct {
			filter string
			want   []string
		}{
			{rss.AdminFeedsAll, []string{healthy, failing, twoFailures, gone, newFeed, oldNever}},
			{rss.AdminFeedsFailing, []string{failing}},
			{rss.AdminFeedsGone, []string{gone}},
			{rss.AdminFeedsNeverFetched, []string{newFeed, oldNever}},
			// Not gone, which was fetched 3 days ago but has no subscribers,
			// nor the new feed, found too recently.
			{rss.AdminFeedsStale, []string{failing}},
			{rss.AdminFeedsNoSubscribers, []string{gone, oldNever}},
		}
		for _, tt := range tests {
			got := ids(list(rss.AdminFeedQuery{Filter: tt.filter}))
			slices.Sort(got)
			want := slices.Sorted(slices.Values(tt.want))
			if !slices.Equal(got, want) {
				t.Errorf("filter %s = %v, want %v", tt.filter, got, want)
			}
		}
	})

	t.Run("counts", func(t *testing.T) {
		got, err := repo.CountFeeds(t.Context(), host)
		if err != nil {
			t.Fatalf("CountFeeds: %v", err)
		}
		want := map[string]int{
			rss.AdminFeedsAll: 6, rss.AdminFeedsFailing: 1, rss.AdminFeedsGone: 1,
			rss.AdminFeedsNeverFetched: 2, rss.AdminFeedsStale: 1, rss.AdminFeedsNoSubscribers: 2,
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("CountFeeds = %v, want %v", got, want)
		}
		// Each count is what its filter lists.
		for filter, n := range got {
			if rows := list(rss.AdminFeedQuery{Filter: filter}); len(rows) != n {
				t.Errorf("filter %s lists %d feeds, counted %d", filter, len(rows), n)
			}
		}
		// No search counts every feed, these included.
		all, err := repo.CountFeeds(t.Context(), "")
		if err != nil {
			t.Fatalf("CountFeeds(\"\"): %v", err)
		}
		if all[rss.AdminFeedsAll] < 6 {
			t.Errorf("CountFeeds(\"\") counts %d feeds, want at least this test's 6", all[rss.AdminFeedsAll])
		}
	})

	t.Run("search", func(t *testing.T) {
		got := ids(list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Q: host + "-nothing"}))
		if len(got) != 0 {
			t.Errorf("search for another host = %v, want none", got)
		}
		// A title, any case; LIKE's wildcards are only themselves.
		rows := list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Q: "two FAILURES"})
		if !slices.Contains(ids(rows), twoFailures) {
			t.Errorf("search for a title lacks its feed")
		}
		if got := list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Q: host[:len(host)-4] + "%"}); len(got) != 0 {
			t.Errorf("search with %% = %d rows, want none", len(got))
		}
	})

	t.Run("columns", func(t *testing.T) {
		for _, r := range list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll}) {
			if r.SiteHost != host {
				t.Errorf("feed %s SiteHost = %q, want %q", r.Title, r.SiteHost, host)
			}
			if r.ID == healthy && r.Subscribers != 2 {
				t.Errorf("Healthy has %d subscribers, want 2", r.Subscribers)
			}
		}
	})

	// Each order sorts by its key, then ID in the same direction, and pages
	// of 2 through the cursor give the same rows.
	sorts := []struct {
		sort string
		less func(a, b rss.AdminFeedRow) int
	}{
		{rss.AdminFeedsBySubscribers, func(a, b rss.AdminFeedRow) int {
			return cmp.Or(cmp.Compare(b.Subscribers, a.Subscribers), cmp.Compare(b.ID, a.ID))
		}},
		{rss.AdminFeedsByPosts, func(a, b rss.AdminFeedRow) int {
			return cmp.Or(cmp.Compare(b.PostsPerWeek, a.PostsPerWeek), cmp.Compare(b.ID, a.ID))
		}},
		{rss.AdminFeedsByFailures, func(a, b rss.AdminFeedRow) int {
			return cmp.Or(cmp.Compare(b.ConsecutiveFailures, a.ConsecutiveFailures), cmp.Compare(b.ID, a.ID))
		}},
		{rss.AdminFeedsByLastSuccess, func(a, b rss.AdminFeedRow) int {
			return cmp.Or(a.LastFetched.Compare(b.LastFetched), cmp.Compare(a.ID, b.ID))
		}},
	}
	for _, tt := range sorts {
		t.Run("sort "+tt.sort, func(t *testing.T) {
			all := list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Sort: tt.sort})
			if len(all) != 6 {
				t.Fatalf("%d rows, want 6", len(all))
			}
			if !slices.IsSortedFunc(all, tt.less) {
				t.Errorf("not sorted: %v", ids(all))
			}
			var paged []rss.AdminFeedRow
			after := ""
			for range 4 {
				page := list(rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Sort: tt.sort, After: after, Limit: 2})
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

	t.Run("unknown filter or order", func(t *testing.T) {
		if _, err := repo.ListFeeds(t.Context(), rss.AdminFeedQuery{Filter: "bogus", Sort: rss.AdminFeedsByPosts, Limit: 1}); err == nil {
			t.Error("unknown filter: no error")
		}
		if _, err := repo.ListFeeds(t.Context(), rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Sort: "bogus", Limit: 1}); err == nil {
			t.Error("unknown order: no error")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryFeedAndSite(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	siteID, host := adminFeedsSite(t, db)
	u := newTestUser(t, db)
	feedID := adminFeed(t, db, siteID, "B feed", `last_error = 'status 500', categories = '{Tech,News}'`, u)
	other := adminFeed(t, db, siteID, "A feed", ``)

	published := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var articleID string
	if err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, published_at, timeline_at) VALUES ($1, 'a', $2, $2) RETURNING id`, feedID, published).Scan(&articleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO articles (feed_id, external_id, timeline_at) VALUES ($1, 'b', '2020-01-01')`, feedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO enrich_jobs (article_id) VALUES ($1)`, articleID); err != nil {
		t.Fatal(err)
	}

	f, err := repo.Feed(t.Context(), feedID)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if f.Title != "B feed" || f.LastError != "status 500" || f.SiteHost != host || f.Site.ID != siteID || f.Site.Host != host || f.Subscribers != 1 {
		t.Errorf("Feed = %+v", f)
	}
	if want := []string{"Tech", "News"}; !slices.Equal(f.Categories, want) {
		t.Errorf("Feed categories = %q, want %q", f.Categories, want)
	}
	if f.Articles != 2 || f.EnrichJobs != 1 || f.OldestEnrichJob.IsZero() {
		t.Errorf("Feed counts: %d articles, %d jobs, oldest %v; want 2, 1, set", f.Articles, f.EnrichJobs, f.OldestEnrichJob)
	}
	// The second article arrived now, after the first was published.
	if f.NewestArticleAt.Before(published) {
		t.Errorf("NewestArticleAt = %v, want after %v", f.NewestArticleAt, published)
	}
	if _, err := repo.Feed(t.Context(), "00000000-0000-4000-8000-000000000000"); err != rss.ErrNoRecord {
		t.Errorf("Feed of no feed: %v, want ErrNoRecord", err)
	}

	s, err := repo.Site(t.Context(), siteID)
	if err != nil {
		t.Fatalf("Site: %v", err)
	}
	if s.Site.Host != host || s.Lookup.ID != "" || s.Icon.Icon.SiteID != "" {
		t.Errorf("Site = %+v, want its host, no lookup and no icon", s)
	}
	if len(s.Feeds) != 2 || s.Feeds[0].ID != other || s.Feeds[1].ID != feedID || s.Feeds[1].Subscribers != 1 {
		t.Errorf("Site's feeds = %+v, want A feed, then B feed with 1 subscriber", s.Feeds)
	}
	if _, err := repo.Site(t.Context(), "00000000-0000-4000-8000-000000000000"); err != rss.ErrNoRecord {
		t.Errorf("Site of no site: %v, want ErrNoRecord", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryActions(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	by := testActor(t, db)
	siteID, host := adminFeedsSite(t, db)
	future := `next_fetch_at = now() + interval '1 day'`
	failing := adminFeed(t, db, siteID, "Failing", `consecutive_failures = 7, `+future)
	gone := adminFeed(t, db, siteID, "Gone", `gone_at = now(), `+future)
	notGone := adminFeed(t, db, siteID, "Fine", future)
	now := dbNow(t, db)
	missing := "00000000-0000-4000-8000-000000000000"

	var failures int
	var next time.Time
	var goneAt sql.NullTime
	read := func(id string) {
		t.Helper()
		if err := db.QueryRow(`SELECT consecutive_failures, next_fetch_at, gone_at FROM feeds WHERE id = $1`, id).Scan(&failures, &next, &goneAt); err != nil {
			t.Fatal(err)
		}
	}
	lastEntry := func(action, targetID string) rss.AuditEvent {
		t.Helper()
		for _, e := range auditBy(t, db, by) {
			if e.Action == action && e.TargetID == targetID {
				return e
			}
		}
		t.Fatalf("no %s of %s in the audit log", action, targetID)
		return rss.AuditEvent{}
	}

	if err := repo.ResetBackoff(t.Context(), failing, by); err != nil {
		t.Fatalf("ResetBackoff: %v", err)
	}
	read(failing)
	if failures != 0 || next.After(now.Add(time.Minute)) {
		t.Errorf("after ResetBackoff: %d failures, next fetch %v; want 0, now", failures, next)
	}
	e := lastEntry("feed.reset_backoff", failing)
	if e.TargetType != "feed" || e.Before["consecutive_failures"] != float64(7) || e.After["consecutive_failures"] != float64(0) {
		t.Errorf("reset entry = %+v", e)
	}

	if err := repo.ClearGone(t.Context(), gone, by); err != nil {
		t.Fatalf("ClearGone: %v", err)
	}
	read(gone)
	if goneAt.Valid || next.After(now.Add(time.Minute)) {
		t.Errorf("after ClearGone: gone %v, next fetch %v; want not gone, now", goneAt, next)
	}
	e = lastEntry("feed.clear_gone", gone)
	if e.Before["gone_at"] == nil || e.After["gone_at"] != nil {
		t.Errorf("clear entry = %+v", e)
	}

	// A feed that isn't gone is left alone, with no record.
	if err := repo.ClearGone(t.Context(), notGone, by); err != nil {
		t.Fatalf("ClearGone of a feed not gone: %v", err)
	}
	read(notGone)
	if next.Before(now.Add(time.Hour)) {
		t.Errorf("ClearGone of a feed not gone moved its next fetch to %v", next)
	}
	for _, e := range auditBy(t, db, by) {
		if e.TargetID == notGone {
			t.Errorf("ClearGone of a feed not gone recorded %+v", e)
		}
	}

	// A site checked before, due in the future, with no subscribed feed:
	// the icon runner takes it only once CheckIcon clears icon_checked_at.
	if _, err := db.Exec(`UPDATE sites SET icon_checked_at = now() - interval '1 day', icon_next_check_at = now() + interval '30 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckIcon(t.Context(), siteID, by); err != nil {
		t.Fatalf("CheckIcon: %v", err)
	}
	var iconChecked sql.NullTime
	var iconNext time.Time
	if err := db.QueryRow(`SELECT icon_checked_at, icon_next_check_at FROM sites WHERE id = $1`, siteID).Scan(&iconChecked, &iconNext); err != nil {
		t.Fatal(err)
	}
	if iconChecked.Valid || iconNext.After(now.Add(time.Minute)) {
		t.Errorf("after CheckIcon: icon_checked_at %v, icon_next_check_at %v; want NULL, now", iconChecked, iconNext)
	}
	var due bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM sites WHERE id = $1 AND `+siteIconDue+`)`, siteID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Error("after CheckIcon, the site isn't due for the icon runner")
	}
	e = lastEntry("site.check_icon", siteID)
	if e.TargetType != "site" || e.Before["icon_checked_at"] == nil || e.After["icon_checked_at"] != nil ||
		e.Before["icon_next_check_at"] == nil || e.After["icon_next_check_at"] == nil {
		t.Errorf("check icon entry = %+v, want icon_checked_at set then NULL, and icon_next_check_at both times", e)
	}

	// A lookup that finished a moment ago runs again.
	if _, err := db.Exec(`INSERT INTO lookups (site_key, url, status, site_id, finished_at) VALUES ($1, $2, 'done', $3, now())`, host, "https://"+host+"/", siteID); err != nil {
		t.Fatal(err)
	}
	l, err := repo.CheckForFeeds(t.Context(), siteID, by)
	if err != nil {
		t.Fatalf("CheckForFeeds: %v", err)
	}
	if l.Status != rss.LookupPending || l.SiteKey != host || l.SiteID != siteID {
		t.Errorf("CheckForFeeds = %+v, want the site's lookup pending again, keeping its site", l)
	}
	var priority int
	if err := db.QueryRow(`SELECT priority FROM lookups WHERE id = $1`, l.ID).Scan(&priority); err != nil || priority != 0 {
		t.Errorf("lookup priority = %d (%v), want a reader's, 0", priority, err)
	}
	e = lastEntry("site.lookup", siteID)
	if e.After["lookup_id"] != l.ID {
		t.Errorf("lookup entry = %+v, want its lookup's ID", e)
	}

	for name, err := range map[string]error{
		"ResetBackoff": repo.ResetBackoff(t.Context(), missing, by),
		"ClearGone":    repo.ClearGone(t.Context(), missing, by),
		"CheckIcon":    repo.CheckIcon(t.Context(), missing, by),
	} {
		if err != rss.ErrNoRecord {
			t.Errorf("%s of nothing: %v, want ErrNoRecord", name, err)
		}
	}
	if _, err := repo.CheckForFeeds(t.Context(), missing, by); err != rss.ErrNoRecord {
		t.Errorf("CheckForFeeds of nothing: %v, want ErrNoRecord", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryDiscoverSections(t *testing.T) {
	db := psqltest.NewDB(t)
	sections, err := NewAdminFeedRepository(db).DiscoverSections(t.Context())
	if err != nil {
		t.Fatalf("DiscoverSections: %v", err)
	}
	// The migration's sections, in their order.
	if len(sections) < 15 || sections[0].Name != "News" || sections[0].Slug != "news" || sections[0].ID == "" {
		t.Fatalf("DiscoverSections = %+v, want News first", sections)
	}
	names := map[string]bool{}
	for _, sec := range sections {
		if names[sec.Name] {
			t.Errorf("section %q listed twice", sec.Name)
		}
		names[sec.Name] = true
	}
	if !names["Film and TV"] || !names["Travel"] {
		t.Errorf("DiscoverSections lacks the migration's sections: %+v", sections)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositorySetDiscoverSection(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	by := testActor(t, db)
	siteID, _ := adminFeedsSite(t, db)
	feed := adminFeed(t, db, siteID, "Picked", ``)
	gone := adminFeed(t, db, siteID, "Gone", `gone_at = now()`)
	missing := "00000000-0000-4000-8000-000000000000"

	sections, err := repo.DiscoverSections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := map[string]string{}
	for _, sec := range sections {
		id[sec.Name] = sec.ID
	}
	science, food, music, news := id["Science"], id["Food"], id["Music"], id["News"]

	section := func(feedID string) rss.Section {
		t.Helper()
		f, err := repo.Feed(t.Context(), feedID)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		return f.Section
	}
	entries := func(feedID string) []rss.AuditEvent {
		t.Helper()
		var out []rss.AuditEvent
		for _, e := range auditBy(t, db, by) {
			if e.Action == "feed.discover" && e.TargetID == feedID {
				out = append(out, e)
			}
		}
		return out
	}
	set := func(feedID, sectionID string) {
		t.Helper()
		if err := repo.SetDiscoverSection(t.Context(), feedID, sectionID, by); err != nil {
			t.Fatalf("SetDiscoverSection(%q): %v", sectionID, err)
		}
	}

	if got := section(feed); got.ID != "" {
		t.Fatalf("a new feed's section = %+v, want none", got)
	}
	set(feed, science)
	if got := section(feed); got.ID != science || got.Name != "Science" || got.Slug != "science" {
		t.Errorf("after setting Science: %+v", got)
	}
	set(feed, food)
	if got := section(feed); got.Name != "Food" {
		t.Errorf("after changing to Food: %+v", got)
	}
	// The section it already has: no change, no record.
	set(feed, food)
	set(feed, "")
	if got := section(feed); got.ID != "" {
		t.Errorf("after removing: %+v, want none", got)
	}
	// Newest first, each with the sections' IDs and names.
	es := entries(feed)
	want := [][2]string{{"Food", ""}, {"Science", "Food"}, {"", "Science"}}
	if len(es) != len(want) {
		t.Fatalf("%d feed.discover entries, want %d: %+v", len(es), len(want), es)
	}
	for i, w := range want {
		if es[i].TargetType != "feed" || es[i].Before["section"] != w[0] || es[i].After["section"] != w[1] ||
			es[i].Before["section_id"] != id[w[0]] || es[i].After["section_id"] != id[w[1]] {
			t.Errorf("entry %d = %+v, want %q to %q", i, es[i], w[0], w[1])
		}
	}

	for name, sectionID := range map[string]string{"an unknown section": missing, "not an ID": "Science"} {
		if err := repo.SetDiscoverSection(t.Context(), feed, sectionID, by); err != rss.ErrUnknownSection {
			t.Errorf("%s: %v, want ErrUnknownSection", name, err)
		}
	}
	if err := repo.SetDiscoverSection(t.Context(), gone, science, by); err != rss.ErrFeedGone {
		t.Errorf("adding a gone feed: %v, want ErrFeedGone", err)
	}
	if err := repo.SetDiscoverSection(t.Context(), missing, science, by); err != rss.ErrNoRecord {
		t.Errorf("a feed that doesn't exist: %v, want ErrNoRecord", err)
	}
	if section(feed).ID != "" || section(gone).ID != "" || len(entries(gone)) != 0 {
		t.Errorf("a refused change was made or recorded")
	}

	// A gone feed already in Discover can be taken out.
	set(feed, music)
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, feed); err != nil {
		t.Fatal(err)
	}
	set(feed, "")
	if got := section(feed); got.ID != "" {
		t.Errorf("after taking a gone feed out: %+v", got)
	}

	// A section with feeds under it can't be deleted, and a deleted feed's
	// row goes with it.
	other := adminFeed(t, db, siteID, "Deleted", ``)
	set(other, news)
	if _, err := db.Exec(`DELETE FROM discover_sections WHERE id = $1`, news); err == nil {
		t.Error("deleted a section with a feed under it")
	}
	if _, err := db.Exec(`DELETE FROM feeds WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM discover_feeds WHERE feed_id = $1`, other).Scan(&n); err != nil || n != 0 {
		t.Errorf("a deleted feed's discover_feeds rows: %d (%v), want 0", n, err)
	}
}

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
	prefix := fmt.Sprintf("admin-sites-%d-", time.Now().UnixNano())
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
