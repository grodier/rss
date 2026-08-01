package main

import (
	"context"
	"os"
)

func main() {
	ctx := context.Background()
	if err := NewApplication().Run(ctx); err != nil {
		os.Exit(1)
	}
}
