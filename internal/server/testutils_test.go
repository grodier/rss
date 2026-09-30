package server

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/psql"
)

// newTestServer returns a Server wired with no database. Services{} holds nil
// repositories, so tests can only exercise code paths that return before a
// repository call (validation, routing, middleware, templates).
func newTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := NewServer(logger, Config{Port: 4000, Env: "development"}, Services{}, scs.New())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// newTestDB connects to the database named by RSS_TEST_DB_DSN, which must
// already be migrated. Without it the test is skipped locally, but fails in CI.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("RSS_TEST_DB_DSN")
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("RSS_TEST_DB_DSN must be set in CI")
		}
		t.Skip("RSS_TEST_DB_DSN not set; skipping database test")
	}
	db, err := psql.OpenDB(dsn, 5, 5, time.Minute)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
