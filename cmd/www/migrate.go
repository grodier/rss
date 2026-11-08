package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/seed"
	"github.com/grodier/rss/migrations"
)

// runMigrate runs "www migrate [flags]": it applies the embedded migrations to
// the database, then with -seed queues the seed list's lookups (seedSites),
// and returns the exit code. Hosts run it before new code goes live (Render's
// pre-deploy command, which takes one command: hence -seed rather than running
// "www seed" after it). Like parseConfig, it writes flag errors and the -help
// output to stderr.
func runMigrate(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	env := "development"
	var dsn string
	var seedList bool

	fs := flag.NewFlagSet("rss-www migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&env, "env", env, "Environment (development|production)")
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")
	fs.BoolVar(&seedList, "seed", seedList, "After migrating, queue lookups of the seed list's sites not found yet, as www seed does")

	if err := cli.SetFlagsFromEnv(fs, getenv); err != nil {
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

	logger := cli.NewLogger(stdout, env)
	cfg := defaultConfig().db
	db, err := psql.OpenDB(dsn, cfg.maxOpenConns, cfg.maxIdleConns, cfg.maxIdleTime)
	if err != nil {
		logger.Error(fmt.Sprintf("open database: %v", err))
		return 1
	}
	defer db.Close()

	ctx := context.Background()
	if err := psql.Migrate(ctx, db, migrations.FS, logger); err != nil {
		logger.Error(err.Error())
		return 1
	}
	if seedList {
		if err := seedSites(ctx, psql.NewSeedRepository(db), seed.Entries(), logger); err != nil {
			logger.Error(err.Error())
			return 1
		}
	}
	return 0
}
