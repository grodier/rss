package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/grodier/rss/internal/pgsql"
	"github.com/grodier/rss/internal/server"
)

type Application struct {
	config config
	logger *slog.Logger
}

func NewApplication(logger *slog.Logger) *Application {
	return &Application{
		config: defaultConfig(),
		logger: logger,
	}
}

type config struct {
	env    string
	server serverConfig
	db     dbConfig
}

type serverConfig struct {
	port int
}

type dbConfig struct {
	dsn                string
	maxOpenConnections int
	maxIdleConnections int
	maxIdleTime        time.Duration
}

func defaultConfig() config {
	return config{
		env: "development",
		server: serverConfig{
			port: 8080,
		},
		db: dbConfig{
			dsn:                os.Getenv("RSS_DB_DSN"),
			maxOpenConnections: 25,
			maxIdleConnections: 25,
			maxIdleTime:        15 * time.Minute,
		},
	}
}

func (app *Application) Run(ctx context.Context, args []string) error {
	app.config = app.ParseConfigs(args)

	db, err := pgsql.OpenDB(app.config.db.dsn, app.config.db.maxOpenConnections, app.config.db.maxIdleConnections, app.config.db.maxIdleTime)
	if err != nil {
		return err
	}
	defer db.Close()

	app.logger.Info("database connection pool established")

	srv := server.NewServer(app.logger)
	srv.Port = app.config.server.port
	srv.Env = app.config.env
	srv.Version = version

	if err := srv.Serve(); err != nil {
		return err
	}

	return nil
}

func (app *Application) ParseConfigs(args []string) config {
	config := defaultConfig()

	fs := flag.NewFlagSet("rss-api", flag.ContinueOnError)

	fs.StringVar(&config.env, "env", config.env, "Environment (development|production)")
	fs.IntVar(&config.server.port, "port", config.server.port, "Server port")

	fs.StringVar(&config.db.dsn, "db-dsn", config.db.dsn, "Database DSN")
	fs.IntVar(&config.db.maxOpenConnections, "db-max-open-connections", config.db.maxOpenConnections, "Database max open connections")
	fs.IntVar(&config.db.maxIdleConnections, "db-max-idle-connections", config.db.maxIdleConnections, "Database max idle connections")
	fs.DurationVar(&config.db.maxIdleTime, "db-max-idle-time", config.db.maxIdleTime, "Database max idle time")

	fs.Parse(args)

	if config.env != "development" && config.env != "production" {
		app.logger.Warn("invalid environment value, falling back to default", "provided", config.env, "default", "development")
		config.env = "development"
	}

	return config
}
