package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

type config struct {
	env     string
	server  serverConfig
	db      dbConfig
	lookup  lookupConfig
	refresh refreshConfig
}

type serverConfig struct {
	port int
	// inviteCode, when set, opens signup to people who have it.
	inviteCode string
	// proxyHops is how many reverse proxies in front of the app append to
	// X-Forwarded-For; 0 ignores the header.
	proxyHops int
}

type lookupConfig struct {
	workers int
}

type refreshConfig struct {
	workers int
}

type dbConfig struct {
	dsn          string
	maxOpenConns int
	maxIdleConns int
	maxIdleTime  time.Duration
}

func defaultConfig() config {
	return config{
		env: "development",
		server: serverConfig{
			port: 8080,
		},
		db: dbConfig{
			maxOpenConns: 25,
			maxIdleConns: 25,
			maxIdleTime:  15 * time.Minute,
		},
		lookup: lookupConfig{
			workers: 2,
		},
		refresh: refreshConfig{
			workers: 2,
		},
	}
}

func (c config) Validate() error {
	var errs []error
	if c.env != "development" && c.env != "production" {
		errs = append(errs, fmt.Errorf("invalid environment %q: must be development or production", c.env))
	}
	if c.server.port < 1 || c.server.port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port %d: must be 1-65535", c.server.port))
	}
	if c.server.proxyHops < 0 || c.server.proxyHops > 3 {
		errs = append(errs, errors.New("proxy hops must be between 0 and 3"))
	}
	if c.server.inviteCode != "" && len(c.server.inviteCode) < 16 {
		errs = append(errs, errors.New("invite code must be at least 16 characters"))
	}
	if c.db.dsn == "" {
		errs = append(errs, errors.New("database DSN must not be empty"))
	}
	if c.lookup.workers < 0 || c.lookup.workers > 16 {
		errs = append(errs, fmt.Errorf("invalid lookup workers %d: must be 0-16", c.lookup.workers))
	}
	if c.refresh.workers < 0 || c.refresh.workers > 16 {
		errs = append(errs, fmt.Errorf("invalid refresh workers %d: must be 0-16", c.refresh.workers))
	}
	return errors.Join(errs...)
}

// parseConfig reads the configuration from args and, for flags not on the
// command line, from RSS_ environment variables looked up with getenv. Like
// the flag package, it writes its errors and the -help output to out, so the
// caller only has to exit.
func parseConfig(args []string, getenv func(string) string, out io.Writer) (config, error) {
	cfg := defaultConfig()

	fs := flag.NewFlagSet("rss-www", flag.ContinueOnError)
	fs.SetOutput(out)

	fs.StringVar(&cfg.env, "env", cfg.env, "Environment (development|production)")
	fs.IntVar(&cfg.server.port, "port", cfg.server.port, "Server port")
	fs.StringVar(&cfg.server.inviteCode, "invite-code", cfg.server.inviteCode, "Open signup to people with this code (at least 16 characters; default: signup off)")

	fs.IntVar(&cfg.server.proxyHops, "proxy-hops", cfg.server.proxyHops, "Reverse proxies in front of the app that append to X-Forwarded-For (0-3; 0 ignores the header)")

	fs.StringVar(&cfg.db.dsn, "db-dsn", cfg.db.dsn, "PostgreSQL DSN")
	fs.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", cfg.db.maxOpenConns, "PostgreSQL max open connections")
	fs.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", cfg.db.maxIdleConns, "PostgreSQL max idle connections")
	fs.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", cfg.db.maxIdleTime, "PostgreSQL max idle time")

	fs.IntVar(&cfg.lookup.workers, "lookup-workers", cfg.lookup.workers, "Background lookup workers (0 disables them)")
	fs.IntVar(&cfg.refresh.workers, "refresh-workers", cfg.refresh.workers, "Background feed refresh workers (0 disables them)")

	if err := setFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}
	// fs.Parse reports its own errors to out.
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}

	return cfg, nil
}

// setFlagsFromEnv sets each flag in fs from its RSS_ environment variable, if
// set and not empty, and appends "(env RSS_…)" to its usage. Call it before
// fs.Parse so flags on the command line win.
func setFlagsFromEnv(fs *flag.FlagSet, getenv func(string) string) error {
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		name := envName(f.Name)
		f.Usage += " (env " + name + ")"
		if err != nil {
			return
		}
		if v := getenv(name); v != "" {
			if setErr := f.Value.Set(v); setErr != nil {
				err = fmt.Errorf("invalid value %q for %s: %w", v, name, setErr)
			}
		}
	})
	return err
}

// envName returns the environment variable for a flag: -db-dsn → RSS_DB_DSN.
func envName(flagName string) string {
	return "RSS_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}
