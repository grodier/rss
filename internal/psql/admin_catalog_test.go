package psql

import (
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB.
func TestAdminFeedRepositoryCatalog(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminFeedRepository(db)

	second := newTestSection(t, db, "Second", 2)
	first := newTestSection(t, db, "First", 1)
	empty := newTestSection(t, db, "Empty", 3)
	posted := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	b := newCatalogFeed(t, db, first, posted)
	if _, err := db.Exec(`UPDATE feeds SET title = 'b feed', posts_per_week = 2.5 WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	a := newCatalogFeed(t, db, first)
	if _, err := db.Exec(`UPDATE feeds SET title = 'A feed' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	user := newTestUser(t, db)
	if err := NewSubscriptionRepository(db).Subscribe(t.Context(), user, b.ID); err != nil {
		t.Fatal(err)
	}
	c := newCatalogFeed(t, db, second)
	if _, err := db.Exec(`UPDATE feeds SET gone_at = now() WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Every section, in their order; the test's are after the others.
	var mine []rss.AdminCatalogSection
	for _, sec := range got {
		if sec.ID == first.ID || sec.ID == second.ID || sec.ID == empty.ID {
			mine = append(mine, sec)
		}
	}
	if len(mine) != 3 || mine[0].ID != first.ID || mine[1].ID != second.ID || mine[2].ID != empty.ID {
		t.Fatalf("test sections = %+v, want First, Second, Empty", mine)
	}
	if mine[0].Name != first.Name || mine[0].Slug != first.Slug {
		t.Errorf("section = %+v, want %+v", mine[0].Section, first)
	}
	if n := len(mine[2].Feeds); n != 0 {
		t.Errorf("Empty has %d feeds, want 0", n)
	}
	// A gone feed is listed, marked, though Discover hides it.
	if len(mine[1].Feeds) != 1 || mine[1].Feeds[0].ID != c.ID || mine[1].Feeds[0].GoneAt.IsZero() {
		t.Errorf("Second's feeds = %+v, want %s, gone", mine[1].Feeds, c.ID)
	}

	// By title, any case.
	fs := mine[0].Feeds
	if len(fs) != 2 || fs[0].ID != a.ID || fs[1].ID != b.ID {
		t.Fatalf("First's feeds = %+v, want A feed, b feed", fs)
	}
	if !fs[0].LatestPost.IsZero() || fs[0].Subscribers != 0 || !fs[0].GoneAt.IsZero() {
		t.Errorf("A feed = %+v, want no post, no subscribers", fs[0])
	}
	f := fs[1]
	if !f.LatestPost.Equal(posted) || f.Subscribers != 1 || f.PostsPerWeek != 2.5 || f.Title != "b feed" || f.URL != b.Url || f.SiteID != b.SiteID || f.SiteHost == "" {
		t.Errorf("b feed = %+v, want posted %v, 1 subscriber, 2.5 a week, its URL and site", f, posted)
	}
}
