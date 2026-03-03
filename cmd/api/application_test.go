package main

import (
	"log/slog"
	"os"
	"testing"
)

func newTestApplication() *Application {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewApplication(logger)
}

func TestParseConfigs_Defaults(t *testing.T) {
	app := newTestApplication()

	cfg := app.ParseConfigs([]string{})

	if cfg.server.port != 8080 {
		t.Errorf("default port: got %d, want %d", cfg.server.port, 8080)
	}

	if cfg.env != "development" {
		t.Errorf("default env: got %q, want %q", cfg.env, "development")
	}
}

func TestParseConfigs_ValidFlags(t *testing.T) {
	app := newTestApplication()

	cfg := app.ParseConfigs([]string{"-port", "3000", "-env", "production"})

	if cfg.server.port != 3000 {
		t.Errorf("port: got %d, want %d", cfg.server.port, 3000)
	}

	if cfg.env != "production" {
		t.Errorf("env: got %q, want %q", cfg.env, "production")
	}
}

func TestParseConfigs_InvalidEnvFallback(t *testing.T) {
	app := newTestApplication()

	cfg := app.ParseConfigs([]string{"-env", "staging"})

	if cfg.env != "development" {
		t.Errorf("env: got %q, want %q (expected fallback to development)", cfg.env, "development")
	}
}
