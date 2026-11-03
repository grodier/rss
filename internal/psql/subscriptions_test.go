package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestUser inserts a user and deletes it (and its subscriptions) on cleanup.
// newTestUser inserts a user and deletes it when t ends. It's a bare row,
// without UserRepository.Create's default timelines, so tests start from no
// timelines; it can't log in.
func newTestUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	email := fmt.Sprintf("subs-test-%d@example.com", time.Now().UnixNano())
	var id string
	err := db.QueryRowContext(t.Context(), `INSERT INTO users (name, email, hashed_password) VALUES ('Test User', $1, '') RETURNING id`, email).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	return id
}

func subscriptionCount(t *testing.T, db *sql.DB, userID, feedID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, userID, feedID).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	t.Run("subscribe then SubscribedFeedIDs reports the feed", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		got, err := repo.SubscribedFeedIDs(ctx, user, []string{feed.ID})
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if !got[feed.ID] || len(got) != 1 {
			t.Errorf("SubscribedFeedIDs = %v, want only %s", got, feed.ID)
		}
	})

	t.Run("subscribing twice is not an error and leaves one row", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		for range 2 {
			if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
		if n := subscriptionCount(t, db, user, feed.ID); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})

	t.Run("unsubscribe removes it", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)
		if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}

		if err := repo.Unsubscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		if n := subscriptionCount(t, db, user, feed.ID); n != 0 {
			t.Errorf("rows = %d, want 0", n)
		}
	})

	t.Run("unsubscribing when not subscribed is not an error", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		if err := repo.Unsubscribe(ctx, user, feed.ID); err != nil {
			t.Errorf("Unsubscribe: %v", err)
		}
	})

	t.Run("SubscribedFeedIDs excludes other users and feeds not asked about", func(t *testing.T) {
		user := newTestUser(t, db)
		other := newTestUser(t, db)
		mine := newTestFeed(t, db)
		theirs := newTestFeed(t, db)
		unasked := newTestFeed(t, db)
		for _, s := range []struct{ user, feed string }{
			{user, mine.ID}, {user, unasked.ID}, {other, theirs.ID},
		} {
			if err := repo.Subscribe(ctx, s.user, s.feed); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}

		got, err := repo.SubscribedFeedIDs(ctx, user, []string{mine.ID, theirs.ID})
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if !got[mine.ID] || len(got) != 1 {
			t.Errorf("SubscribedFeedIDs = %v, want only %s", got, mine.ID)
		}
	})

	t.Run("SubscribedFeedIDs with no feed IDs returns an empty map", func(t *testing.T) {
		got, err := repo.SubscribedFeedIDs(ctx, newTestUser(t, db), nil)
		if err != nil {
			t.Fatalf("SubscribedFeedIDs: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("SubscribedFeedIDs = %v, want an empty non-nil map", got)
		}
	})

	t.Run("subscribing to a nonexistent feed returns ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)

		err := repo.Subscribe(ctx, user, "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionRepositoryListByUser(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	// newSite inserts a site with the given title; empty means no title.
	newSite := func(t *testing.T, tag, title string) string {
		t.Helper()
		host := fmt.Sprintf("%s-%d.example.com", tag, time.Now().UnixNano())
		id, err := NewSiteRepository(db).Upsert(ctx, rss.Site{Host: host, URL: "https://" + host + "/", Title: title})
		if err != nil {
			t.Fatalf("Upsert site: %v", err)
		}
		t.Cleanup(func() { db.Exec(`DELETE FROM sites WHERE id = $1`, id) })
		return id
	}
	newFeed := func(t *testing.T, siteID, title string) string {
		t.Helper()
		id, err := NewFeedRepository(db).Upsert(ctx, rss.Feed{
			SiteID: siteID, Title: title, NextFetch: time.Now().Add(time.Hour),
			Url: fmt.Sprintf("https://example.com/%s-%d.xml", title, time.Now().UnixNano()),
		})
		if err != nil {
			t.Fatalf("Upsert feed: %v", err)
		}
		return id
	}
	subscribe := func(t *testing.T, user string, feeds ...string) {
		t.Helper()
		for _, f := range feeds {
			if err := repo.Subscribe(ctx, user, f); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}

	t.Run("returns only this user's subscriptions", func(t *testing.T) {
		user, other := newTestUser(t, db), newTestUser(t, db)
		site := newSite(t, "own", "Own")
		mine, theirs := newFeed(t, site, "mine"), newFeed(t, site, "theirs")
		subscribe(t, user, mine)
		subscribe(t, other, theirs)

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 1 || got[0].Feed.ID != mine || got[0].Site.ID != site || got[0].Site.Title != "Own" {
			t.Errorf("got %+v, want only feed %s on site %s", got, mine, site)
		}
	})

	t.Run("ordered by site title or host, then feed title", func(t *testing.T) {
		user := newTestUser(t, db)
		// Hosts start with the tag, so an untitled site sorts by its host.
		bravo := newSite(t, "bravo", "")
		alpha := newSite(t, "alpha", "Zulu title")
		mike := newSite(t, "mike", "mike title")
		f1, f2 := newFeed(t, bravo, "b-second"), newFeed(t, bravo, "a-first")
		f3, f4 := newFeed(t, alpha, "x"), newFeed(t, mike, "y")
		subscribe(t, user, f1, f2, f3, f4)

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		var ids []string
		for _, s := range got {
			ids = append(ids, s.Feed.ID)
		}
		// bravo-host (a-first, b-second), then "mike title", then "Zulu title".
		want := []string{f2, f1, f4, f3}
		if fmt.Sprint(ids) != fmt.Sprint(want) {
			t.Errorf("order = %v, want %v", ids, want)
		}
	})

	t.Run("returns Title and orders a site's feeds by their names", func(t *testing.T) {
		user := newTestUser(t, db)
		site := newSite(t, "renamed", "Renamed")
		first, second := newFeed(t, site, "m-first"), newFeed(t, site, "n-second")
		subscribe(t, user, first, second)
		if err := repo.Rename(ctx, user, second, "AAA"); err != nil {
			t.Fatalf("Rename: %v", err)
		}

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d subscriptions, want 2", len(got))
		}
		if got[0].Feed.ID != second || got[0].Title != "AAA" {
			t.Errorf("first = %s %q, want %s %q", got[0].Feed.ID, got[0].Title, second, "AAA")
		}
		if got[1].Feed.ID != first || got[1].Title != "" {
			t.Errorf("second = %s %q, want %s with no name", got[1].Feed.ID, got[1].Title, first)
		}
	})

	t.Run("LatestArticleAt", func(t *testing.T) {
		user := newTestUser(t, db)
		site := newSite(t, "latest", "Latest")
		withPublished, withoutDate, empty := newFeed(t, site, "a-pub"), newFeed(t, site, "b-nodate"), newFeed(t, site, "c-empty")
		subscribe(t, user, withPublished, withoutDate, empty)
		insert := func(feed, ext, published string) {
			t.Helper()
			var pub any
			if published != "" {
				pub = published
			}
			if _, err := db.Exec(`INSERT INTO articles (feed_id, external_id, published_at, timeline_at) VALUES ($1, $2, $3, COALESCE($3::timestamptz, now()))`, feed, ext, pub); err != nil {
				t.Fatalf("insert article: %v", err)
			}
		}
		insert(withPublished, "old", "2020-01-01T00:00:00Z")
		insert(withPublished, "new", "2021-06-01T00:00:00Z")
		insert(withoutDate, "nodate", "")
		var createdAt time.Time
		if err := db.QueryRow(`SELECT created_at FROM articles WHERE feed_id = $1`, withoutDate).Scan(&createdAt); err != nil {
			t.Fatal(err)
		}

		got, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d subscriptions, want 3", len(got))
		}
		if want := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC); !got[0].LatestArticleAt.Equal(want) {
			t.Errorf("published: got %v, want %v", got[0].LatestArticleAt, want)
		}
		if !got[1].LatestArticleAt.Equal(createdAt) {
			t.Errorf("no published_at: got %v, want created_at %v", got[1].LatestArticleAt, createdAt)
		}
		if !got[2].LatestArticleAt.IsZero() {
			t.Errorf("no articles: got %v, want zero", got[2].LatestArticleAt)
		}
	})

	t.Run("no subscriptions is an empty result", func(t *testing.T) {
		got, err := repo.ListByUser(ctx, newTestUser(t, db))
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionGet(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	t.Run("returns the feed, site and name", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)
		if err := repo.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := repo.Rename(ctx, user, feed.ID, "My name"); err != nil {
			t.Fatalf("Rename: %v", err)
		}

		got, err := repo.Get(ctx, user, feed.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Feed.ID != feed.ID || got.Feed.Title != feed.Title || got.Feed.Url != feed.Url {
			t.Errorf("Feed = %+v, want %+v", got.Feed, feed)
		}
		if got.Site.ID != feed.SiteID || got.Site.Host == "" {
			t.Errorf("Site = %+v, want site %s", got.Site, feed.SiteID)
		}
		if got.Title != "My name" {
			t.Errorf("Title = %q, want %q", got.Title, "My name")
		}
		if !got.LatestArticleAt.IsZero() {
			t.Errorf("LatestArticleAt = %v, want zero", got.LatestArticleAt)
		}
	})

	t.Run("not subscribed returns ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		_, err := repo.Get(ctx, user, feed.ID)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionRename(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	ctx := t.Context()

	title := func(t *testing.T, user, feed string) string {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT title FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, user, feed).Scan(&got); err != nil {
			t.Fatalf("select title: %v", err)
		}
		return got
	}

	t.Run("sets the name and empty resets it, leaving other users' alone", func(t *testing.T) {
		user, other := newTestUser(t, db), newTestUser(t, db)
		feed := newTestFeed(t, db)
		for _, u := range []string{user, other} {
			if err := repo.Subscribe(ctx, u, feed.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}

		if err := repo.Rename(ctx, user, feed.ID, "Mine"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if got := title(t, user, feed.ID); got != "Mine" {
			t.Errorf("title = %q, want %q", got, "Mine")
		}
		if got := title(t, other, feed.ID); got != "" {
			t.Errorf("other user's title = %q, want empty", got)
		}

		if err := repo.Rename(ctx, user, feed.ID, ""); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if got := title(t, user, feed.ID); got != "" {
			t.Errorf("title after reset = %q, want empty", got)
		}
	})

	t.Run("not subscribed returns ErrNoRecord", func(t *testing.T) {
		user := newTestUser(t, db)
		feed := newTestFeed(t, db)

		err := repo.Rename(ctx, user, feed.ID, "Mine")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}

// tagNames returns the names of tags, in order.
func tagNames(tags []rss.Tag) []string {
	var names []string
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscriptionTags(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSubscriptionRepository(db)
	tags := NewTagRepository(db)
	ctx := t.Context()

	// tagged subscribes a new user to two feeds, tagging the first with
	// "news", "AI" and "Cooking" and leaving the second untagged.
	tagged := func(t *testing.T) (user string, feeds []string) {
		t.Helper()
		user = newTestUser(t, db)
		feeds = newTestSubscriptions(t, db, user, 2)
		if err := tags.SetForSubscription(ctx, user, feeds[0], nil, []string{"news", "AI", "Cooking"}); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}
		return user, feeds
	}
	want := []string{"AI", "Cooking", "news"}

	t.Run("ListByUser returns each subscription's tags in name order", func(t *testing.T) {
		user, feeds := tagged(t)
		// Another user's tags on the same feed don't show.
		other := newTestUser(t, db)
		if err := repo.Subscribe(ctx, other, feeds[0]); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := tags.SetForSubscription(ctx, other, feeds[0], nil, []string{"Theirs"}); err != nil {
			t.Fatalf("SetForSubscription: %v", err)
		}

		subs, err := repo.ListByUser(ctx, user)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		got := map[string][]rss.Tag{}
		for _, sub := range subs {
			got[sub.Feed.ID] = sub.Tags
		}
		if names := tagNames(got[feeds[0]]); !slices.Equal(names, want) {
			t.Errorf("tags = %v, want %v", names, want)
		}
		if len(got[feeds[1]]) != 0 {
			t.Errorf("untagged subscription's tags = %v, want none", got[feeds[1]])
		}
		for _, tag := range got[feeds[0]] {
			if tag.ID == "" {
				t.Errorf("tag %q has no ID", tag.Name)
			}
		}
	})

	t.Run("Get returns the subscription's tags in name order", func(t *testing.T) {
		user, feeds := tagged(t)

		sub, err := repo.Get(ctx, user, feeds[0])
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if names := tagNames(sub.Tags); !slices.Equal(names, want) {
			t.Errorf("tags = %v, want %v", names, want)
		}

		sub, err = repo.Get(ctx, user, feeds[1])
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(sub.Tags) != 0 {
			t.Errorf("untagged subscription's tags = %v, want none", sub.Tags)
		}
	})

	t.Run("unsubscribing drops the subscription's tags and keeps the tags", func(t *testing.T) {
		user, feeds := tagged(t)

		if err := repo.Unsubscribe(ctx, user, feeds[0]); err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
		var n int
		err := db.QueryRow(`SELECT count(*) FROM subscription_tags WHERE user_id = $1 AND feed_id = $2`, user, feeds[0]).Scan(&n)
		if err != nil {
			t.Fatalf("count subscription_tags: %v", err)
		}
		if n != 0 {
			t.Errorf("subscription_tags rows = %d, want 0", n)
		}
		list, err := tags.List(ctx, user)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if names := tagNames(list); !slices.Equal(names, want) {
			t.Errorf("tags = %v, want %v", names, want)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSubscribeWritesReaderPositions(t *testing.T) {
	db := psqltest.NewDB(t)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	feed := newTestFeed(t, db)
	hourID, hourAt := insertPositionedArticle(t, db, feed.ID, "hour", time.Hour)
	threeID, threeAt := insertPositionedArticle(t, db, feed.ID, "three-hours", 3*time.Hour)
	daysID, _ := insertPositionedArticle(t, db, feed.ID, "two-days", 48*time.Hour)

	subscribe := func(t *testing.T, user string) {
		t.Helper()
		if err := subs.Subscribe(ctx, user, feed.ID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	recentReader := newTestUser(t, db)
	recentSeen := setTimelineMark(t, db, recentReader, 30*time.Minute, time.Minute)
	subscribe(t, recentReader)

	t.Run("posts from the last NewsWindow go above what the reader saw", func(t *testing.T) {
		got := readerPositions(t, db, recentReader)
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2: %v", len(got), got)
		}
		hour, three := got[hourID], got[threeID]
		if !hour.After(recentSeen) || !three.After(recentSeen) {
			t.Errorf("got %v and %v, want both after seen_up_to %v", hour, three, recentSeen)
		}
		if !hour.After(three) {
			t.Errorf("hour-ago at %v, want after three-hours-ago at %v", hour, three)
		}
		if _, ok := got[daysID]; ok {
			t.Error("a row for the two-days-ago article")
		}
	})

	t.Run("after seenUpTo: the shared position", func(t *testing.T) {
		user := newTestUser(t, db)
		seen := setTimelineMark(t, db, user, 2*time.Hour, time.Minute)
		subscribe(t, user)
		got := readerPositions(t, db, user)
		if !got[hourID].Equal(hourAt) {
			t.Errorf("hour-ago: got %v, want timeline_at %v", got[hourID], hourAt)
		}
		if three, ok := got[threeID]; !ok || !three.After(seen) || !three.Before(hourAt) || !three.After(threeAt) {
			t.Errorf("three-hours-ago: got %v (row %v), want between seen_up_to %v and %v", three, ok, seen, hourAt)
		}
	})

	t.Run("no mark, no rows", func(t *testing.T) {
		user := newTestUser(t, db)
		subscribe(t, user)
		if got := readerPositions(t, db, user); len(got) != 0 {
			t.Errorf("got %d rows, want none", len(got))
		}
	})

	t.Run("subscribing again changes nothing", func(t *testing.T) {
		before := readerPositions(t, db, recentReader)
		insertPositionedArticle(t, db, feed.ID, "since", 10*time.Minute)
		subscribe(t, recentReader)
		if got := readerPositions(t, db, recentReader); !maps.EqualFunc(got, before, time.Time.Equal) {
			t.Errorf("got %v, want %v", got, before)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestUnsubscribeKeepsReaderPositions(t *testing.T) {
	db := psqltest.NewDB(t)
	subs := NewSubscriptionRepository(db)
	ctx := t.Context()

	feed := newTestFeed(t, db)
	article, at := insertPositionedArticle(t, db, feed.ID, "recent", time.Hour)
	user := newTestUser(t, db)
	setTimelineMark(t, db, user, 10*time.Minute, time.Minute)
	if err := subs.Subscribe(ctx, user, feed.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	raised, ok := readerPositions(t, db, user)[article]
	if !ok || !raised.After(at) {
		t.Fatalf("got row %v (ok %v) after subscribing, want one raised above %v", raised, ok, at)
	}

	if err := subs.Unsubscribe(ctx, user, feed.ID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if got, ok := readerPositions(t, db, user)[article]; !ok || !got.Equal(raised) {
		t.Errorf("after Unsubscribe got row %v (ok %v), want it kept at %v", got, ok, raised)
	}

	// The reader looks again, above the raised row, then follows the feed
	// again: the post stays where it was rather than being raised above
	// what they've now seen.
	setTimelineMark(t, db, user, 0, 0)
	if err := subs.Subscribe(ctx, user, feed.ID); err != nil {
		t.Fatalf("Subscribe again: %v", err)
	}
	if got := readerPositions(t, db, user)[article]; !got.Equal(raised) {
		t.Errorf("after subscribing again got %v, want %v", got, raised)
	}
}

func TestSubscribeKeepsCopiesInPlace(t *testing.T) {
	db := psqltest.NewDB(t)
	subs := NewSubscriptionRepository(db)
	articles := NewArticleRepository(db)
	ctx := t.Context()

	// insertCopy inserts an article of feedID with canonical URL url,
	// flagged as having copies, positioned that long ago.
	insertCopy := func(t *testing.T, feedID, title, url string, ago time.Duration) (string, time.Time) {
		t.Helper()
		var id string
		var at time.Time
		err := db.QueryRow(`INSERT INTO articles (feed_id, external_id, title, url, canonical_url, has_copies, timeline_at, created_at)
			VALUES ($1, $2, $2, $3, $3, true, now() - $4 * interval '1 microsecond', now() - $4 * interval '1 microsecond')
			RETURNING id, timeline_at`, feedID, title, url, ago.Microseconds()).Scan(&id, &at)
		if err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id, at
	}
	subscribe := func(t *testing.T, userID string, feeds ...rss.Feed) {
		t.Helper()
		for _, f := range feeds {
			if err := subs.Subscribe(ctx, userID, f.ID); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
		}
	}
	url := func(t *testing.T) string { return "https://blog.example/" + t.Name() }
	// ownPosition gives userID their own position for articleID.
	ownPosition := func(t *testing.T, userID, articleID string, at time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at)
			SELECT $1, id, feed_id, $3 FROM articles WHERE id = $2`, userID, articleID, at.Truncate(time.Microsecond)); err != nil {
			t.Fatalf("insert row: %v", err)
		}
	}
	// olderOf inserts another article of feed, 10 minutes old, and returns its ID.
	olderOf := func(t *testing.T, feed rss.Feed) string {
		t.Helper()
		id, _ := insertPositionedArticle(t, db, feed.ID, "older", 10*time.Minute)
		return id
	}

	t.Run("an older copy takes the entry's place instead of moving it", func(t *testing.T) {
		agg, blog, other := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		_, aggAt := insertCopy(t, agg.ID, "via the aggregator", url(t), 5*time.Minute)
		blogID, _ := insertCopy(t, blog.ID, "on the blog", url(t), 48*time.Hour)
		insertPositionedArticle(t, db, other.ID, "newer", time.Minute)
		olderID, olderAt := insertPositionedArticle(t, db, other.ID, "older", 10*time.Minute)
		subscribe(t, reader, agg, other)
		ownPosition(t, reader, olderID, olderAt) // an active reader has rows for what arrived lately

		subscribe(t, reader, blog)

		items, err := articles.ListTimeline(ctx, reader, rss.TimelineFilter{}, rss.ArticleCursor{}, 10)
		if err != nil {
			t.Fatalf("ListTimeline: %v", err)
		}
		var got []string
		for _, item := range items {
			got = append(got, item.Article.Title)
		}
		if want := []string{"newer", "on the blog", "older"}; !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		entry := items[1]
		if want := aggAt.Add(-time.Microsecond); !entry.Position.Equal(want) {
			t.Errorf("the blog's copy is at %v, want just below the aggregator's, %v", entry.Position, want)
		}
		if len(entry.AlsoIn) != 1 || entry.AlsoIn[0].ID != agg.ID {
			t.Errorf("AlsoIn = %v, want the aggregator", entry.AlsoIn)
		}
		if _, ok := readerPositions(t, db, reader)[blogID]; !ok {
			t.Error("the blog's copy has no row")
		}
	})

	t.Run("below the reader's own position for the entry", func(t *testing.T) {
		agg, blog := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		aggID, _ := insertCopy(t, agg.ID, "via the aggregator", url(t), time.Hour)
		blogID, _ := insertCopy(t, blog.ID, "on the blog", url(t), 48*time.Hour)
		subscribe(t, reader, agg)
		raised := time.Now().Add(-3 * time.Minute).Truncate(time.Microsecond)
		if _, err := db.Exec(`INSERT INTO timeline_entries (user_id, article_id, feed_id, timeline_at) VALUES ($1, $2, $3, $4)`, reader, aggID, agg.ID, raised); err != nil {
			t.Fatalf("insert row: %v", err)
		}

		subscribe(t, reader, blog)

		if got, want := readerPositions(t, db, reader)[blogID], raised.Add(-time.Microsecond); !got.Equal(want) {
			t.Errorf("the blog's copy is at %v, want %v", got, want)
		}
	})

	t.Run("a recent copy isn't raised above what the reader saw", func(t *testing.T) {
		agg, blog := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		_, aggAt := insertCopy(t, agg.ID, "via the aggregator", url(t), 5*time.Minute)
		blogID, _ := insertCopy(t, blog.ID, "on the blog", url(t), time.Hour)
		subscribe(t, reader, agg)
		ownPosition(t, reader, olderOf(t, agg), time.Now().Add(-10*time.Minute))
		setTimelineMark(t, db, reader, time.Minute, time.Minute)

		subscribe(t, reader, blog)

		if got, want := readerPositions(t, db, reader)[blogID], aggAt.Add(-time.Microsecond); !got.Equal(want) {
			t.Errorf("the blog's copy is at %v, want %v", got, want)
		}
	})

	t.Run("an entry below the reader's own positions moves as before", func(t *testing.T) {
		agg, blog := newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		insertCopy(t, agg.ID, "via the aggregator", url(t), 72*time.Hour)
		blogID, _ := insertCopy(t, blog.ID, "on the blog", url(t), 120*time.Hour)
		subscribe(t, reader, agg)
		ownPosition(t, reader, olderOf(t, agg), time.Now().Add(-10*time.Minute))

		subscribe(t, reader, blog)

		if _, ok := readerPositions(t, db, reader)[blogID]; ok {
			t.Error("the blog's copy got a row below the reader's own positions")
		}
	})

	t.Run("a later copy, or a copy of an article not in the timeline, gets no row", func(t *testing.T) {
		agg, blog, unfollowed := newTestFeed(t, db), newTestFeed(t, db), newTestFeed(t, db)
		reader := newTestUser(t, db)
		insertCopy(t, agg.ID, "via the aggregator", url(t), 48*time.Hour)
		insertCopy(t, blog.ID, "on the blog", url(t), 24*time.Hour)
		insertCopy(t, unfollowed.ID, "elsewhere", url(t)+"/other", time.Minute)
		insertCopy(t, blog.ID, "other post", url(t)+"/other", 24*time.Hour)
		subscribe(t, reader, agg)
		ownPosition(t, reader, olderOf(t, agg), time.Now().Add(-72*time.Hour))

		subscribe(t, reader, blog)

		if got := readerPositions(t, db, reader); len(got) != 1 {
			t.Errorf("got rows %v, want only the reader's own one", got)
		}
	})
}
