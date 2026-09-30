package server

import (
	"io"
	"log/slog"
	"testing"

	"github.com/alexedwards/scs/v2"
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
