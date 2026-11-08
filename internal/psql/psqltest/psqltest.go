// Package psqltest provides helpers for tests that need a real database.
package psqltest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
)

// DSN returns RSS_TEST_DB_DSN, for tests that need to open their own
// connections. Without it the test is skipped locally, but fails in CI
// (GitHub Actions sets CI=true) so database tests can't silently stop running.
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("RSS_TEST_DB_DSN")
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("RSS_TEST_DB_DSN must be set in CI")
		}
		t.Skip("RSS_TEST_DB_DSN not set; skipping database test")
	}
	return dsn
}

// NewDB connects to the database named by RSS_TEST_DB_DSN (see DSN). The
// database must already be migrated (www migrate). Tests share it, so each
// test must create uniquely-named rows and delete them in t.Cleanup. Never
// truncate tables.
//
// It doesn't use psql.OpenDB because psql's own tests import this package,
// which would be an import cycle.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", DSN(t))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	return db
}

// NewSchemaDSN creates a new, empty schema in the test database, dropped when
// the test ends, and returns a DSN whose search_path puts it first, for tests
// that run migrations without touching public. public stays on the
// search_path for citext.
func NewSchemaDSN(t *testing.T) string {
	t.Helper()
	admin := NewDB(t)
	schema := fmt.Sprintf("schema_test_%d", time.Now().UnixNano())
	name := pq.QuoteIdentifier(schema)
	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + name + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	return withSearchPath(DSN(t), schema+",public")
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
