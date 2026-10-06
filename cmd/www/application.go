package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/lookup"
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

func (app *Application) Run(args []string) error {
	cfg, err := app.ParseConfigs(args)
	if err != nil {
		return err
	}
	app.config = cfg

	db, err := psql.OpenDB(app.config.db.dsn, app.config.db.maxOpenConns, app.config.db.maxIdleConns, app.config.db.maxIdleTime)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// One client serves lookups and refreshes; it is safe for concurrent use.
	fetcher := fetch.New(fetch.Options{})

	services := server.Services{
		FeedService:    psql.NewFeedRepository(db),
		UserService:    psql.NewUserRepository(db),
		SearchService:  psql.NewSearchRepository(db),
		SiteService:    psql.NewSiteRepository(db),
		LookupService:  psql.NewLookupRepository(db),
		ArticleService: psql.NewArticleRepository(db),
		Refresher:      &ingest.Refresher{Fetcher: fetcher, Store: psql.NewFeedRepository(db)},
	}

	srvConfig := server.Config{
		Port: app.config.server.port,
		Env:  app.config.env,
	}

	sessionManager := scs.New()
	sessionManager.Store = postgresstore.New(db)
	sessionManager.Lifetime = 12 * time.Hour
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = app.config.env == "production"

	srv, err := server.NewServer(app.logger, srvConfig, services, sessionManager)
	if err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore default signal handling so a second
	// Ctrl-C kills the process if graceful shutdown hangs.
	context.AfterFunc(sigCtx, stop)

	// ctx is also canceled when either component fails, stopping the other.
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	var runnerErr error
	runnerDone := make(chan struct{})
	if app.config.lookup.workers > 0 {
		runner := &lookup.Runner{
			Store:      psql.NewLookupRepository(db),
			Saver:      psql.NewDiscoveryRepository(db),
			Discoverer: &discovery.Discoverer{Fetcher: fetcher},
			Logger:     app.logger,
			Workers:    app.config.lookup.workers,
		}
		go func() {
			defer close(runnerDone)
			if err := runner.Run(ctx); err != nil {
				runnerErr = fmt.Errorf("lookup runner: %w", err)
				cancel()
			}
		}()
	} else {
		close(runnerDone)
	}

	err = srv.Serve(ctx)

	// Stop the workers (if Serve returned on its own) and wait for them
	// before the deferred db.Close runs.
	cancel()
	<-runnerDone

	return errors.Join(err, runnerErr)
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

	fs.IntVar(&cfg.lookup.workers, "lookup-workers", cfg.lookup.workers, "Background lookup workers (0 disables them)")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}
