package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/mail"
)

type config struct {
	env        string
	server     serverConfig
	db         dbConfig
	lookup     lookupConfig
	refresh    refreshConfig
	mailRunner mailRunnerConfig
	mail       mailConfig
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

type mailRunnerConfig struct {
	// workers, when 0, makes this process queue emails without sending them.
	workers int
}

// mailConfig is how the app sends email, and what emails link to.
type mailConfig struct {
	// baseURL is the app's public URL, without a trailing slash. Links in
	// emails are built from it, never from a request's Host header.
	baseURL string
	// supportEmail is where people can get help, and the Reply-To of emails.
	supportEmail string
	// smtpHost, when empty, makes the app log emails instead of sending them.
	smtpHost     string
	smtpPort     int
	smtpUsername string
	smtpPassword string
	from         string
}

// defaultMailFrom is -mail-from's default, which production must override.
const defaultMailFrom = "rss <hello@localhost>"

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
		mailRunner: mailRunnerConfig{
			workers: 1,
		},
		mail: mailConfig{
			smtpPort: 587,
			from:     defaultMailFrom,
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
	if c.mailRunner.workers < 0 || c.mailRunner.workers > 4 {
		errs = append(errs, fmt.Errorf("invalid mail workers %d: must be 0-4", c.mailRunner.workers))
	}
	errs = append(errs, c.mail.validate(c.env)...)
	return errors.Join(errs...)
}

// validate returns an error for each invalid mail setting, and in production
// for each missing one: emails carry one-time links, which must never end up
// in the logs there.
func (c mailConfig) validate(env string) []error {
	var errs []error
	u, err := url.Parse(c.baseURL)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must be an absolute http or https URL", c.baseURL))
	case u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must not have a path, query or fragment", c.baseURL))
	case env == "production" && u.Scheme != "https":
		errs = append(errs, errors.New("-base-url (RSS_BASE_URL) must be set to an https URL in production"))
	}
	if _, err := netmail.ParseAddress(c.from); err != nil {
		errs = append(errs, fmt.Errorf("invalid mail from %q: %v", c.from, err))
	}
	if c.supportEmail != "" {
		if _, err := netmail.ParseAddress(c.supportEmail); err != nil {
			errs = append(errs, fmt.Errorf("invalid support email %q: %v", c.supportEmail, err))
		}
	}
	if c.smtpPort < 1 || c.smtpPort > 65535 {
		errs = append(errs, fmt.Errorf("invalid SMTP port %d: must be 1-65535", c.smtpPort))
	}
	if env == "production" {
		if c.smtpHost == "" {
			errs = append(errs, errors.New("-smtp-host (RSS_SMTP_HOST) must be set in production"))
		}
		if c.from == defaultMailFrom {
			errs = append(errs, errors.New("-mail-from (RSS_MAIL_FROM) must be set in production"))
		}
		if c.supportEmail == "" {
			errs = append(errs, errors.New("-support-email (RSS_SUPPORT_EMAIL) must be set in production"))
		}
	}
	return errs
}

// registerMailFlags registers the mail flags in fs, writing to c. -base-url
// has no default here: call c.setBaseURLDefault after fs.Parse.
func registerMailFlags(fs *flag.FlagSet, c *mailConfig) {
	fs.StringVar(&c.baseURL, "base-url", "", "Public URL of the app, for links in emails (default http://localhost:<port>; required in production)")
	fs.StringVar(&c.supportEmail, "support-email", c.supportEmail, "Where people can get help: shown on pages and used as Reply-To (required in production)")
	fs.StringVar(&c.smtpHost, "smtp-host", c.smtpHost, "SMTP server (default: emails are logged, not sent; required in production)")
	fs.IntVar(&c.smtpPort, "smtp-port", c.smtpPort, "SMTP port: 465 for implicit TLS, otherwise STARTTLS")
	fs.StringVar(&c.smtpUsername, "smtp-username", c.smtpUsername, "SMTP username")
	fs.StringVar(&c.smtpPassword, "smtp-password", c.smtpPassword, "SMTP password")
	fs.StringVar(&c.from, "mail-from", c.from, "From address of every email (required in production)")
}

// setBaseURLDefault defaults the base URL to the app on localhost at port,
// and trims a trailing slash.
func (c *mailConfig) setBaseURLDefault(port int) {
	if c.baseURL == "" {
		c.baseURL = "http://localhost:" + strconv.Itoa(port)
	}
	c.baseURL = strings.TrimSuffix(c.baseURL, "/")
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
	fs.IntVar(&cfg.mailRunner.workers, "mail-workers", cfg.mailRunner.workers, "Background email workers (0-4; 0 queues emails without sending them)")

	registerMailFlags(fs, &cfg.mail)

	if err := setFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}
	// fs.Parse reports its own errors to out.
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg.mail.setBaseURLDefault(cfg.server.port)

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
