package main

import (
	"log/slog"
	"os"
)

// version identifies the build; the container image sets it to the commit SHA
// with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := NewApplication(os.Stdout).Run(os.Args[1:]); err != nil {
		// Run's logger may not exist yet (e.g. the flags were invalid).
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Error(err.Error())
		os.Exit(1)
	}
}
