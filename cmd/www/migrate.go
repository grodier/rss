package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/grodier/rss/internal/cli"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/migrations"
)

// runMigrate runs "www migrate [flags]": it applies the embedded migrations to
// the database, then re-keys the stored sites if the site-key rules changed
// (rekey), and returns the exit code. Hosts run it before new code goes live
// (Render's pre-deploy command), so a failure of either fails the deploy.
func runMigrate(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return runDBCommand("migrate", args, getenv, stdout, stderr, func(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
		if err := psql.Migrate(ctx, db, migrations.FS, logger); err != nil {
			return err
		}
		return rekey(ctx, db, logger)
	})
}

// runRekey runs "www rekey [flags]": only the re-key step of migrate, for
// local databases.
func runRekey(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return runDBCommand("rekey", args, getenv, stdout, stderr, rekey)
}

// rekey moves the stored feeds onto the sites the current site-key rules
// name, once per discovery.SiteKeyRulesVersion (psql's Rekey).
func rekey(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	res, err := psql.NewSiteRepository(db).Rekey(ctx, discovery.SiteKeyRulesVersion, discovery.FeedSiteKey, discovery.AliasHosts())
	if err != nil {
		return fmt.Errorf("re-key sites: %w", err)
	}
	if res.Skipped {
		logger.Debug("site keys: current", "version", discovery.SiteKeyRulesVersion)
		return nil
	}
	logger.Info("site keys: re-keyed", "version", discovery.SiteKeyRulesVersion,
		"feeds", res.Feeds, "created", res.Created, "deleted", res.Deleted)
	return nil
}

// runDBCommand parses the flags of "www <name> [flags]", opens the database
// and runs fn on it, returning the exit code: 2 for invalid flags, 1 when fn
// fails. Like parseConfig, it writes flag errors and the -help output to
// stderr.
func runDBCommand(name string, args []string, getenv func(string) string, stdout, stderr io.Writer, fn func(context.Context, *sql.DB, *slog.Logger) error) int {
	env := "development"
	var dsn string

	fs := flag.NewFlagSet("rss-www "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&env, "env", env, "Environment (development|production)")
	fs.StringVar(&dsn, "db-dsn", dsn, "PostgreSQL DSN")

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

	if err := fn(context.Background(), db, logger); err != nil {
		logger.Error(err.Error())
		return 1
	}
	return 0
}
