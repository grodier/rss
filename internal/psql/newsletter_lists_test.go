package psql

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// The tests in this file check that lists and searches across readers leave
// out newsletter feeds (#846). Some read whole tables (the admin's counts,
// the picking page), so none of them is parallel.

// newOwnedFeedOn makes a newsletter feed of ownerID, titled title, on
// siteID, an ordinary web site. Mail only makes them on the newsletters'
// internal site, which the site conditions already leave out, but nothing in
// the schema keeps them there: this checks each list's feed condition on
// its own. The owner follows it; it's deleted on cleanup.
func newOwnedFeedOn(t *testing.T, db *sql.DB, ownerID, siteID, title string) rss.Feed {
	t.Helper()
	var id string
	err := db.QueryRow(`WITH n AS (SELECT gen_random_uuid() AS id)
		INSERT INTO feeds (id, site_id, url, title, owner_id)
		SELECT n.id, $1, 'newsletter:' || n.id, $2, $3 FROM n
		RETURNING id`, siteID, title, ownerID).Scan(&id)
	if err != nil {
		t.Fatalf("insert owned feed: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM feeds WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup owned feed: %v", err)
		}
	})
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), ownerID, id); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return rss.Feed{ID: id, SiteID: siteID, Title: title, OwnerID: ownerID}
}

// feedIDs returns the IDs of feeds.
func feedIDs(feeds []rss.Feed) []string {
	ids := make([]string, len(feeds))
	for i, f := range feeds {
		ids[i] = f.ID
	}
	return ids
}

func TestNewsletterFeedsNotSearched(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSearchRepository(db)
	owner := newTestUser(t, db)
	siteID := newTestSite(t, db)
	title := fmt.Sprintf("Secret Letter %d", psqltest.Unique())
	owned := newOwnedFeedOn(t, db, owner, siteID, title)
	web := newTestFeedOn(t, db, siteID)
	var host string
	if err := db.QueryRow(`SELECT host FROM sites WHERE id = $1`, siteID).Scan(&host); err != nil {
		t.Fatal(err)
	}

	// By the newsletter's title, its site isn't found: no one can probe
	// whether some reader receives a newsletter.
	sites, _, err := repo.Search(t.Context(), title, "", "", 100)
	if err != nil {
		t.Fatalf("Search(title): %v", err)
	}
	for _, s := range sites {
		if s.Site.ID == siteID {
			t.Errorf("Search(%q) found the site through its newsletter feed", title)
		}
	}

	// By the site's host, the site is found without the newsletter feed.
	sites, _, err = repo.Search(t.Context(), host, host, "", 100)
	if err != nil {
		t.Fatalf("Search(host): %v", err)
	}
	if len(sites) != 1 || sites[0].Site.ID != siteID {
		t.Fatalf("Search(%q) = %d sites, want the site", host, len(sites))
	}
	if got := feedIDs(sites[0].Feeds); !slices.Equal(got, []string{web.ID}) {
		t.Errorf("the site's feeds = %v, want only the web feed %s (not %s)", got, web.ID, owned.ID)
	}
}

func TestNewsletterFeedsNotMatchedForSignup(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)
	owner := newTestUser(t, db)
	newOwnedFeed(t, db, owner)

	// A waitlist answer of "Newsletters" never matches the internal site.
	matches, err := repo.MatchForSignup(t.Context(), []string{"Newsletters", "newsletters"})
	if err != nil {
		t.Fatalf("MatchForSignup: %v", err)
	}
	for _, m := range matches {
		if m.Site != nil && m.Site.Host == rss.NewsletterSiteHost {
			t.Errorf("MatchForSignup(%q) matched the newsletters' site", m.Candidate)
		}
		if m.Feed != nil && m.Feed.IsNewsletter() {
			t.Errorf("MatchForSignup(%q) offered a newsletter feed", m.Candidate)
		}
	}

	// A site's only feed to follow is never a newsletter feed on it.
	siteID := newTestSite(t, db)
	newOwnedFeedOn(t, db, owner, siteID, "A newsletter")
	web := newTestFeedOn(t, db, siteID)
	var url string
	if err := db.QueryRow(`SELECT url FROM sites WHERE id = $1`, siteID).Scan(&url); err != nil {
		t.Fatal(err)
	}
	matches, err = repo.MatchForSignup(t.Context(), []string{url})
	if err != nil {
		t.Fatalf("MatchForSignup(url): %v", err)
	}
	if m := matches[0]; m.Feed == nil || m.Feed.ID != web.ID {
		t.Errorf("MatchForSignup(%q).Feed = %+v, want the web feed %s alone", url, m.Feed, web.ID)
	}
}

func TestNewsletterFeedsNotPicked(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	owner := newTestUser(t, db)
	// One site whose only feed is a newsletter, still to decide on, and one
	// whose only feed is a newsletter that was skipped.
	candidate, skipped := newTestSite(t, db), newTestSite(t, db)
	newOwnedFeedOn(t, db, owner, candidate, "A newsletter")
	newOwnedFeedOn(t, db, owner, skipped, "Another newsletter")
	if _, err := db.Exec(`INSERT INTO discover_skips (feed_id) SELECT id FROM feeds WHERE site_id = $1`, skipped); err != nil {
		t.Fatalf("skip it: %v", err)
	}

	for filter, siteID := range map[string]string{rss.AdminPickAll: candidate, rss.AdminPickSkipped: skipped} {
		sites, err := repo.PickSites(t.Context(), rss.AdminPickQuery{Filter: filter, Limit: 1000})
		if err != nil {
			t.Fatalf("PickSites(%s): %v", filter, err)
		}
		for _, s := range sites {
			if s.Site.ID == siteID {
				t.Errorf("PickSites(%s) listed a site whose only feed is a newsletter", filter)
			}
		}
	}

	// The counts don't count them either: a web feed added to each site
	// adds it to the count.
	before, err := repo.CountPick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	newTestFeedOn(t, db, candidate)
	web := newTestFeedOn(t, db, skipped)
	if _, err := db.Exec(`INSERT INTO discover_skips (feed_id) VALUES ($1)`, web.ID); err != nil {
		t.Fatal(err)
	}
	after, err := repo.CountPick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{rss.AdminPickAll, rss.AdminPickSkipped} {
		if after[filter] != before[filter]+1 {
			t.Errorf("CountPick(%s) went %d → %d with a web feed added, want +1: the newsletter counted before", filter, before[filter], after[filter])
		}
	}
}

func TestNewsletterFeedsNotShared(t *testing.T) {
	db := psqltest.NewDB(t)
	articles := NewArticleRepository(db)
	owner := newTestUser(t, db)
	canonical := fmt.Sprintf("https://example.com/story-%d", psqltest.Unique())

	// The owner's newsletter and a web feed carry the same story, the
	// newsletter's copy first.
	letter := newOwnedFeed(t, db, owner, rss.Article{ExternalID: "n1", URL: canonical + "?u=personal", CanonicalURL: canonical, Title: "From the letter"})
	web := newSubscribedFeed(t, db, owner)
	if _, err := saveArticles(t.Context(), db, web.ID, []rss.Article{{ExternalID: "w1", URL: canonical, CanonicalURL: canonical, Title: "From the web"}}); err != nil {
		t.Fatalf("saveArticles: %v", err)
	}
	letterArticle := articleID(t, db, letter.ID, "n1")
	webArticle := articleID(t, db, web.ID, "w1")
	if _, err := db.Exec(`UPDATE articles SET timeline_at = timeline_at - interval '1 hour' WHERE id = $1`, letterArticle); err != nil {
		t.Fatal(err)
	}
	// One star covers every copy: starred through the web copy, the story
	// shows on the starred-only timeline. (Starred through the newsletter's
	// copy, it's left out, like the newsletter: see "A story starred
	// through a newsletter" in docs/backlog.md.)
	if err := NewStarRepository(db).Star(t.Context(), owner, webArticle); err != nil {
		t.Fatalf("Star: %v", err)
	}

	for _, starred := range []bool{false, true} {
		t.Run(fmt.Sprintf("starred only %v", starred), func(t *testing.T) {
			items, err := articles.ListShared(t.Context(), owner, rss.TimelineFilter{StarredOnly: starred}, 50)
			if err != nil {
				t.Fatalf("ListShared: %v", err)
			}
			var ids []string
			for _, it := range items {
				ids = append(ids, it.Article.ID)
			}
			if slices.Contains(ids, letterArticle) {
				t.Error("the shared timeline lists the newsletter's article")
			}
			// The newsletter's earlier copy doesn't collapse the web one away.
			if !slices.Contains(ids, webArticle) {
				t.Error("the shared timeline left out the web copy")
			}
		})
	}
}

func TestNewsletterFeedsNotOnSitePages(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	siteID := newTestSite(t, db)
	owned := newOwnedFeedOn(t, db, newTestUser(t, db), siteID, "A newsletter")
	web := newTestFeedOn(t, db, siteID)

	feeds, err := repo.ListBySite(t.Context(), siteID)
	if err != nil {
		t.Fatalf("ListBySite: %v", err)
	}
	if got := feedIDs(feeds); !slices.Equal(got, []string{web.ID}) {
		t.Errorf("ListBySite = %v, want only the web feed %s", got, web.ID)
	}
	facts, err := repo.SiteFeedFacts(t.Context(), siteID)
	if err != nil {
		t.Fatalf("SiteFeedFacts: %v", err)
	}
	if _, ok := facts[owned.ID]; ok {
		t.Error("SiteFeedFacts has the newsletter feed")
	}
	if _, ok := facts[web.ID]; !ok {
		t.Error("SiteFeedFacts lacks the web feed")
	}
}

func TestNewsletterFeedsNotInAdminFeedList(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	title := fmt.Sprintf("Secret Letter %d", psqltest.Unique())
	newOwnedFeedOn(t, db, newTestUser(t, db), newTestSite(t, db), title)
	newOwnedFeed(t, db, newTestUser(t, db))

	for _, q := range []string{title, "newsletter:"} {
		rows, err := repo.ListFeeds(t.Context(), rss.AdminFeedQuery{Filter: rss.AdminFeedsAll, Sort: rss.AdminFeedsBySubscribers, Q: q, Limit: 100})
		if err != nil {
			t.Fatalf("ListFeeds(%q): %v", q, err)
		}
		for _, r := range rows {
			if r.Feed.IsNewsletter() || r.SiteHost == rss.NewsletterSiteHost {
				t.Errorf("ListFeeds(%q) listed a newsletter feed", q)
			}
		}
		counts, err := repo.CountFeeds(t.Context(), q)
		if err != nil {
			t.Fatalf("CountFeeds(%q): %v", q, err)
		}
		if counts[rss.AdminFeedsAll] != len(rows) {
			t.Errorf("CountFeeds(%q) = %d, want %d, the feeds listed", q, counts[rss.AdminFeedsAll], len(rows))
		}
	}
}
