package server

import (
	"io"
	"log/slog"
	"testing"

	"github.com/alexedwards/scs/v2"
)

// newTestServer returns a Server wired with no database and no services.
// Services{} holds nil stores, so tests can only exercise code paths that
// return before a store call (validation, routing, middleware, templates). Use
// newTestServerWith and the fakes in fakes_test.go for the rest.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWith(t, Services{})
}

// newTestServerWith returns a Server wired with the given services (typically
// fakes from fakes_test.go) and no database.
func newTestServerWith(t *testing.T, svc Services) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development"}, svc)
}

// newTestServerConfig is newTestServerWith with an explicit Config.
func newTestServerConfig(t *testing.T, cfg Config, svc Services) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := NewServer(logger, cfg, svc, scs.New())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
