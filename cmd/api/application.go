package main

import (
	"context"
	"log/slog"

	"github.com/grodier/rss/internal/server"
)

type Application struct {
	logger *slog.Logger
}

func NewApplication(logger *slog.Logger) *Application {
	return &Application{
		logger: logger,
	}
}

func (app *Application) Run(ctx context.Context, args []string) error {
	srv := server.NewServer(app.logger)
	srv.Port = 4000
	srv.Env = "development"
	srv.Version = version

	if err := srv.Serve(); err != nil {
		return err
	}

	return nil
}
