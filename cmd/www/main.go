package main

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
)

// version identifies the build; the container image sets it to the commit SHA
// with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		os.Exit(2)
	}

	logger := newLogger(os.Stdout, cfg.env)
	if err := NewApplication(cfg, logger).Run(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

// newLogger logs to out as JSON in production, where the host's log search can
// filter by field, and as text otherwise, for reading in a terminal.
func newLogger(out io.Writer, env string) *slog.Logger {
	if env == "production" {
		return slog.New(slog.NewJSONHandler(out, nil))
	}
	return slog.New(slog.NewTextHandler(out, nil))
}
