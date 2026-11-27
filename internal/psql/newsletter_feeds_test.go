package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// The tests in this file read whole tables (the refresh, icon and
// enrichment queues' conditions, the Overview's counts) or change the
// newsletters' shared site, so none of them is parallel.

// newsletterSiteID returns the ID of the newsletters' internal site, made by
// its migration.
func newsletterSiteID(t *testing.T, db *sql.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT id FROM sites WHERE host = $1`, rss.NewsletterSiteHost).Scan(&id); err != nil {
		t.Fatalf("select the newsletters' site: %v", err)
	}
	return id
}

// newOwnedFeed makes a newsletter feed of ownerID directly in the
// database, as mail will: on the newsletters' site, with the URL
// newsletter:<id> and next_fetch_at left at its default (due). The owner
// follows it, then articles are saved to it (saveArticles). Deleting the
// owner deletes it; it's also deleted on cleanup.
func newOwnedFeed(t *testing.T, db *sql.DB, ownerID string, articles ...rss.Article) rss.Feed {
	t.Helper()
	var id string
	err := db.QueryRow(`WITH n AS (SELECT gen_random_uuid() AS id)
		INSERT INTO feeds (id, site_id, url, title, owner_id)
		SELECT n.id, s.id, 'newsletter:' || n.id, 'A newsletter', $1
		FROM n, sites s WHERE s.host = $2
		RETURNING id`, ownerID, rss.NewsletterSiteHost).Scan(&id)
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
	if len(articles) > 0 {
		if _, err := saveArticles(t.Context(), db, id, articles); err != nil {
			t.Fatalf("saveArticles: %v", err)
		}
	}
	feed, err := NewFeedRepository(db).GetVisible(t.Context(), ownerID, id)
	if err != nil {
		t.Fatalf("GetVisible: %v", err)
	}
	return feed
}

// newsletterIssue is an article of a newsletter that enrichment would want
// if its feed were an ordinary one: a URL, and no image.
func newsletterIssue(externalID string) rss.Article {
	return rss.Article{ExternalID: externalID, URL: "https://example.com/" + externalID + "?u=personal", Title: "Issue " + externalID}
}

// articleID returns the ID of feedID's article externalID.
func articleID(t *testing.T, db *sql.DB, feedID, externalID string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT id FROM articles WHERE feed_id = $1 AND external_id = $2`, feedID, externalID).Scan(&id); err != nil {
		t.Fatalf("select article %s: %v", externalID, err)
	}
	return id
}

// feedEnrichJobs counts feedID's articles' enrichment jobs.
func feedEnrichJobs(t *testing.T, db *sql.DB, feedID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM enrich_jobs j JOIN articles a ON a.id = j.article_id WHERE a.feed_id = $1`, feedID).Scan(&n); err != nil {
		t.Fatalf("count enrich jobs: %v", err)
	}
	return n
}

// refreshQueue is the Overview's count of the refresh queue.
func refreshQueue(t *testing.T, db *sql.DB) int {
	t.Helper()
	queues, err := NewAdminOverviewRepository(db).Queues(t.Context())
	if err != nil {
		t.Fatalf("Queues: %v", err)
	}
	for _, q := range queues {
		if q.Name == "refresh" {
			return q.Jobs
		}
	}
	t.Fatal("no refresh queue")
	return 0
}

func TestNewsletterFeedNeverFetched(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewFeedRepository(db)
	owner := newTestUser(t, db)
	owned := newOwnedFeed(t, db, owner)
	if !owned.IsNewsletter() || owned.OwnerID != owner {
		t.Fatalf("owned feed = %+v, want a newsletter of %s", owned, owner)
	}
	// Due before anything else and requested, so ClaimDue would take it
	// first; web is the feed it should take instead.
	setNextFetch(t, db, owned.ID, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC))
	setRefreshRequested(t, db, owned.ID)
	web := newSubscribedFeed(t, db, owner)
	setNextFetch(t, db, web.ID, time.Date(1999, 1, 2, 0, 0, 0, 0, time.UTC))
	setRefreshRequested(t, db, web.ID)

	t.Run("ClaimDue", func(t *testing.T) {
		got, err := repo.ClaimDue(t.Context(), time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue: %v", err)
		}
		if got.ID != web.ID {
			t.Errorf("ClaimDue = %s, want the web feed %s, never the newsletter %s", got.ID, web.ID, owned.ID)
		}
		if _, err := db.Exec(`UPDATE feeds SET next_fetch_at = '2100-01-01' WHERE id = $1`, web.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the Overview's queue", func(t *testing.T) {
		setNextFetch(t, db, owned.ID, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC))
		due := refreshQueue(t, db)
		setNextFetch(t, db, owned.ID, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
		if notDue := refreshQueue(t, db); notDue != due {
			t.Errorf("refresh queue = %d with the newsletter due, %d with it not; want them equal", due, notDue)
		}
	})

	t.Run("RequestRefresh", func(t *testing.T) {
		setNextFetch(t, db, owned.ID, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC))
		if _, err := db.Exec(`UPDATE feeds SET refresh_requested_at = NULL WHERE id = $1`, owned.ID); err != nil {
			t.Fatal(err)
		}
		if err := repo.RequestRefresh(t.Context(), owned.ID); err != nil {
			t.Fatalf("RequestRefresh: %v", err)
		}
		got, err := repo.GetByID(t.Context(), owned.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !got.RefreshRequestedAt.IsZero() {
			t.Errorf("refresh_requested_at = %v, want NULL", got.RefreshRequestedAt)
		}
	})
}

func TestNewsletterFeedGetVisible(t *testing.T) {
	db := psqltest.NewDB(t)
	feeds, articles := NewFeedRepository(db), NewArticleRepository(db)
	a, b := newTestUser(t, db), newTestUser(t, db)
	owned := newOwnedFeed(t, db, a, newsletterIssue("1"))
	ownedArticle := articleID(t, db, owned.ID, "1")
	web := newTestFeed(t, db)
	webArticle := insertDebugArticle(t, db, web.ID, "a", "", time.Now())

	t.Run("reader B", func(t *testing.T) {
		if _, err := feeds.GetVisible(t.Context(), b, owned.ID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("feed: err = %v, want ErrNoRecord", err)
		}
		if _, err := articles.GetVisible(t.Context(), b, ownedArticle); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("article: err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("reader A", func(t *testing.T) {
		f, err := feeds.GetVisible(t.Context(), a, owned.ID)
		if err != nil || f.ID != owned.ID || f.OwnerID != a || f.Url != "newsletter:"+owned.ID {
			t.Errorf("feed = %+v, %v; want theirs", f, err)
		}
		art, err := articles.GetVisible(t.Context(), a, ownedArticle)
		if err != nil || art.ID != ownedArticle || art.FeedID != owned.ID || art.Title != "Issue 1" {
			t.Errorf("article = %+v, %v; want theirs", art, err)
		}
	})

	t.Run("a web feed, for anyone", func(t *testing.T) {
		f, err := feeds.GetVisible(t.Context(), b, web.ID)
		if err != nil || f.ID != web.ID || f.IsNewsletter() {
			t.Errorf("feed = %+v, %v; want the web feed", f, err)
		}
		if art, err := articles.GetVisible(t.Context(), b, webArticle); err != nil || art.ID != webArticle {
			t.Errorf("article = %+v, %v; want the web feed's", art, err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		missing := "00000000-0000-4000-8000-000000000000"
		if _, err := feeds.GetVisible(t.Context(), a, missing); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("feed: err = %v, want ErrNoRecord", err)
		}
		if _, err := articles.GetVisible(t.Context(), a, missing); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("article: err = %v, want ErrNoRecord", err)
		}
	})
}

func TestNewsletterFeedNotEnriched(t *testing.T) {
	db := psqltest.NewDB(t)
	owner := newTestUser(t, db)

	t.Run("saveArticles", func(t *testing.T) {
		owned := newOwnedFeed(t, db, owner, newsletterIssue("1"))
		if n := feedEnrichJobs(t, db, owned.ID); n != 0 {
			t.Errorf("enrich jobs = %d, want 0", n)
		}
	})

	t.Run("ReportImageFailed", func(t *testing.T) {
		issue := newsletterIssue("1")
		issue.ImageURL, issue.Excerpt = "https://example.com/i.png", "An excerpt"
		owned := newOwnedFeed(t, db, owner, issue)
		id := articleID(t, db, owned.ID, "1")
		if err := NewArticleRepository(db).ReportImageFailed(t.Context(), id); err != nil {
			t.Fatalf("ReportImageFailed: %v", err)
		}
		if n := feedEnrichJobs(t, db, owned.ID); n != 0 {
			t.Errorf("enrich jobs = %d, want 0", n)
		}
	})

	t.Run("followed again", func(t *testing.T) {
		owned := newOwnedFeed(t, db, owner, newsletterIssue("1"))
		subs := NewSubscriptionRepository(db)
		if err := subs.Unsubscribe(t.Context(), owner, owned.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if err := subs.Subscribe(t.Context(), owner, owned.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if n := feedEnrichJobs(t, db, owned.ID); n != 0 {
			t.Errorf("enrich jobs = %d, want 0", n)
		}
	})

	t.Run("ClaimNext", func(t *testing.T) {
		// A job however it got there, which ClaimNext must never take.
		owned := newOwnedFeed(t, db, owner, newsletterIssue("1"))
		id := articleID(t, db, owned.ID, "1")
		if _, err := db.Exec(`INSERT INTO enrich_jobs (article_id, next_attempt_at) VALUES ($1, '1999-01-01')`, id); err != nil {
			t.Fatal(err)
		}
		var due bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM enrich_jobs j JOIN articles a ON a.id = j.article_id
			WHERE j.article_id = $1 AND `+enrichJobDue+`)`, id).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if due {
			t.Error("the newsletter's job is due, want it never claimed")
		}
	})
}

func TestNewsletterSite(t *testing.T) {
	db := psqltest.NewDB(t)
	siteID := newsletterSiteID(t, db)

	t.Run("never due for an icon check", func(t *testing.T) {
		// Even never checked and with a followed feed, either of which
		// makes another site due.
		newOwnedFeed(t, db, newTestUser(t, db))
		if _, err := db.Exec(`UPDATE sites SET icon_checked_at = NULL, icon_next_check_at = '1999-01-01' WHERE id = $1`, siteID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := db.Exec(`UPDATE sites SET icon_checked_at = now(), icon_next_check_at = now() WHERE id = $1`, siteID); err != nil {
				t.Errorf("restore the newsletters' site: %v", err)
			}
		})
		var due bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM sites WHERE id = $1 AND `+siteIconDue+`)`, siteID).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if due {
			t.Error("the newsletters' site is due for an icon check, want never")
		}
	})

	t.Run("upsertSite refuses it", func(t *testing.T) {
		_, err := upsertSite(t.Context(), db, rss.Site{Host: rss.NewsletterSiteHost, URL: "http://www.newsletters/", Title: "Taken"}, true)
		if !errors.Is(err, errReservedSite) {
			t.Errorf("upsertSite: err = %v, want errReservedSite", err)
		}
		got, err := NewSiteRepository(db).GetByID(t.Context(), siteID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.URL != "" || got.Title != "Newsletters" {
			t.Errorf("site = %+v, want it unchanged", got)
		}
	})
}

func TestNewsletterFeedArticleDebug(t *testing.T) {
	db := psqltest.NewDB(t)
	a, b := newTestUser(t, db), newTestUser(t, db)
	canonical := fmt.Sprintf("https://example.com/newsletter-copy-%d", psqltest.Unique())
	web := newTestFeed(t, db)
	webArticle := insertDebugArticle(t, db, web.ID, "a", canonical, time.Now())
	issue := newsletterIssue("1")
	issue.CanonicalURL = canonical
	owned := newOwnedFeed(t, db, a, issue)
	ownedArticle := articleID(t, db, owned.ID, "1")

	copies := func(userID string) []string {
		t.Helper()
		d, err := NewDebugRepository(db).ArticleDebug(t.Context(), userID, webArticle)
		if err != nil {
			t.Fatalf("ArticleDebug: %v", err)
		}
		var ids []string
		for _, c := range d.Copies {
			ids = append(ids, c.ArticleID)
		}
		return ids
	}
	if got := copies(b); len(got) != 0 {
		t.Errorf("reader B's copies = %v, want none", got)
	}
	if got := copies(a); len(got) != 1 || got[0] != ownedArticle {
		t.Errorf("reader A's copies = %v, want their newsletter's %s", got, ownedArticle)
	}
}

func TestNewsletterFeedAdmin(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)
	owned := newOwnedFeed(t, db, newTestUser(t, db))
	siteID := newsletterSiteID(t, db)
	by := rss.AuditActor{AdminEmail: "admin@example.com", Via: rss.AuditViaWeb}

	if _, err := repo.Feed(t.Context(), owned.ID); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Feed: err = %v, want ErrNoRecord", err)
	}
	if _, err := repo.Site(t.Context(), siteID); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("Site: err = %v, want ErrNoRecord", err)
	}
	if err := repo.CheckIcon(t.Context(), siteID, by); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("CheckIcon: err = %v, want ErrNoRecord", err)
	}
	if _, err := repo.CheckForFeeds(t.Context(), siteID, by); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("CheckForFeeds: err = %v, want ErrNoRecord", err)
	}
	var lookups int
	if err := db.QueryRow(`SELECT count(*) FROM lookups WHERE site_key = $1`, rss.NewsletterSiteHost).Scan(&lookups); err != nil {
		t.Fatal(err)
	}
	if lookups != 0 {
		t.Errorf("lookups of the newsletters' site = %d, want 0", lookups)
	}
}

func TestNewsletterFeedOwnerDeleted(t *testing.T) {
	db := psqltest.NewDB(t)
	owner := newTestUser(t, db)
	owned := newOwnedFeed(t, db, owner, newsletterIssue("1"))
	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	var feeds, articles int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM feeds WHERE id = $1), (SELECT count(*) FROM articles WHERE feed_id = $1)`, owned.ID).Scan(&feeds, &articles); err != nil {
		t.Fatal(err)
	}
	if feeds != 0 || articles != 0 {
		t.Errorf("after deleting the owner: %d feeds, %d articles; want none", feeds, articles)
	}
}
