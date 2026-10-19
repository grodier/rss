package main

import (
	"context"
	"database/sql"
	"errors"
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
	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/refresh"
	"github.com/grodier/rss/internal/server"
)

type Application struct {
	config config
	logger *slog.Logger
}

func NewApplication(cfg config, logger *slog.Logger) *Application {
	return &Application{
		config: cfg,
		logger: logger,
	}
}

func (app *Application) Run() error {
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
		FeedService:         psql.NewFeedRepository(db),
		UserService:         psql.NewUserRepository(db),
		SessionService:      psql.NewUserSessionRepository(db),
		SearchService:       psql.NewSearchRepository(db),
		SiteService:         psql.NewSiteRepository(db),
		LookupService:       psql.NewLookupRepository(db),
		ArticleService:      psql.NewArticleRepository(db),
		SubscriptionService: psql.NewSubscriptionRepository(db),
		ReadService:         psql.NewReadRepository(db),
		Refresher:           refresher,
	}

	srvConfig := server.Config{
		Port:         app.config.server.port,
		Env:          app.config.env,
		InviteCode:   app.config.server.inviteCode,
		Version:      version,
		ProxyHops:    app.config.server.proxyHops,
		BaseURL:      app.config.mail.baseURL,
		SupportEmail: app.config.mail.supportEmail,
	}

	sessionManager := newSessionManager(db, app.config.env)

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

// sessionLifetime is the most a login lasts, however often it's used; the
// server ends idle ones sooner. See "Accounts" in docs/decisions.md.
const sessionLifetime = 365 * 24 * time.Hour

// newSessionManager returns the session manager, storing sessions in db.
func newSessionManager(db *sql.DB, env string) *scs.SessionManager {
	sessionManager := scs.New()
	sessionManager.Store = postgresstore.New(db)
	sessionManager.Lifetime = sessionLifetime
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = env == "production"
	return sessionManager
}

// newMailSender returns the sender c describes: SMTP, or the log when there's
// no SMTP host.
func newMailSender(c mailConfig, logger *slog.Logger) mail.Sender {
	if c.smtpHost == "" {
		return &mail.LogSender{Logger: logger}
	}
	return &mail.SMTPSender{
		Host:     c.smtpHost,
		Port:     c.smtpPort,
		Username: c.smtpUsername,
		Password: c.smtpPassword,
		From:     c.from,
		ReplyTo:  c.supportEmail,
	}
}
