package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestRunReturnsErrorWhenDBUnreachable(t *testing.T) {
	app := NewApplication(slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := app.Run(context.Background(), []string{
		"-db-dsn", "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
	})
	if err == nil {
		t.Fatal("expected an error when the database is unreachable, got nil")
	}
}
