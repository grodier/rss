package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/grodier/rss/internal/cli"
)

type config struct {
	env        string
	server     serverConfig
	db         dbConfig
	lookup     lookupConfig
	refresh    refreshConfig
	importer   importerConfig
	icons      iconsConfig
	enrich     enrichConfig
	mailRunner mailRunnerConfig
	mail       cli.Mail
}

type serverConfig struct {
	port int
	// inviteCode, when set, opens signup to people who have it.
	inviteCode string
	// proxyHops is how many reverse proxies in front of the app append to
	// X-Forwarded-For; 0 ignores the header.
	proxyHops int
	// debugEmails are the accounts that may turn on debug mode, lowercased;
	// empty turns debug mode off.
	debugEmails []string
}

type lookupConfig struct {
	workers int
}

type refreshConfig struct {
	workers int
}

type importerConfig struct {
	workers int
}

type iconsConfig struct {
	workers int
}

type enrichConfig struct {
	workers int
}

type mailRunnerConfig struct {
	// workers, when 0, makes this process queue emails without sending them.
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
		importer: importerConfig{
			// Imported feeds mostly wait on other sites, so more workers
			// overlap the waits (#448).
			workers: 6,
		},
		icons: iconsConfig{
			workers: 1,
		},
		enrich: enrichConfig{
			workers: 1,
		},
		mailRunner: mailRunnerConfig{
			workers: 1,
		},
		mail: cli.DefaultMail(),
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
	for _, e := range c.server.debugEmails {
		if addr, err := netmail.ParseAddress(e); err != nil || addr.Name != "" || addr.Address != e {
			errs = append(errs, fmt.Errorf("debug-emails: %q is not an email address", e))
		}
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
	if c.importer.workers < 0 || c.importer.workers > 16 {
		errs = append(errs, fmt.Errorf("invalid import workers %d: must be 0-16", c.importer.workers))
	}
	if c.icons.workers < 0 || c.icons.workers > 4 {
		errs = append(errs, fmt.Errorf("invalid icon workers %d: must be 0-4", c.icons.workers))
	}
	if c.enrich.workers < 0 || c.enrich.workers > 4 {
		errs = append(errs, fmt.Errorf("invalid enrich workers %d: must be 0-4", c.enrich.workers))
	}
	if c.mailRunner.workers < 0 || c.mailRunner.workers > 4 {
		errs = append(errs, fmt.Errorf("invalid mail workers %d: must be 0-4", c.mailRunner.workers))
	}
	errs = append(errs, c.mail.Validate(c.env)...)
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

	var debugEmails string
	fs.StringVar(&debugEmails, "debug-emails", "", "Accounts that may turn on debug mode, comma-separated (default: debug mode off)")

	fs.IntVar(&cfg.server.proxyHops, "proxy-hops", cfg.server.proxyHops, "Reverse proxies in front of the app that append to X-Forwarded-For (0-3; 0 ignores the header)")

	fs.StringVar(&cfg.db.dsn, "db-dsn", cfg.db.dsn, "PostgreSQL DSN")
	fs.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", cfg.db.maxOpenConns, "PostgreSQL max open connections")
	fs.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", cfg.db.maxIdleConns, "PostgreSQL max idle connections")
	fs.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", cfg.db.maxIdleTime, "PostgreSQL max idle time")

	fs.IntVar(&cfg.lookup.workers, "lookup-workers", cfg.lookup.workers, "Background lookup workers (0 disables them)")
	fs.IntVar(&cfg.refresh.workers, "refresh-workers", cfg.refresh.workers, "Background feed refresh workers (0 disables them)")
	fs.IntVar(&cfg.importer.workers, "import-workers", cfg.importer.workers, "Background OPML import workers (0-16; 0 disables them)")
	fs.IntVar(&cfg.icons.workers, "icon-workers", cfg.icons.workers, "Background site icon workers (0-4; 0 disables them)")
	fs.IntVar(&cfg.enrich.workers, "enrich-workers", cfg.enrich.workers, "Background article enrichment workers (0-4; 0 disables them)")
	fs.IntVar(&cfg.mailRunner.workers, "mail-workers", cfg.mailRunner.workers, "Background email workers (0-4; 0 queues emails without sending them)")

	cfg.mail.RegisterFlags(fs)

	if err := cli.SetFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}
	// fs.Parse reports its own errors to out.
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg.mail.SetBaseURLDefault(cfg.server.port)
	cfg.server.debugEmails = parseDebugEmails(debugEmails)

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}

	return cfg, nil
}

// parseDebugEmails splits -debug-emails on commas, trims and lowercases each
// entry, and drops empty ones.
func parseDebugEmails(s string) []string {
	var emails []string
	for e := range strings.SplitSeq(s, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			emails = append(emails, e)
		}
	}
	return emails
}
