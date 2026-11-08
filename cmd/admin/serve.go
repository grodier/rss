package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/grodier/rss/internal/admin"
	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
)

// errNoLogin is why the admin refuses to run in production: anyone who
// reached it could use it. #567 adds the login, and removes the check.
var errNoLogin = errors.New("admin has no login yet (#567); it only runs locally")

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
	if cfg.env == "production" {
		fmt.Fprintln(stderr, errNoLogin)
		return 2
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

	srv, err := admin.New(logger, admin.Config{
		Port:      cfg.port,
		Env:       cfg.env,
		Version:   version,
		BaseURL:   cfg.baseURL,
		ProxyHops: cfg.proxyHops,
	}, admin.Services{Database: db})
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
