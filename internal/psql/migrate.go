package psql

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
)

// Migrate applies the goose migrations in fsys that db doesn't have yet, and
// logs each one it applies.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, logger *slog.Logger) error {
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("applied migration", "file", r.Source.Path, "duration", r.Duration)
	}
	logger.Info("database migrated", "applied", len(results))
	return nil
}
