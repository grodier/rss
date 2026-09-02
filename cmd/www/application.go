package main

import (
	"context"
	"flag"
	"log/slog"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/psql"
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

	db, err := psql.OpenDB(app.config.db.dsn, app.config.db.maxOpenConns, app.config.db.maxIdleConns, app.config.db.maxIdleTime)
	if err != nil {
		return nil
	}
	defer db.Close()

	services := server.Services{
		FeedService: psql.NewFeedRepository(db),
	}

	srvConfig := server.Config{
		Port: app.config.server.port,
		Env:  app.config.env,
	}

	// TODO: consider abstracting to manage chosen impl
	sessionManager := scs.New()
	sessionManager.Store = postgresstore.New(db)
	sessionManager.Lifetime = 12 * time.Hour

	srv, err := server.NewServer(app.logger, srvConfig, services, sessionManager)
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

	fs.StringVar(&cfg.db.dsn, "db-dsn", cfg.db.dsn, "PostgreSQL DSN")
	fs.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", cfg.db.maxOpenConns, "PostgreSQL max open connections")
	fs.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", cfg.db.maxIdleConns, "PostgreSQL max idle connections")
	fs.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", cfg.db.maxIdleTime, "PostgreSQL max idle time")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}
