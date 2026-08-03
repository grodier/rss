package main

import (
	"context"
	"log/slog"
	"os"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := NewApplication(logger).Run(ctx); err != nil {
		os.Exit(1)
	}
}
