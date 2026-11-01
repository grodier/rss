package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/seed"
)

// runSeed runs "www seed [flags]": it queues lookups of the seed list's sites
// not found yet (seedSites) and returns the exit code. It's run by hand (make
// db/seed-sites), never by make db/reset, so a local reset doesn't look up
// the whole list; production runs it on every deploy as "www migrate -seed".
// Like parseConfig, it writes flag errors and the -help output to stderr.
func runSeed(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	env := "development"
	var dsn string

	fs := flag.NewFlagSet("rss-www seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&env, "env", env, "Environment (development|production)")
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")

	if err := setFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// fs.Parse reports its own errors to stderr.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	var errs []error
	if env != "development" && env != "production" {
		errs = append(errs, fmt.Errorf("invalid environment %q: must be development or production", env))
	}
	if dsn == "" {
		errs = append(errs, errors.New("database DSN must not be empty"))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	logger := newLogger(stdout, env)
	cfg := defaultConfig().db
	db, err := psql.OpenDB(dsn, cfg.maxOpenConns, cfg.maxIdleConns, cfg.maxIdleTime)
	if err != nil {
		logger.Error(fmt.Sprintf("open database: %v", err))
		return 1
	}
	defer db.Close()

	if err := seedSites(context.Background(), psql.NewSeedRepository(db), seed.Entries(), logger); err != nil {
		logger.Error(err.Error())
		return 1
	}
	return 0
}

// seedSites queues lookups of the seed list's sites that haven't been found
// (seed.Run); the app's lookup workers then find them, after readers' own
// lookups. It logs what it queued, and each site whose lookup has failed, so
// a site that keeps failing can be fixed or dropped in sites.txt.
func seedSites(ctx context.Context, store seed.Store, entries []seed.Entry, logger *slog.Logger) error {
	res, err := seed.Run(ctx, store, entries)
	if err != nil {
		return err
	}
	logger.Info("seed list queued", "sites", len(entries), "lookups_queued", res.Queued, "lookups_retried", len(res.Retried))
	if len(res.Retried) > 0 {
		logger.Warn("seed list: retrying lookups that failed or found no feeds", "site_keys", res.Retried)
	}
	if len(res.Missing) > 0 {
		logger.Warn("seed list: lookups that failed or found no feeds too recently to retry; a later run retries them", "site_keys", res.Missing)
	}
	return nil
}
