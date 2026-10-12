package psql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"

	"github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// Fingerprint returns the hex SHA-256 of the names and contents of the *.sql
// files in fsys, in name order.
func Fingerprint(fsys fs.FS) (string, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return "", err
	}
	slices.Sort(names)

	h := sha256.New()
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return "", err
		}
		// The NUL separators and the length keep the encoding unambiguous: a
		// name can't contain NUL, and the length marks where the contents end.
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Migrate applies the goose migrations in fsys to db. While pre-alpha, it first
// checks that the migrations haven't changed since they were applied. If they
// have, it wipes the current schema and starts over only when allowResetTo is
// the new fingerprint, and fails otherwise.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, allowResetTo string, logger *slog.Logger) error {
	fp, err := Fingerprint(fsys)
	if err != nil {
		return fmt.Errorf("fingerprint migrations: %w", err)
	}

	var schema sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return fmt.Errorf("read current schema: %w", err)
	}
	if !schema.Valid {
		return errors.New("no schema in search_path exists")
	}
	// Qualified, so a schema_fingerprint table further down the search_path
	// (as in the tests, which put public after their own schema) is never read.
	table := pq.QuoteIdentifier(schema.String) + ".schema_fingerprint"

	stored, err := storedFingerprint(ctx, db, table)
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}

	if version > 0 && stored != fp {
		if allowResetTo != fp {
			return fmt.Errorf("migrations have changed since this database was migrated (new fingerprint %s): locally, rebuild it with make db/reset; in production, run the \"Reset production database\" workflow", fp)
		}
		logger.Warn("migrations changed: wiping the database schema", "old", stored, "new", fp)
		if err := resetSchema(ctx, db, schema.String); err != nil {
			return err
		}
		// A provider checks for its version table only once, and the reset
		// dropped it, so start over with a new one.
		provider, err = goose.NewProvider(goose.DialectPostgres, db, fsys)
		if err != nil {
			return fmt.Errorf("create migration provider: %w", err)
		}
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("applied migration", "file", r.Source.Path, "duration", r.Duration)
	}

	if err := storeFingerprint(ctx, db, table, fp); err != nil {
		return err
	}
	logger.Info("database migrated", "applied", len(results), "fingerprint", fp)
	return nil
}

// storedFingerprint returns the fingerprint stored in table, or "" if the
// table doesn't exist or is empty.
func storedFingerprint(ctx context.Context, db *sql.DB, table string) (string, error) {
	var fp string
	err := db.QueryRowContext(ctx, `SELECT fingerprint FROM `+table).Scan(&fp)
	var pqErr *pq.Error
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case errors.As(err, &pqErr) && pqErr.Code == "42P01": // undefined_table
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read migrations fingerprint: %w", err)
	}
	return fp, nil
}

// resetSchema drops schema with everything in it and creates it again, empty.
func resetSchema(ctx context.Context, db *sql.DB, schema string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reset schema: %w", err)
	}
	defer tx.Rollback()

	name := pq.QuoteIdentifier(schema)
	if _, err := tx.ExecContext(ctx, `DROP SCHEMA `+name+` CASCADE`); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA `+name); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return tx.Commit()
}

// storeFingerprint replaces the fingerprint stored in table with fp, creating
// the table if needed. The table belongs to the pre-alpha migration check, not
// to the app, so it isn't in the migrations.
func storeFingerprint(ctx context.Context, db *sql.DB, table, fp string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store migrations fingerprint: %w", err)
	}
	defer tx.Rollback()

	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + table + ` (fingerprint text NOT NULL)`,
		`DELETE FROM ` + table,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("store migrations fingerprint: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (fingerprint) VALUES ($1)`, fp); err != nil {
		return fmt.Errorf("store migrations fingerprint: %w", err)
	}
	return tx.Commit()
}
