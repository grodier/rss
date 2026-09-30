// Package psqltest provides helpers for tests that need a real database.
package psqltest

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// NewDB connects to the database named by RSS_TEST_DB_DSN. The database must
// already be migrated (goose up). Tests share it, so each test must create
// uniquely-named rows and delete them in t.Cleanup. Never truncate tables.
//
// Without RSS_TEST_DB_DSN the test is skipped locally, but fails in CI
// (GitHub Actions sets CI=true) so database tests can't silently stop running.
//
// It doesn't use psql.OpenDB because psql's own tests import this package,
// which would be an import cycle.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("RSS_TEST_DB_DSN")
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("RSS_TEST_DB_DSN must be set in CI")
		}
		t.Skip("RSS_TEST_DB_DSN not set; skipping database test")
	}
	db, err := sql.Open("postgres", dsn)
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
