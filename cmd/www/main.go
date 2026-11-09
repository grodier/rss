package main

import (
	"errors"
	"flag"
	"io"
	"os"

	"github.com/grodier/rss/internal/cli"
)

// version identifies the build; the container image sets it to the commit SHA
// with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run runs the command in args and returns the process's exit code: 0 on
// success or -help, 1 when the command fails, 2 for invalid configuration.
// "www migrate [flags]" applies the migrations; "www seed [flags]" queues
// lookups of the seed list's sites; "www [flags]" serves the app.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "migrate":
			return runMigrate(args[1:], getenv, stdout, stderr)
		case "seed":
			return runSeed(args[1:], getenv, stdout, stderr)
		}
	}

	cfg, err := parseConfig(args, getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}

	logger := cli.NewLogger(stdout, cfg.env)
	if err := NewApplication(cfg, logger).Run(); err != nil {
		logger.Error(err.Error())
		return 1
	}
	return 0
}
