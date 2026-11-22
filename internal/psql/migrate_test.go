package psql

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/grodier/rss/internal/discovery"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/migrations"
)

// newMigrateDB returns a connection to a new, empty schema of the test
// database (psqltest.NewSchemaDSN), so Migrate can run without touching public.
func newMigrateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", psqltest.NewSchemaDSN(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustMigrate(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := Migrate(context.Background(), db, migrations.FS, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
}

func appliedMigrations(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&n); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	return n
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		)`, table).Scan(&exists)
	if err != nil {
		t.Fatalf("look up table %s: %v", table, err)
	}
	return exists
}

func TestMigrate(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db)

	sources, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if got := appliedMigrations(t, db); got != len(sources) {
		t.Errorf("applied %d migrations, want %d", got, len(sources))
	}

	// A second run has nothing to apply.
	mustMigrate(t, db)
	if got := appliedMigrations(t, db); got != len(sources) {
		t.Errorf("after a second run: applied %d migrations, want %d", got, len(sources))
	}
}

func TestMigrateKeepsData(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db)
	if _, err := db.Exec(`INSERT INTO sites (host, url, title) VALUES ('migrate-test.example', 'https://migrate-test.example/', 'Migrate test')`); err != nil {
		t.Fatalf("insert site: %v", err)
	}

	mustMigrate(t, db)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sites WHERE host = 'migrate-test.example'`).Scan(&n); err != nil {
		t.Fatalf("count sites: %v", err)
	}
	if n != 1 {
		t.Errorf("found %d sites after migrating again, want 1", n)
	}
}

func TestMigrateDropsFingerprintTable(t *testing.T) {
	db := newMigrateDB(t)
	if _, err := db.Exec(`CREATE TABLE schema_fingerprint (fingerprint text NOT NULL)`); err != nil {
		t.Fatalf("create schema_fingerprint: %v", err)
	}

	mustMigrate(t, db)
	if tableExists(t, db, "schema_fingerprint") {
		t.Error("schema_fingerprint exists after Migrate, want it dropped")
	}
}

// 00021 records whether each existing lookup ran on its site's home page,
// which until then was its URL's path "/" with no query.
func TestMigrateLookupsHomePage(t *testing.T) {
	db := newMigrateDB(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 20); err != nil {
		t.Fatalf("migrate to 20: %v", err)
	}
	want := map[string]bool{
		"https://home.example/":          true,
		"http://www.home2.example/":      true,
		"https://feed.example/feed.xml":  false,
		"https://query.example/?ref=x":   false,
		"https://medium.com/@alice/post": false,
	}
	for u := range want {
		if _, err := db.Exec(`INSERT INTO lookups (site_key, url) VALUES ($1, $1)`, u); err != nil {
			t.Fatalf("insert lookup: %v", err)
		}
	}

	mustMigrate(t, db)
	for u, home := range want {
		var got bool
		if err := db.QueryRow(`SELECT home_page FROM lookups WHERE url = $1`, u).Scan(&got); err != nil {
			t.Fatalf("home_page of %s: %v", u, err)
		}
		if got != home {
			t.Errorf("home_page of %s = %v, want %v", u, got, home)
		}
	}
}

// 00024 trims the stored titles of sites on a shared host as
// discovery.OwnName trims a lookup's, and leaves ordinary sites alone.
func TestMigrateSharedHostSiteNames(t *testing.T) {
	db := newMigrateDB(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 23); err != nil {
		t.Fatalf("migrate to 23: %v", err)
	}
	sites := map[string]string{
		"medium.com/@alice":               "Alice – Medium",
		"medium.com/@ann":                 "Stories by Ann on Medium",
		"mastodon.social/@bob":            "Bob (@bob@mastodon.social) - Mastodon",
		"bsky.app/profile/cy.bsky.social": "Cy (@cy.bsky.social) — Bluesky",
		"youtube.com/@chan":               "Channel name - YouTube",
		"github.com/owner/repo":           "owner/repo: a tool - fast - GitHub",
		"github.com/owner/other":          "GitHub - owner/other: a tool",
		"github.com/owner":                "owner (Owner Name) · GitHub",
		"medium.com/@plain":               "Plain",
		"medium.com/@untitled":            "",
		"example.com/@dash":               "- Medium",
		"example.com":                     "Notes - Medium",
	}
	for host, title := range sites {
		if _, err := db.Exec(`INSERT INTO sites (host, url, title) VALUES ($1, $2, $3)`, host, "https://"+host, title); err != nil {
			t.Fatalf("insert site: %v", err)
		}
	}

	mustMigrate(t, db)
	for host, title := range sites {
		var got string
		if err := db.QueryRow(`SELECT title FROM sites WHERE host = $1`, host).Scan(&got); err != nil {
			t.Fatalf("title of %s: %v", host, err)
		}
		if want := discovery.OwnName(host, title); got != want {
			t.Errorf("title of %s = %q, want %q (OwnName of %q)", host, got, want, title)
		}
	}
}
