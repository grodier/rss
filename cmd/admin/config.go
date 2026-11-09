package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/cli"
)

type config struct {
	env       string
	port      int
	baseURL   string
	proxyHops int
	// secretKey is the hex-encoded 32-byte key admins' TOTP secrets are
	// sealed with; "" in development makes serve generate one per run.
	secretKey string
	db        dbConfig
	// version, with -version, prints the build's version instead of serving.
	version bool
}

type dbConfig struct {
	dsn          string
	maxOpenConns int
	maxIdleConns int
	maxIdleTime  time.Duration
}

func defaultConfig() config {
	return config{
		env:  "development",
		port: 4001,
		db: dbConfig{
			// The admin shares the database with www, on a small plan: see
			// "A second service on the database's connections" in
			// docs/backlog.md.
			maxOpenConns: 3,
			maxIdleConns: 3,
			maxIdleTime:  15 * time.Minute,
		},
	}
}

func (c config) Validate() error {
	var errs []error
	if c.env != "development" && c.env != "production" {
		errs = append(errs, fmt.Errorf("invalid environment %q: must be development or production", c.env))
	}
	if c.port < 1 || c.port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port %d: must be 1-65535", c.port))
	}
	if c.proxyHops < 0 || c.proxyHops > 3 {
		errs = append(errs, errors.New("proxy hops must be between 0 and 3"))
	}
	if c.db.dsn == "" {
		errs = append(errs, errors.New("database DSN must not be empty"))
	}
	if c.db.maxOpenConns < 1 || c.db.maxOpenConns > 10 {
		errs = append(errs, fmt.Errorf("invalid database max open connections %d: must be 1-10", c.db.maxOpenConns))
	}
	switch {
	case c.secretKey != "":
		if _, err := parseSecretKey(c.secretKey); err != nil {
			errs = append(errs, err)
		}
	case c.env == "production":
		errs = append(errs, errors.New("-secret-key (RSS_SECRET_KEY) must be set in production"))
	}
	u, err := url.Parse(c.baseURL)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must be an absolute http or https URL", c.baseURL))
	case u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		errs = append(errs, fmt.Errorf("invalid base URL %q: must not have a path, query or fragment", c.baseURL))
	case c.env == "production" && u.Scheme != "https":
		errs = append(errs, errors.New("-base-url (RSS_BASE_URL) must be set to an https URL in production"))
	}
	return errors.Join(errs...)
}

// parseConfig reads the configuration of "admin serve" from args and, for
// flags not on the command line, from RSS_ environment variables looked up
// with getenv. Like the flag package, it writes its errors and the -help
// output to out, so the caller only has to exit.
func parseConfig(args []string, getenv func(string) string, out io.Writer) (config, error) {
	cfg := defaultConfig()

	fs := flag.NewFlagSet("admin serve", flag.ContinueOnError)
	fs.SetOutput(out)

	fs.StringVar(&cfg.env, "env", cfg.env, "Environment (development|production)")
	fs.IntVar(&cfg.port, "port", cfg.port, "Server port")
	fs.StringVar(&cfg.baseURL, "base-url", "", "The admin's own URL (default http://localhost:<port>)")
	fs.IntVar(&cfg.proxyHops, "proxy-hops", cfg.proxyHops, "Reverse proxies in front of the admin that append to X-Forwarded-For (0-3; 0 ignores the header)")
	fs.StringVar(&cfg.db.dsn, "db-dsn", cfg.db.dsn, "PostgreSQL DSN")
	fs.StringVar(&cfg.secretKey, "secret-key", "", secretKeyUsage)
	fs.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", cfg.db.maxOpenConns, "PostgreSQL max open connections (1-10)")
	fs.BoolVar(&cfg.version, "version", false, "Print the build's version and exit")

	if err := cli.SetFlagsFromEnv(fs, getenv); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}
	// fs.Parse reports its own errors to out.
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.version {
		return cfg, nil
	}
	if fs.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintln(out, err)
		return config{}, err
	}
	if cfg.baseURL == "" {
		cfg.baseURL = "http://localhost:" + strconv.Itoa(cfg.port)
	}
	cfg.baseURL = strings.TrimSuffix(cfg.baseURL, "/")
	cfg.db.maxIdleConns = cfg.db.maxOpenConns

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(out, err)
		return config{}, err
	}
	return cfg, nil
}

// secretKeyUsage is -secret-key's help, for both serve and enroll.
const secretKeyUsage = "Key that seals admins' TOTP secrets: 32 bytes, hex-encoded (openssl rand -hex 32)"

// parseSecretKey decodes a hex-encoded 32-byte -secret-key.
func parseSecretKey(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid -secret-key (RSS_SECRET_KEY): must be 32 bytes, hex-encoded (64 characters)")
	}
	return key, nil
}
