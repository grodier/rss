package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := NewApplication(logger).Run(os.Args[1:]); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
