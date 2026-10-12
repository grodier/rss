package psql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lib/pq"
	"github.com/pressly/goose/v3"

	"github.com/grodier/rss/internal/psql/psqltest"
)

// widgetsV1 and widgetsV2 are the same migration before and after an edit, as
// when a pre-alpha schema change edits 00001_init.sql.
var (
	widgetsV1 = fstest.MapFS{
		"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE widgets (id int);\n")},
	}
	widgetsV2 = fstest.MapFS{
		"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE widgets (id int, name text);\n")},
	}
)

// newMigrateDB returns a connection to a new, empty schema of the test
// database that is dropped when the test ends, so Migrate can create and wipe
// it without touching public. public stays on the search_path for citext.
func newMigrateDB(t *testing.T) *sql.DB {
	t.Helper()
	admin := psqltest.NewDB(t)
	schema := fmt.Sprintf("migrate_test_%d", time.Now().UnixNano())
	name := pq.QuoteIdentifier(schema)
	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + name + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})

	db, err := sql.Open("postgres", withSearchPath(psqltest.DSN(t), schema+",public"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// withSearchPath adds search_path to dsn, in either of lib/pq's DSN forms.
func withSearchPath(dsn, path string) string {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		q := u.Query()
		q.Set("search_path", path)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " search_path=" + path
}

func migrate(t *testing.T, db *sql.DB, fsys fstest.MapFS, allowResetTo string) error {
	t.Helper()
	return Migrate(context.Background(), db, fsys, allowResetTo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func mustMigrate(t *testing.T, db *sql.DB, fsys fstest.MapFS) {
	t.Helper()
	if err := migrate(t, db, fsys, ""); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
}

func mustFingerprint(t *testing.T, fsys fstest.MapFS) string {
	t.Helper()
	fp, err := Fingerprint(fsys)
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	return fp
}

func insertWidget(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO widgets (id) VALUES (1)`); err != nil {
		t.Fatalf("insert widget: %v", err)
	}
}

func countWidgets(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM widgets`).Scan(&n); err != nil {
		t.Fatalf("count widgets: %v", err)
	}
	return n
}

func storedFingerprintOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var fp string
	if err := db.QueryRow(`SELECT fingerprint FROM schema_fingerprint`).Scan(&fp); err != nil {
		t.Fatalf("read stored fingerprint: %v", err)
	}
	return fp
}

func TestMigrateFreshDatabase(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db, widgetsV1)

	if got, want := storedFingerprintOf(t, db), mustFingerprint(t, widgetsV1); got != want {
		t.Errorf("stored fingerprint = %s, want %s", got, want)
	}
	if n := countWidgets(t, db); n != 0 {
		t.Errorf("widgets has %d rows, want 0", n)
	}
}

func TestMigrateUnchanged(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db, widgetsV1)
	insertWidget(t, db)

	mustMigrate(t, db, widgetsV1)
	if n := countWidgets(t, db); n != 1 {
		t.Errorf("widgets has %d rows, want 1", n)
	}
}

func TestMigrateChangedRefuses(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db, widgetsV1)
	insertWidget(t, db)

	err := migrate(t, db, widgetsV2, "")
	assertRefused(t, err, mustFingerprint(t, widgetsV2))
	if n := countWidgets(t, db); n != 1 {
		t.Errorf("widgets has %d rows, want 1", n)
	}
}

func TestMigrateChangedWrongFingerprintRefuses(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db, widgetsV1)
	insertWidget(t, db)

	err := migrate(t, db, widgetsV2, mustFingerprint(t, widgetsV1))
	assertRefused(t, err, mustFingerprint(t, widgetsV2))
	if n := countWidgets(t, db); n != 1 {
		t.Errorf("widgets has %d rows, want 1", n)
	}
}

func TestMigrateChangedResets(t *testing.T) {
	db := newMigrateDB(t)
	mustMigrate(t, db, widgetsV1)
	insertWidget(t, db)

	newFP := mustFingerprint(t, widgetsV2)
	if err := migrate(t, db, widgetsV2, newFP); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if n := countWidgets(t, db); n != 0 {
		t.Errorf("widgets has %d rows after the reset, want 0", n)
	}
	var hasName bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'widgets' AND column_name = 'name'
		)`).Scan(&hasName)
	if err != nil {
		t.Fatalf("look up widgets.name: %v", err)
	}
	if !hasName {
		t.Error("widgets.name doesn't exist: the edited migration wasn't applied")
	}
	if got := storedFingerprintOf(t, db); got != newFP {
		t.Errorf("stored fingerprint = %s, want %s", got, newFP)
	}
}

func TestMigrateWithoutStoredFingerprint(t *testing.T) {
	db := newMigrateDB(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, widgetsV1)
	if err != nil {
		t.Fatalf("goose.NewProvider() error = %v", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("goose Up() error = %v", err)
	}

	err = migrate(t, db, widgetsV1, "")
	assertRefused(t, err, mustFingerprint(t, widgetsV1))
}

func assertRefused(t *testing.T, err error, newFP string) {
	t.Helper()
	if err == nil {
		t.Fatal("Migrate() error = nil, want it to refuse")
	}
	for _, want := range []string{"migrations have changed", newFP} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Migrate() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	base := fstest.MapFS{
		"00001_a.sql": {Data: []byte("CREATE TABLE a (id int);")},
		"00002_b.sql": {Data: []byte("CREATE TABLE b (id int);")},
	}
	fp := mustFingerprint(t, base)

	if len(fp) != 64 {
		t.Errorf("Fingerprint() = %q, want 64 hex characters", fp)
	}
	if again := mustFingerprint(t, fstest.MapFS{
		"00001_a.sql": {Data: []byte("CREATE TABLE a (id int);")},
		"00002_b.sql": {Data: []byte("CREATE TABLE b (id int);")},
	}); again != fp {
		t.Errorf("same files: Fingerprint() = %s, then %s", fp, again)
	}

	tests := []struct {
		name     string
		fsys     fstest.MapFS
		wantSame bool
	}{
		{"contents changed", fstest.MapFS{
			"00001_a.sql": {Data: []byte("CREATE TABLE a (id bigint);")},
			"00002_b.sql": {Data: []byte("CREATE TABLE b (id int);")},
		}, false},
		{"name changed", fstest.MapFS{
			"00001_a.sql":     {Data: []byte("CREATE TABLE a (id int);")},
			"00002_b_new.sql": {Data: []byte("CREATE TABLE b (id int);")},
		}, false},
		{"contents moved between files", fstest.MapFS{
			"00001_a.sql": {Data: []byte("CREATE TABLE a (id int);CREATE TABLE b")},
			"00002_b.sql": {Data: []byte(" (id int);")},
		}, false},
		{"non-sql files ignored", fstest.MapFS{
			"00001_a.sql":   {Data: []byte("CREATE TABLE a (id int);")},
			"00002_b.sql":   {Data: []byte("CREATE TABLE b (id int);")},
			"migrations.go": {Data: []byte("package migrations")},
			"README.md":     {Data: []byte("notes")},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustFingerprint(t, tt.fsys)
			if same := got == fp; same != tt.wantSame {
				t.Errorf("Fingerprint() = %s, base %s: same = %v, want %v", got, fp, same, tt.wantSame)
			}
		})
	}
}
