// Package cli holds what the commands (cmd/www, cmd/admin) share: flags
// settable from RSS_ environment variables, and the logger.
package cli

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// SetFlagsFromEnv sets each flag in fs from its RSS_ environment variable, if
// set and not empty, and appends "(env RSS_…)" to its usage. Call it before
// fs.Parse so flags on the command line win.
func SetFlagsFromEnv(fs *flag.FlagSet, getenv func(string) string) error {
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

// NewLogger logs to out as JSON in production, where the host's log search
// can filter by field, and as text otherwise, for reading in a terminal.
func NewLogger(out io.Writer, env string) *slog.Logger {
	if env == "production" {
		return slog.New(slog.NewJSONHandler(out, nil))
	}
	return slog.New(slog.NewTextHandler(out, nil))
}
