package psql

import (
	"cmp"
	"database/sql"
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
	id, err := NewSiteRepository(db).Upsert(t.Context(), rss.Site{Host: host, URL: "https://" + host + "/"})
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
	id, err := NewFeedRepository(db).Upsert(t.Context(), rss.Feed{
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
	feedID := adminFeed(t, db, siteID, "B feed", `last_error = 'status 500'`, u)
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

	if err := repo.CheckIcon(t.Context(), siteID, by); err != nil {
		t.Fatalf("CheckIcon: %v", err)
	}
	var iconNext time.Time
	if err := db.QueryRow(`SELECT icon_next_check_at FROM sites WHERE id = $1`, siteID).Scan(&iconNext); err != nil {
		t.Fatal(err)
	}
	if iconNext.After(now.Add(time.Minute)) {
		t.Errorf("icon_next_check_at = %v, want now", iconNext)
	}
	lastEntry("site.check_icon", siteID)

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
