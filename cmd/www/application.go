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
	"sync"
	"syscall"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/lookup"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/refresh"
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
	// One refresher serves the Refresh button and the background workers.
	refresher := &ingest.Refresher{Fetcher: fetcher, Store: psql.NewFeedRepository(db)}

	services := server.Services{
		FeedService:    psql.NewFeedRepository(db),
		UserService:    psql.NewUserRepository(db),
		SearchService:  psql.NewSearchRepository(db),
		SiteService:    psql.NewSiteRepository(db),
		LookupService:  psql.NewLookupRepository(db),
		ArticleService: psql.NewArticleRepository(db),
		Refresher:      refresher,
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

	// Background components run alongside the server until ctx is canceled.
	var background []func(context.Context) error
	if app.config.lookup.workers > 0 {
		runner := &lookup.Runner{
			Store:      psql.NewLookupRepository(db),
			Saver:      psql.NewDiscoveryRepository(db),
			Discoverer: &discovery.Discoverer{Fetcher: fetcher},
			Logger:     app.logger,
			Workers:    app.config.lookup.workers,
		}
		background = append(background, func(ctx context.Context) error {
			if err := runner.Run(ctx); err != nil {
				return fmt.Errorf("lookup runner: %w", err)
			}
			return nil
		})
	}
	if app.config.refresh.workers > 0 {
		runner := &refresh.Runner{
			Store:     psql.NewFeedRepository(db),
			Refresher: refresher,
			Logger:    app.logger,
			Workers:   app.config.refresh.workers,
		}
		background = append(background, func(ctx context.Context) error {
			if err := runner.Run(ctx); err != nil {
				return fmt.Errorf("refresh runner: %w", err)
			}
			return nil
		})
	}

	// ctx is also canceled when any component fails, stopping the others.
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, len(background))
	for i, run := range background {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(ctx); err != nil {
				errs[i] = err
				cancel()
			}
		}()
	}

	err = srv.Serve(ctx)

	// Stop the background components (if Serve returned on its own) and
	// wait for them before the deferred db.Close runs.
	cancel()
	wg.Wait()

	return errors.Join(append([]error{err}, errs...)...)
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
	fs.IntVar(&cfg.refresh.workers, "refresh-workers", cfg.refresh.workers, "Background feed refresh workers (0 disables them)")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}
