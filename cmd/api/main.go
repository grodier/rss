package main

import (
	"context"
	"log/slog"
	"os"
)

const version = "0.1.0"

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := NewApplication(logger).Run(ctx, os.Args[1:]); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
