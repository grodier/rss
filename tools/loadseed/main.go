// Command loadseed seeds a development database with a realistic amount of
// feeds, articles and subscriptions, and measures the reading queries
// against it: the psql repository methods themselves, with their plans.
//
// It's a development tool: it refuses to run against a database whose name
// doesn't end in _dev or _test, and it isn't part of the www binary.
//
//	go run ./tools/loadseed              # clean up, seed, then measure
//	go run ./tools/loadseed -seed-only   # clean up and seed
//	go run ./tools/loadseed -measure     # measure an earlier seed
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/lib/pq"
)

// errNotDevDatabase is returned for a DSN whose database isn't a
// development or test one.
var errNotDevDatabase = errors.New("refusing to run: the database name must end in _dev or _test")

// validPrefix is what -prefix may be: it goes into host names and emails,
// and into the pattern cleanup matches hosts with.
var validPrefix = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type options struct {
	dsn     string
	measure bool // measure only, without seeding
	seedCfg seedConfig
	// seedOnly skips measuring after seeding.
	seedOnly bool
}

func main() {
	// No signal handling (only cmd/www handles OS signals): an interrupt
	// ends the process, which rolls back the seed's open transaction.
	err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadseed:", err)
		os.Exit(1)
	}
}

// run parses args, checks the DSN names a development database, then seeds
// and measures as the flags say, printing a report to out.
func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	opts, err := parseFlags(args, getenv)
	if err != nil {
		return err
	}
	if err := checkDevDatabase(opts.dsn); err != nil {
		return err
	}

	if !opts.measure {
		db, err := psql.OpenDB(opts.dsn, 4, 4, time.Minute)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := seed(ctx, db, opts.seedCfg, out); err != nil {
			return err
		}
		if opts.seedOnly {
			return nil
		}
		fmt.Fprintln(out)
	}
	return measure(ctx, opts.dsn, opts.seedCfg.prefix, out)
}

func parseFlags(args []string, getenv func(string) string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("loadseed", flag.ContinueOnError)
	fs.StringVar(&opts.dsn, "db-dsn", "", "PostgreSQL DSN of a database whose name ends in _dev or _test (default: RSS_DB_DSN)")
	fs.StringVar(&opts.seedCfg.prefix, "prefix", "loadseed", "prefix of the seeded users' emails and sites' hosts")
	fs.IntVar(&opts.seedCfg.feeds, "feeds", 1000, "feeds to seed, one site each")
	fs.IntVar(&opts.seedCfg.subscriptions, "subscriptions", 300, "feeds the main user subscribes to")
	fs.Float64Var(&opts.seedCfg.years, "years", 2, "years of articles to seed, back from now")
	fs.Int64Var(&opts.seedCfg.seed, "seed", 1, "random seed")
	fs.BoolVar(&opts.measure, "measure", false, "only measure an earlier seed")
	fs.BoolVar(&opts.seedOnly, "seed-only", false, "only seed, without measuring")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	// Not the flag's default, so -h doesn't print the DSN's password.
	if opts.dsn == "" {
		opts.dsn = getenv("RSS_DB_DSN")
	}

	switch {
	case opts.dsn == "":
		return options{}, errors.New("set -db-dsn or RSS_DB_DSN")
	case !validPrefix.MatchString(opts.seedCfg.prefix):
		return options{}, fmt.Errorf("-prefix %q: use lowercase letters, digits and inner hyphens", opts.seedCfg.prefix)
	case opts.seedCfg.feeds < 1:
		return options{}, errors.New("-feeds must be at least 1")
	case opts.seedCfg.subscriptions < 1 || opts.seedCfg.subscriptions > opts.seedCfg.feeds:
		return options{}, errors.New("-subscriptions must be between 1 and -feeds")
	case opts.seedCfg.years <= 0:
		return options{}, errors.New("-years must be positive")
	case opts.measure && opts.seedOnly:
		return options{}, errors.New("-measure and -seed-only can't be used together")
	}
	return opts, nil
}

// checkDevDatabase returns errNotDevDatabase unless dsn names a database
// ending in _dev or _test. It only parses dsn, never connects.
func checkDevDatabase(dsn string) error {
	cfg, err := pq.NewConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	if !strings.HasSuffix(cfg.Database, "_dev") && !strings.HasSuffix(cfg.Database, "_test") {
		return fmt.Errorf("%w (got %q)", errNotDevDatabase, cfg.Database)
	}
	return nil
}
