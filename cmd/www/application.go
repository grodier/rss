package main

import (
	"context"
	"flag"
	"log/slog"

	"github.com/grodier/rss/internal/server"
)

type Application struct {
	config config
	logger *slog.Logger
}

func NewApplication(logger *slog.Logger) *Application {
	return &Application{
		logger: logger,
	}
}

func (app *Application) Run(ctx context.Context, args []string) error {
	cfg, err := app.ParseConfigs(args)
	if err != nil {
		return err
	}
	app.config = cfg

	srv, err := server.NewServer(app.logger, server.Config{
		Port: app.config.server.port,
		Env:  app.config.env,
	})
	if err != nil {
		return err
	}

	return srv.Serve()
}

func (app *Application) ParseConfigs(args []string) (config, error) {
	cfg := defaultConfig()

	fs := flag.NewFlagSet("rss-www", flag.ContinueOnError)

	fs.StringVar(&cfg.env, "env", cfg.env, "Environment (development|production)")
	fs.IntVar(&cfg.server.port, "port", cfg.server.port, "Server port")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}
