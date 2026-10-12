package main

import (
	"log/slog"
	"os"
)

// version identifies the build; the container image sets it to the commit SHA
// with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := NewApplication(logger).Run(os.Args[1:]); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
