package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestRunReturnsErrorWhenDBUnreachable(t *testing.T) {
	cfg := defaultConfig()
	cfg.db.dsn = "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"
	app := NewApplication(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := app.Run(); err == nil {
		t.Fatal("expected an error when the database is unreachable, got nil")
	}
}
