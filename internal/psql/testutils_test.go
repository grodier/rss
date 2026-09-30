package psql

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// newTestDB connects to the database named by RSS_TEST_DB_DSN. The database must
// already be migrated (goose up). Tests share it, so each test must create
// uniquely-named rows and delete them in t.Cleanup. Never truncate tables.
//
// Without RSS_TEST_DB_DSN the test is skipped locally, but fails in CI
// (GitHub Actions sets CI=true) so database tests can't silently stop running.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("RSS_TEST_DB_DSN")
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("RSS_TEST_DB_DSN must be set in CI")
		}
		t.Skip("RSS_TEST_DB_DSN not set; skipping database test")
	}
	db, err := OpenDB(dsn, 5, 5, time.Minute)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
