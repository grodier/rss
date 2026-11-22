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
// discovery.OwnName trims a lookup's: a title equal to one of the site's
// feed titles as a feed's, any other as a page's. Ordinary sites are left
// alone.
func TestMigrateSharedHostSiteNames(t *testing.T) {
	db := newMigrateDB(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 23); err != nil {
		t.Fatalf("migrate to 23: %v", err)
	}
	type site struct {
		title    string
		fromFeed bool // the site has a feed with this title
	}
	sites := map[string]site{
		"medium.com/@alice":               {"Alice – Medium", false},
		"medium.com/@ann":                 {"Stories by Ann on Medium", true},
		"medium.com/@amy":                 {"  Stories by Amy on Medium\n", false},
		"mastodon.social/@bob":            {"Bob (@bob@mastodon.social) - Mastodon", false},
		"mastodon.social/@jane":           {"Jane | Rust dev", true},
		"bsky.app/profile/cy.bsky.social": {"Cy (@cy.bsky.social) — Bluesky", false},
		"youtube.com/@chan":               {"Channel name - YouTube", false},
		"youtube.com/channel/UCx":         {"Kurzgesagt – In a Nutshell", true},
		"github.com/owner/repo":           {"owner/repo: a tool - fast - GitHub", false},
		"github.com/owner/other":          {"GitHub - owner/other: a tool", false},
		"github.com/owner":                {"owner (Owner Name) · GitHub", false},
		"github.com/acme":                 {"acme (Acme: Tools) · GitHub", false},
		"medium.com/@plain":               {"Plain", false},
		"medium.com/@untitled":            {"", false},
		"example.com/@dash":               {"- Medium", false},
		"example.com":                     {"Notes - Medium", false},
	}
	for host, s := range sites {
		var id string
		if err := db.QueryRow(`INSERT INTO sites (host, url, title) VALUES ($1, $2, $3) RETURNING id`, host, "https://"+host, s.title).Scan(&id); err != nil {
			t.Fatalf("insert site: %v", err)
		}
		feedTitle := "Another title"
		if s.fromFeed {
			feedTitle = s.title
		}
		if _, err := db.Exec(`INSERT INTO feeds (site_id, url, title) VALUES ($1, $2, $3)`, id, "https://"+host+"/feed", feedTitle); err != nil {
			t.Fatalf("insert feed: %v", err)
		}
	}

	mustMigrate(t, db)
	for host, s := range sites {
		var got string
		if err := db.QueryRow(`SELECT title FROM sites WHERE host = $1`, host).Scan(&got); err != nil {
			t.Fatalf("title of %s: %v", host, err)
		}
		if want := discovery.OwnName(host, s.title, s.fromFeed); got != want {
			t.Errorf("title of %s = %q, want %q (OwnName of %q, from a feed: %v)", host, got, want, s.title, s.fromFeed)
		}
	}
}

// 00025 makes the icons of sites on a shared host due now, and leaves
// ordinary sites' schedules alone.
func TestMigrateSharedHostIconsRecheck(t *testing.T) {
	db := newMigrateDB(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 24); err != nil {
		t.Fatalf("migrate to 24: %v", err)
	}
	for _, host := range []string{"medium.com/@alice", "example.com"} {
		if _, err := db.Exec(`INSERT INTO sites (host, url, icon_next_check_at) VALUES ($1, $2, now() + interval '20 days')`, host, "https://"+host); err != nil {
			t.Fatalf("insert site: %v", err)
		}
	}

	mustMigrate(t, db)
	for host, wantDue := range map[string]bool{"medium.com/@alice": true, "example.com": false} {
		var due bool
		if err := db.QueryRow(`SELECT icon_next_check_at <= now() FROM sites WHERE host = $1`, host).Scan(&due); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if due != wantDue {
			t.Errorf("%s due = %v, want %v", host, due, wantDue)
		}
	}
}
