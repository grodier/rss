package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexedwards/scs/postgresstore"

	"github.com/grodier/rss/internal/admin"
	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
)

// runServe runs "admin serve [flags]": the admin site, until SIGINT or
// SIGTERM. It returns the exit code.
func runServe(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if cfg.version {
		fmt.Fprintln(stdout, version)
		return 0
	}

	logger := cli.NewLogger(stdout, cfg.env)
	if err := serve(cfg, logger); err != nil {
		logger.Error(err.Error())
		return 1
	}
	return 0
}

// serve opens the database and runs the admin's server until a signal stops
// it. The admin runs none of www's background workers.
func serve(cfg config, logger *slog.Logger) error {
	db, err := psql.OpenDB(cfg.db.dsn, cfg.db.maxOpenConns, cfg.db.maxIdleConns, cfg.db.maxIdleTime)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	key, err := secretKey(cfg, logger)
	if err != nil {
		return err
	}

	store := postgresstore.NewWithConfig(db, postgresstore.Config{TableName: "admin_sessions", CleanUpInterval: 5 * time.Minute})
	defer store.StopCleanup()
	sessions := admin.NewSessionManager(store, cfg.env)

	srv, err := admin.New(logger, admin.Config{
		Port:         cfg.port,
		Env:          cfg.env,
		Version:      version,
		BaseURL:      cfg.baseURL,
		ProxyHops:    cfg.proxyHops,
		SecretKey:    key,
		AppURL:       cfg.appURL,
		SupportEmail: cfg.supportEmail,
		MailLimits:   cfg.mailLimits,
	}, admin.Services{
		Database: db,
		Admins:   psql.NewAdminRepository(db),
		Audit:    psql.NewAuditRepository(db),
		Waitlist: psql.NewWaitlistRepository(db),
		Invites:  psql.NewInviteRepository(db),
		Emails:   psql.NewEmailRepository(db),
		Overview: psql.NewAdminOverviewRepository(db),
		WebPerf:  psql.NewAdminWebPerfRepository(db),
	}, sessions)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore default signal handling so a second
	// Ctrl-C kills the process if graceful shutdown hangs.
	context.AfterFunc(ctx, stop)

	return srv.Serve(ctx)
}

// secretKey returns -secret-key's key or, in development without one, a new
// key for this run, with a warning: admins enrolled with another key can't
// log in (Validate refuses production without one).
func secretKey(cfg config, logger *slog.Logger) ([]byte, error) {
	if cfg.secretKey != "" {
		return parseSecretKey(cfg.secretKey)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	logger.Warn("no -secret-key (RSS_SECRET_KEY): using a new key for this run, so admins enrolled with another key can't log in; set RSS_SECRET_KEY in .env (openssl rand -hex 32), then admin enroll")
	return key, nil
}
