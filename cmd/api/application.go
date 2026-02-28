package main

import (
	"context"
	"log/slog"
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
	app.logger.Info("Application started", "version", version)
	return nil
}
