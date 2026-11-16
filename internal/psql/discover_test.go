package psql

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/lib/pq"
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
	// One clock reading for the whole test: day(n) is written to the database
	// and compared with what comes back, so it must not move between calls.
	now := time.Now().Truncate(time.Second)
	day := func(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

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
	if mine[0].Total != 5 || mine[0].Name != first.Name {
		t.Errorf("first = %q, Total %d; want %q, 5 (a, b, c, d and followed; not gone)", mine[0].Name, mine[0].Total, first.Name)
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
			if s.Slug == first.Slug && (len(s.Feeds) != 1 || s.Feeds[0].Feed.ID != d.ID || s.Total != 5) {
				t.Errorf("first with 1 per section = %v, Total %d; want [d], 5", ids(s), s.Total)
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

// Requires a migrated database; see psqltest.NewDB.
func TestDiscoverRepositorySectionsOrder(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoverRepository(db)
	subs := NewSubscriptionRepository(db)

	// Four sections in this default order, each with three catalog feeds,
	// so following one or two never empties a section.
	secs := make([]rss.Section, 4)
	feeds := map[string][]rss.Feed{}
	for i := range secs {
		secs[i] = newTestSection(t, db, fmt.Sprintf("Order %d", i), 10+i)
		for range 3 {
			feeds[secs[i].ID] = append(feeds[secs[i].ID], newCatalogFeed(t, db, secs[i]))
		}
	}
	a, b, c, d := secs[0], secs[1], secs[2], secs[3]

	tests := []struct {
		name   string
		follow map[rss.Section]int // how many of the section's feeds the reader follows
		chosen []rss.Section       // users.sections
		want   []rss.Section
	}{
		{"no follows and no choices: the default order", nil, nil, []rss.Section{a, b, c, d}},
		{"chosen sections first, in their order", nil, []rss.Section{d, b}, []rss.Section{d, b, a, c}},
		{"followed counts beat choices", map[rss.Section]int{c: 2, a: 1}, []rss.Section{d, b}, []rss.Section{c, a, d, b}},
		{"ties keep the chosen order, then the default", map[rss.Section]int{a: 1, d: 1}, []rss.Section{d, c}, []rss.Section{d, a, c, b}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := newTestUser(t, db)
			for sec, n := range tt.follow {
				for _, f := range feeds[sec.ID][:n] {
					if err := subs.Subscribe(t.Context(), userID, f.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			var chosen []string
			for _, sec := range tt.chosen {
				chosen = append(chosen, sec.ID)
			}
			if _, err := db.Exec(`UPDATE users SET sections = $2::uuid[] WHERE id = $1`, userID, pq.Array(append([]string{}, chosen...))); err != nil {
				t.Fatal(err)
			}

			got, err := repo.Sections(t.Context(), userID, 3)
			if err != nil {
				t.Fatal(err)
			}
			// Other tests' and the seeded sections may come between; keep
			// this test's.
			var order, want []string
			for _, s := range got {
				for i, sec := range secs {
					if s.Slug == sec.Slug {
						order = append(order, s.Name)
						if s.Position != 1_000_000+10+i {
							t.Errorf("%s's Position = %d, want %d", s.Name, s.Position, 1_000_000+10+i)
						}
					}
				}
			}
			for _, sec := range tt.want {
				want = append(want, sec.Name)
			}
			if !slices.Equal(order, want) {
				t.Errorf("order = %q, want %q", order, want)
			}
		})
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestDiscoverRepositorySection(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoverRepository(db)
	sec := newTestSection(t, db, "Section", 1)

	got, err := repo.Section(t.Context(), sec.Slug)
	if err != nil || got != sec {
		t.Errorf("Section(%q) = %+v, %v; want %+v", sec.Slug, got, err, sec)
	}
	if _, err := repo.Section(t.Context(), "no-such-section"); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("unknown slug: err = %v, want ErrNoRecord", err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestDiscoverRepositorySectionFeeds(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoverRepository(db)
	userID := newTestUser(t, db)
	// One clock reading for the whole test: day(n) is written to the database
	// and compared with what comes back, so it must not move between calls.
	now := time.Now().Truncate(time.Second)
	day := func(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

	sec := newTestSection(t, db, "Section", 1)
	other := newTestSection(t, db, "Other", 2)
	a := newCatalogFeed(t, db, sec, day(5))
	b := newCatalogFeed(t, db, sec, day(2), day(3), day(4), day(6))
	none := newCatalogFeed(t, db, sec)
	followed := newCatalogFeed(t, db, sec, day(1))
	gone := newCatalogFeed(t, db, sec, day(0))
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, gone.ID); err != nil {
		t.Fatal(err)
	}
	newCatalogFeed(t, db, other, day(0))
	newTestFeed(t, db) // not in the catalog
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), userID, followed.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.SectionFeeds(t.Context(), userID, sec.ID)
	if err != nil {
		t.Fatalf("SectionFeeds: %v", err)
	}
	var ids []string
	for _, f := range got {
		ids = append(ids, f.Feed.ID)
	}
	if want := []string{followed.ID, b.ID, a.ID, none.ID}; !slices.Equal(ids, want) {
		t.Fatalf("feeds = %v; want %v (newest-posting first, the one without articles last, gone and other sections' left out)", ids, want)
	}
	for _, f := range got {
		if want := f.Feed.ID == followed.ID; f.Following != want {
			t.Errorf("%s: Following = %v, want %v", f.Feed.ID, f.Following, want)
		}
		if f.Site.ID != f.Feed.SiteID || f.Site.Host == "" {
			t.Errorf("%s: site = %+v", f.Feed.ID, f.Site)
		}
	}
	if fb := got[1]; len(fb.Latest) != DiscoverLatest || !fb.LatestAt().Equal(day(2)) {
		t.Errorf("b's Latest = %d articles, LatestAt %v; want %d, %v", len(fb.Latest), fb.LatestAt(), DiscoverLatest, day(2))
	}
	if len(got[3].Latest) != 0 {
		t.Errorf("none's Latest = %+v, want empty", got[3].Latest)
	}

	t.Run("a section with no feeds", func(t *testing.T) {
		empty := newTestSection(t, db, "Empty", 3)
		got, err := repo.SectionFeeds(t.Context(), userID, empty.ID)
		if err != nil || len(got) != 0 {
			t.Errorf("SectionFeeds = %+v, %v; want none", got, err)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestDiscoverRepositorySectionPosts(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewDiscoverRepository(db)
	userID := newTestUser(t, db)
	now := time.Now().Truncate(time.Second)
	hour := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }
	unix := func(n int) int64 { return hour(n).Unix() }

	sec := newTestSection(t, db, "Section", 1)
	other := newTestSection(t, db, "Other", 2)
	a := newCatalogFeed(t, db, sec, hour(1), hour(4), hour(6))
	b := newCatalogFeed(t, db, sec, hour(2), hour(3), hour(5))
	gone := newCatalogFeed(t, db, sec, hour(0))
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, gone.ID); err != nil {
		t.Fatal(err)
	}
	newCatalogFeed(t, db, other, hour(0))
	notCatalog := newTestFeed(t, db)
	saveDated(t, db, notCatalog, hour(0))
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), userID, b.ID); err != nil {
		t.Fatal(err)
	}

	type post struct {
		feed      string
		at        int64
		following bool
	}
	read := func(before rss.ArticleCursor, limit int) ([]post, []rss.SectionPost) {
		t.Helper()
		got, err := repo.SectionPosts(t.Context(), userID, sec.ID, before, limit)
		if err != nil {
			t.Fatalf("SectionPosts: %v", err)
		}
		var out []post
		for _, p := range got {
			if p.Feed.Site.ID != p.Feed.Feed.SiteID || p.Feed.Site.Host == "" || p.Article.ID == "" || p.Article.FeedID != p.Feed.Feed.ID {
				t.Errorf("post = %+v; want its article, feed and site", p)
			}
			out = append(out, post{p.Feed.Feed.ID, p.Article.PublishedAt.Unix(), p.Feed.Following})
		}
		return out, got
	}

	first, raw := read(rss.ArticleCursor{}, 4)
	want := []post{{a.ID, unix(1), false}, {b.ID, unix(2), true}, {b.ID, unix(3), true}, {a.ID, unix(4), false}}
	if !slices.Equal(first, want) {
		t.Fatalf("first page = %+v; want %+v (newest first, only this section's catalog feeds, not gone)", first, want)
	}

	last := raw[len(raw)-1].Article
	second, _ := read(rss.ArticleCursor{At: last.FeedSortAt(), ID: last.ID}, 4)
	if want := []post{{b.ID, unix(5), true}, {a.ID, unix(6), false}}; !slices.Equal(second, want) {
		t.Errorf("second page = %+v; want %+v", second, want)
	}

	t.Run("a section with no feeds", func(t *testing.T) {
		empty := newTestSection(t, db, "Empty", 3)
		got, err := repo.SectionPosts(t.Context(), userID, empty.ID, rss.ArticleCursor{}, 10)
		if err != nil || len(got) != 0 {
			t.Errorf("SectionPosts = %+v, %v; want none", got, err)
		}
	})
}
