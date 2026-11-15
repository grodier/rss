package psql

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newTestSection inserts a Discover section after every other one and
// deletes it on cleanup, after the feeds under it (registered later).
func newTestSection(t *testing.T, db *sql.DB, name string, position int) rss.Section {
	t.Helper()
	sec := rss.Section{Slug: fmt.Sprintf("test-%d", time.Now().UnixNano()), Name: fmt.Sprintf("%s %d", name, time.Now().UnixNano())}
	if err := db.QueryRow(`INSERT INTO discover_sections (slug, name, position) VALUES ($1, $2, $3) RETURNING id`,
		sec.Slug, sec.Name, 1_000_000+position).Scan(&sec.ID); err != nil {
		t.Fatalf("insert section: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM discover_sections WHERE id = $1`, sec.ID); err != nil {
			t.Errorf("cleanup section: %v", err)
		}
	})
	return sec
}

// newCatalogFeed is newTestFeed in section sec, with an article published
// at each of dates.
func newCatalogFeed(t *testing.T, db *sql.DB, sec rss.Section, dates ...time.Time) rss.Feed {
	t.Helper()
	f := newTestFeed(t, db)
	putInSection(t, db, f.ID, sec)
	saveDated(t, db, f, dates...)
	return f
}

func putInSection(t *testing.T, db *sql.DB, feedID string, sec rss.Section) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO discover_feeds (feed_id, section_id) VALUES ($1, $2)`, feedID, sec.ID); err != nil {
		t.Fatalf("insert discover_feeds: %v", err)
	}
}

func saveDated(t *testing.T, db *sql.DB, f rss.Feed, dates ...time.Time) {
	t.Helper()
	if len(dates) == 0 {
		return
	}
	var articles []rss.Article
	for i, d := range dates {
		articles = append(articles, rss.Article{ExternalID: fmt.Sprintf("a%d", i), Title: fmt.Sprintf("Post %d", i), PublishedAt: d})
	}
	if _, err := NewFeedRepository(db).SaveFetch(t.Context(), f, articles, f.NextFetch); err != nil {
		t.Fatalf("SaveFetch: %v", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestDiscoverRepositorySections(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoverRepository(db)
	subs := NewSubscriptionRepository(db)
	userID := newTestUser(t, db)
	day := func(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour).Truncate(time.Second) }

	first := newTestSection(t, db, "First", 1)
	second := newTestSection(t, db, "Second", 2)
	empty := newTestSection(t, db, "Empty", 3)
	allFollowed := newTestSection(t, db, "All followed", 4)

	// first: four feeds, newest-posting d, then b, a, and c with none.
	a := newCatalogFeed(t, db, first, day(5), day(9))
	b := newCatalogFeed(t, db, first, day(2))
	c := newCatalogFeed(t, db, first)
	d := newCatalogFeed(t, db, first, day(1), day(3), day(4), day(6), day(7))
	followed := newCatalogFeed(t, db, first, day(0))
	gone := newCatalogFeed(t, db, first, day(0))
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, gone.ID); err != nil {
		t.Fatal(err)
	}
	// second: a feed of followed's site, which still shows.
	sibling, err := NewFeedRepository(db).Upsert(t.Context(), rss.Feed{SiteID: followed.SiteID, Url: fmt.Sprintf("https://example.com/sibling-%d.xml", time.Now().UnixNano()), Title: "Sibling"})
	if err != nil {
		t.Fatal(err)
	}
	putInSection(t, db, sibling, second)
	// allFollowed: one feed, followed.
	alsoFollowed := newCatalogFeed(t, db, allFollowed, day(1))
	for _, id := range []string{followed.ID, alsoFollowed.ID} {
		if err := subs.Subscribe(t.Context(), userID, id); err != nil {
			t.Fatal(err)
		}
	}
	_ = empty

	got, err := repo.Sections(t.Context(), userID, 3)
	if err != nil {
		t.Fatalf("Sections: %v", err)
	}
	// Other tests' and the seeded sections come first; keep this test's.
	var mine []rss.DiscoverSection
	for _, s := range got {
		if slices.Contains([]string{first.Slug, second.Slug, empty.Slug, allFollowed.Slug}, s.Slug) {
			mine = append(mine, s)
		}
	}
	if len(mine) != 2 || mine[0].Slug != first.Slug || mine[1].Slug != second.Slug {
		t.Fatalf("sections = %+v; want first and second only, in order", mine)
	}

	ids := func(s rss.DiscoverSection) []string {
		var out []string
		for _, f := range s.Feeds {
			out = append(out, f.Feed.ID)
		}
		return out
	}
	if want := []string{d.ID, b.ID, a.ID}; !slices.Equal(ids(mine[0]), want) {
		t.Errorf("first's feeds = %v; want %v (c, followed and gone left out or cut)", ids(mine[0]), want)
	}
	if mine[0].Total != 4 || mine[0].Name != first.Name {
		t.Errorf("first = %q, Total %d; want %q, 4 (a, b, c, d)", mine[0].Name, mine[0].Total, first.Name)
	}
	if want := []string{sibling}; !slices.Equal(ids(mine[1]), want) || mine[1].Total != 1 {
		t.Errorf("second's feeds = %v, Total %d; want %v, 1", ids(mine[1]), mine[1].Total, want)
	}

	// d's three newest articles, newest first, and its site.
	fd := mine[0].Feeds[0]
	if len(fd.Latest) != DiscoverLatest {
		t.Fatalf("d's Latest has %d articles; want %d", len(fd.Latest), DiscoverLatest)
	}
	for i, want := range []time.Time{day(1), day(3), day(4)} {
		if a := fd.Latest[i]; !a.PublishedAt.Equal(want) || a.ID == "" || a.Title == "" {
			t.Errorf("Latest[%d] = %+v; want published %v, with an ID and title", i, a, want)
		}
	}
	if fd.Site.ID != d.SiteID || fd.Site.Host == "" || !fd.LatestAt().Equal(day(1)) {
		t.Errorf("d's site %+v, LatestAt %v", fd.Site, fd.LatestAt())
	}
	if got := mine[0].Feeds[2]; len(got.Latest) != 2 {
		t.Errorf("a's Latest has %d articles; want 2", len(got.Latest))
	}

	t.Run("perSection", func(t *testing.T) {
		got, err := repo.Sections(t.Context(), userID, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if s.Slug == first.Slug && (len(s.Feeds) != 1 || s.Feeds[0].Feed.ID != d.ID || s.Total != 4) {
				t.Errorf("first with 1 per section = %v, Total %d; want [d], 4", ids(s), s.Total)
			}
		}
	})

	t.Run("a feed with no articles", func(t *testing.T) {
		got, err := repo.Sections(t.Context(), userID, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if s.Slug != first.Slug {
				continue
			}
			if last := s.Feeds[len(s.Feeds)-1]; last.Feed.ID != c.ID || len(last.Latest) != 0 || !last.LatestAt().IsZero() {
				t.Errorf("last feed = %+v; want c, with no articles, last", last)
			}
		}
	})
}
