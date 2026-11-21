package psql

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// pickArticle adds an article titled title to feed id, published at.
func pickArticle(t *testing.T, repo *AdminFeedRepository, id, title string, at time.Time) {
	t.Helper()
	if _, err := repo.DB.Exec(`INSERT INTO articles (feed_id, external_id, title, published_at, timeline_at) VALUES ($1, $2, $3, $4, $4)`,
		id, fmt.Sprintf("pick-%d", time.Now().UnixNano()), title, at); err != nil {
		t.Fatalf("insert article: %v", err)
	}
}

// skipFeed skips feed id, as the picking page's Save does.
func skipFeed(t *testing.T, repo *AdminFeedRepository, id string) {
	t.Helper()
	if _, err := repo.DB.Exec(`INSERT INTO discover_skips (feed_id) VALUES ($1)`, id); err != nil {
		t.Fatalf("skip: %v", err)
	}
}

// isSkipped reports whether feed id is skipped.
func isSkipped(t *testing.T, repo *AdminFeedRepository, id string) bool {
	t.Helper()
	var ok bool
	if err := repo.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM discover_skips WHERE feed_id = $1)`, id).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// seedPick puts site siteID's host on the seed list, added at, with the
// section hint sectionID ("" for none) and source, and a lookup that found
// the site.
func seedPick(t *testing.T, repo *AdminFeedRepository, host, siteID, sectionID, source string, at time.Time) {
	t.Helper()
	seedSite(t, repo.DB, host, "https://"+host+"/", at)
	if _, err := repo.DB.Exec(`UPDATE seed_sites SET section_id = NULLIF($2, '')::uuid, source = $3 WHERE site_key = $1`,
		host, sectionID, source); err != nil {
		t.Fatal(err)
	}
	setLookup(t, NewLookupRepository(repo.DB), host, "done", siteID, "")
}

// pickPages reads every page of the picking page under filter, limit
// sites at a time, following the cursor, and returns the sites whose IDs
// are in ids, in order. A repeated site fails the test.
func pickPages(t *testing.T, repo *AdminFeedRepository, filter string, limit int, ids []string) []rss.AdminPickSite {
	t.Helper()
	var out []rss.AdminPickSite
	seen := map[string]bool{}
	after := ""
	for {
		page, err := repo.PickSites(t.Context(), rss.AdminPickQuery{Filter: filter, After: after, Limit: limit})
		if err != nil {
			t.Fatalf("PickSites(%s, after %q): %v", filter, after, err)
		}
		if len(page) > limit {
			t.Fatalf("PickSites returned %d sites, over the limit %d", len(page), limit)
		}
		for _, s := range page {
			if seen[s.ID] {
				t.Fatalf("PickSites(%s) repeats %s", filter, s.Host)
			}
			seen[s.ID] = true
			if slices.Contains(ids, s.ID) {
				out = append(out, s)
			}
		}
		if len(page) < limit {
			return out
		}
		after = page[len(page)-1].ID
	}
}

// pickFeedIDs are the IDs of s's feeds, in order.
func pickFeedIDs(s rss.AdminPickSite) []string {
	var out []string
	for _, f := range s.Feeds {
		out = append(out, f.ID)
	}
	return out
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryPickSites(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	now := time.Now()

	// Created first, so they're deleted after the feeds under them.
	catalogSec := newTestSection(t, db, "Pick catalog", 1)
	hintSec := newTestSection(t, db, "Pick hint", 2)

	// Sites are created oldest first, so All lists them in reverse.
	mixedID, mixed := adminFeedsSite(t, db)
	putInSection(t, db, adminFeed(t, db, mixedID, "In the catalog", ""), catalogSec)
	skipped := adminFeed(t, db, mixedID, "Skipped", "")
	skipFeed(t, repo, skipped)
	adminFeed(t, db, mixedID, "Gone", "gone_at = now()")
	candidate := adminFeed(t, db, mixedID, "Candidate", "posts_per_week = 2.5")
	seedPick(t, repo, mixed, mixedID, "", "IndieWeb list", now.Add(-2*time.Hour))

	hintedID, hinted := adminFeedsSite(t, db)
	only := adminFeed(t, db, hintedID, "Only", "")
	seedPick(t, repo, hinted, hintedID, hintSec.ID, "waitlist", now.Add(-time.Hour))

	readerID, _ := adminFeedsSite(t, db)
	adminFeed(t, db, readerID, "Looked up by a reader", "")

	manyID, many := adminFeedsSite(t, db)
	var manyFeeds []string
	for i := range 25 {
		id := adminFeed(t, db, manyID, fmt.Sprintf("Many %02d", i), "")
		pickArticle(t, repo, id, "Post", now.Add(-time.Duration(i)*time.Hour))
		manyFeeds = append(manyFeeds, id)
	}
	seedPick(t, repo, many, manyID, "", "", now.Add(-3*time.Hour))

	commentsID, commentsHost := adminFeedsSite(t, db)
	comments := adminFeed(t, db, commentsID, "Comments for Example", "")
	pickArticle(t, repo, comments, "Re: newest", now)
	posts := adminFeed(t, db, commentsID, "Posts", "")
	for i, title := range []string{"Fourth", "Third", "Second", "First"} {
		pickArticle(t, repo, posts, title, now.Add(-time.Duration(4-i)*time.Hour))
	}
	quiet := adminFeed(t, db, commentsID, "Quiet", "")
	seedPick(t, repo, commentsHost, commentsID, "", "", now.Add(-4*time.Hour))

	decidedID, decided := adminFeedsSite(t, db)
	putInSection(t, db, adminFeed(t, db, decidedID, "Decided", ""), catalogSec)
	seedPick(t, repo, decided, decidedID, "", "", now)

	ids := []string{mixedID, hintedID, readerID, manyID, commentsID, decidedID}
	site := func(sites []rss.AdminPickSite, id string) rss.AdminPickSite {
		t.Helper()
		for _, s := range sites {
			if s.ID == id {
				return s
			}
		}
		t.Fatalf("site %s isn't listed", id)
		return rss.AdminPickSite{}
	}
	order := func(sites []rss.AdminPickSite) []string {
		var out []string
		for _, s := range sites {
			out = append(out, s.ID)
		}
		return out
	}

	for _, limit := range []int{1, 2, 25} {
		t.Run(fmt.Sprintf("seed, %d a page", limit), func(t *testing.T) {
			got := pickPages(t, repo, rss.AdminPickSeed, limit, ids)
			// Newest seed entry first; the reader's site and the decided
			// one aren't listed.
			if want := []string{hintedID, mixedID, manyID, commentsID}; !slices.Equal(order(got), want) {
				t.Errorf("seed order = %v, want %v", order(got), want)
			}
		})
		t.Run(fmt.Sprintf("all, %d a page", limit), func(t *testing.T) {
			got := pickPages(t, repo, rss.AdminPickAll, limit, ids)
			if want := []string{commentsID, manyID, readerID, hintedID, mixedID}; !slices.Equal(order(got), want) {
				t.Errorf("all order = %v, want %v", order(got), want)
			}
		})
	}

	sites := pickPages(t, repo, rss.AdminPickAll, 100, ids)

	t.Run("candidates", func(t *testing.T) {
		m := site(sites, mixedID)
		if got := pickFeedIDs(m); !slices.Equal(got, []string{candidate}) {
			t.Errorf("mixed site's feeds = %v, want only the candidate", got)
		}
		if f := m.Feeds[0]; f.Title != "Candidate" || f.PostsPerWeek != 2.5 || !f.LatestPost.IsZero() || len(f.Latest) != 0 || f.Comments {
			t.Errorf("candidate = %+v", f)
		}
		if m.Source != "IndieWeb list" || m.More != 0 {
			t.Errorf("mixed site's source %q, more %d", m.Source, m.More)
		}
	})

	t.Run("hints", func(t *testing.T) {
		if h := site(sites, hintedID); h.Hint.ID != hintSec.ID || h.Hint.Name != hintSec.Name || !h.HintSeed || h.Source != "waitlist" {
			t.Errorf("hinted site = hint %+v, seed %v, source %q; want the seed entry's", h.Hint, h.HintSeed, h.Source)
		}
		if got := pickFeedIDs(site(sites, hintedID)); !slices.Equal(got, []string{only}) {
			t.Errorf("hinted site's feeds = %v", got)
		}
		// No seed hint: the section of the site's feed in the catalog.
		if m := site(sites, mixedID); m.Hint.ID != catalogSec.ID || m.HintSeed {
			t.Errorf("mixed site's hint = %+v (seed %v), want the catalog's", m.Hint, m.HintSeed)
		}
		if r := site(sites, readerID); r.Hint.ID != "" || r.HintSeed || r.Source != "" {
			t.Errorf("reader's site = hint %+v, source %q; want none", r.Hint, r.Source)
		}
	})

	t.Run("at most 20 feeds", func(t *testing.T) {
		m := site(sites, manyID)
		// Most recently posting first.
		if got := pickFeedIDs(m); !slices.Equal(got, manyFeeds[:20]) {
			t.Errorf("many feeds = %v, want the 20 newest", got)
		}
		if m.More != 5 {
			t.Errorf("More = %d, want 5", m.More)
		}
	})

	t.Run("comments last and latest titles", func(t *testing.T) {
		c := site(sites, commentsID)
		if got := pickFeedIDs(c); !slices.Equal(got, []string{posts, quiet, comments}) {
			t.Errorf("feeds = %v, want posts, quiet, comments", got)
		}
		p := c.Feeds[0]
		if !slices.Equal(p.Latest, []string{"First", "Second", "Third"}) {
			t.Errorf("latest = %q, want the 3 newest", p.Latest)
		}
		if d := p.LatestPost.Sub(now.Add(-time.Hour)); d < -time.Second || d > time.Second {
			t.Errorf("latest post %v, want an hour ago", p.LatestPost)
		}
		if p.Comments || !c.Feeds[2].Comments {
			t.Errorf("comments = %v, %v; want false, true", p.Comments, c.Feeds[2].Comments)
		}
	})

	t.Run("skipped", func(t *testing.T) {
		got := pickPages(t, repo, rss.AdminPickSkipped, 1, ids)
		if !slices.Equal(order(got), []string{mixedID}) {
			t.Fatalf("skipped sites = %v, want the mixed one", order(got))
		}
		if ids := pickFeedIDs(got[0]); !slices.Equal(ids, []string{skipped}) {
			t.Errorf("skipped feeds = %v", ids)
		}
	})

	t.Run("cursor", func(t *testing.T) {
		// Past the end, an unknown site and not a UUID: nothing.
		last := []rss.AdminPickQuery{
			{Filter: rss.AdminPickSeed, After: "00000000-0000-4000-8000-000000000000", Limit: 5},
			{Filter: rss.AdminPickAll, After: "nope", Limit: 5},
			// A site not on the seed list has no place in its order.
			{Filter: rss.AdminPickSeed, After: readerID, Limit: 5},
		}
		for _, q := range last {
			if got, err := repo.PickSites(t.Context(), q); err != nil || len(got) != 0 {
				t.Errorf("PickSites(%+v) = %d sites, %v; want none", q, len(got), err)
			}
		}
		if _, err := repo.PickSites(t.Context(), rss.AdminPickQuery{Filter: "nope", Limit: 5}); err == nil {
			t.Error("an unknown filter isn't an error")
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryCountPick(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	before, err := repo.CountPick(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	seededID, seeded := adminFeedsSite(t, db)
	adminFeed(t, db, seededID, "Seeded", "")
	adminFeed(t, db, seededID, "Seeded too", "")
	seedPick(t, repo, seeded, seededID, "", "", time.Now())
	readerID, _ := adminFeedsSite(t, db)
	adminFeed(t, db, readerID, "Reader", "")
	skipFeed(t, repo, adminFeed(t, db, readerID, "Skipped", ""))
	goneID, _ := adminFeedsSite(t, db)
	adminFeed(t, db, goneID, "Gone", "gone_at = now()")

	after, err := repo.CountPick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for filter, want := range map[string]int{rss.AdminPickSeed: 1, rss.AdminPickAll: 2, rss.AdminPickSkipped: 1} {
		if got := after[filter] - before[filter]; got != want {
			t.Errorf("%s counts %d more sites, want %d", filter, got, want)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositorySavePicks(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	by := testActor(t, db)
	sec := newTestSection(t, db, "Picks", 1)
	siteID, _ := adminFeedsSite(t, db)
	picked := adminFeed(t, db, siteID, "Picked", "")
	wasSkipped := adminFeed(t, db, siteID, "Was skipped", "")
	skipFeed(t, repo, wasSkipped)
	toSkip := adminFeed(t, db, siteID, "To skip", "")
	alreadySkipped := adminFeed(t, db, siteID, "Already skipped", "")
	skipFeed(t, repo, alreadySkipped)
	gone := adminFeed(t, db, siteID, "Gone", "gone_at = now()")
	other := adminFeed(t, db, siteID, "Other", "")
	otherSkip := adminFeed(t, db, siteID, "Other skip", "")

	sectionOf := func(id string) string {
		t.Helper()
		f, err := repo.Feed(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return f.Section.ID
	}
	actions := func(id string) []string {
		t.Helper()
		var out []string
		for _, e := range auditBy(t, db, by) {
			if e.TargetID == id {
				out = append(out, e.Action)
			}
		}
		return out
	}

	err := repo.SavePicks(t.Context(),
		[]rss.DiscoverPick{{FeedID: picked, SectionID: sec.ID}, {FeedID: wasSkipped, SectionID: sec.ID}},
		[]string{toSkip, alreadySkipped}, by)
	if err != nil {
		t.Fatalf("SavePicks: %v", err)
	}
	if sectionOf(picked) != sec.ID || sectionOf(wasSkipped) != sec.ID {
		t.Error("the picks aren't in the catalog")
	}
	if isSkipped(t, repo, wasSkipped) || !isSkipped(t, repo, toSkip) || !isSkipped(t, repo, alreadySkipped) {
		t.Error("skips: want the picked feed unskipped and both skips kept")
	}
	// Newest first.
	for id, want := range map[string][]string{
		picked:         {"feed.discover"},
		wasSkipped:     {"feed.discover", "feed.discover_unskip"},
		toSkip:         {"feed.discover_skip"},
		alreadySkipped: nil,
	} {
		if got := actions(id); !slices.Equal(got, want) {
			t.Errorf("audit of %s = %v, want %v", id, got, want)
		}
	}

	// Any error saves nothing and names the feed.
	for name, tc := range map[string]struct {
		pick rss.DiscoverPick
		want error
	}{
		"a gone feed":        {rss.DiscoverPick{FeedID: gone, SectionID: sec.ID}, rss.ErrFeedGone},
		"an unknown section": {rss.DiscoverPick{FeedID: gone, SectionID: "00000000-0000-4000-8000-000000000000"}, rss.ErrUnknownSection},
		"a missing feed":     {rss.DiscoverPick{FeedID: "00000000-0000-4000-8000-000000000000", SectionID: sec.ID}, rss.ErrNoRecord},
	} {
		err := repo.SavePicks(t.Context(), []rss.DiscoverPick{{FeedID: other, SectionID: sec.ID}, tc.pick}, []string{otherSkip}, by)
		var pe *rss.PickError
		if !errors.As(err, &pe) || pe.FeedID != tc.pick.FeedID || !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want a PickError for %s wrapping %v", name, err, tc.pick.FeedID, tc.want)
		}
		if sectionOf(other) != "" || isSkipped(t, repo, otherSkip) || len(actions(other)) != 0 {
			t.Errorf("%s: the rest of the save was kept", name)
		}
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryUnskip(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	by := testActor(t, db)
	sec := newTestSection(t, db, "Unskip", 1)
	siteID, _ := adminFeedsSite(t, db)
	feed := adminFeed(t, db, siteID, "Skipped", "")
	skipFeed(t, repo, feed)

	if err := repo.Unskip(t.Context(), feed, by); err != nil {
		t.Fatalf("Unskip: %v", err)
	}
	if isSkipped(t, repo, feed) {
		t.Error("still skipped")
	}
	if err := repo.Unskip(t.Context(), feed, by); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Unskip again: %v, want ErrNoRecord", err)
	}
	es := auditBy(t, db, by)
	if len(es) != 1 || es[0].Action != "feed.discover_unskip" || es[0].TargetType != "feed" || es[0].TargetID != feed {
		t.Errorf("audit = %+v, want one feed.discover_unskip", es)
	}

	// Putting a skipped feed in the catalog from its page unskips it.
	skipFeed(t, repo, feed)
	if err := repo.SetDiscoverSection(t.Context(), feed, sec.ID, by); err != nil {
		t.Fatal(err)
	}
	if isSkipped(t, repo, feed) {
		t.Error("SetDiscoverSection left the feed skipped")
	}
}
