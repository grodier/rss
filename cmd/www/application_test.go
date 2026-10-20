package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestRunReturnsErrorWhenDBUnreachable(t *testing.T) {
	cfg := defaultConfig()
	cfg.db.dsn = "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"
	app := NewApplication(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := app.Run(); err == nil {
		t.Fatal("expected an error when the database is unreachable, got nil")
	}
}

func TestNewSessionManager(t *testing.T) {
	// sql.Open doesn't connect, so no database is needed.
	db, err := sql.Open("postgres", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	tests := []struct {
		env        string
		wantSecure bool
	}{
		{"development", false},
		{"production", true},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			sm := newSessionManager(db, tt.env)
			if sm.Lifetime != 365*24*time.Hour {
				t.Errorf("Lifetime = %v, want 365 days", sm.Lifetime)
			}
			if sm.IdleTimeout != 0 {
				t.Errorf("IdleTimeout = %v, want 0", sm.IdleTimeout)
			}
			if !sm.Cookie.Persist {
				t.Error("Cookie.Persist = false, want true")
			}
			if sm.Cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("Cookie.SameSite = %v, want Lax", sm.Cookie.SameSite)
			}
			if sm.Cookie.Secure != tt.wantSecure {
				t.Errorf("Cookie.Secure = %v, want %v", sm.Cookie.Secure, tt.wantSecure)
			}
		})
	}
}

func TestNamed(t *testing.T) {
	boom := errors.New("boom")
	err := named("mail runner", func(context.Context) error { return boom })(t.Context())
	if !errors.Is(err, boom) || err.Error() != "mail runner: boom" {
		t.Errorf("error = %v, want %q wrapping boom", err, "mail runner: boom")
	}

	if err := named("mail runner", func(context.Context) error { return nil })(t.Context()); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}
