package psql

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log/slog"
	"testing"

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
