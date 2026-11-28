package psql

import (
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/migrations"
	"github.com/pressly/goose/v3"
)

// Requires a test database; runs in its own schema (newMigrateDB).
func TestMigrateFeedURLAliases(t *testing.T) {
	t.Parallel()
	db := newMigrateDB(t)
	ctx := t.Context()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 32); err != nil {
		t.Fatalf("migrate to 32: %v", err)
	}

	siteID, err := upsertSite(ctx, db, rss.Site{Host: "reddit.com", URL: "https://www.reddit.com/", Title: "Reddit"}, true)
	if err != nil {
		t.Fatal(err)
	}
	addFeed := func(url string) string {
		t.Helper()
		id, err := upsertFeed(ctx, db, rss.Feed{SiteID: siteID, Url: url, Title: url})
		if err != nil {
			t.Fatalf("Upsert feed %s: %v", url, err)
		}
		return id
	}
	golang := addFeed("https://old.reddit.com/r/golang/.rss")
	channel := addFeed("https://m.youtube.com/feeds/videos.xml?channel_id=UCabc")
	plainHTTP := addFeed("http://np.reddit.com/r/rust/.rss")
	// A feed whose rewritten URL is already stored stays where it is.
	twin := addFeed("https://i.reddit.com/r/python/.rss")
	addFeed("https://www.reddit.com/r/python/.rss")
	other := addFeed("https://music.youtube.com/feed")

	if _, err := provider.UpTo(ctx, 33); err != nil {
		t.Fatalf("migrate to 33: %v", err)
	}

	for id, want := range map[string]string{
		golang:    "https://www.reddit.com/r/golang/.rss",
		channel:   "https://www.youtube.com/feeds/videos.xml?channel_id=UCabc",
		plainHTTP: "http://www.reddit.com/r/rust/.rss",
		twin:      "https://i.reddit.com/r/python/.rss",
		other:     "https://music.youtube.com/feed",
	} {
		var got string
		if err := db.QueryRow(`SELECT url FROM feeds WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("feed url = %q, want %q", got, want)
		}
	}
}
