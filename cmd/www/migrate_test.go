package main

import (
	"bytes"
	"database/sql"
	"io"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
)

func TestRunMigrateRequiresDSN(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"migrate"}, noEnv, io.Discard, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if want := "database DSN must not be empty"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestRunMigrate(t *testing.T) {
	dsn := psqltest.NewSchemaDSN(t)
	migrate := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run([]string{"migrate", "-db-dsn", dsn}, noEnv, &stdout, &stderr); code != 0 {
			t.Fatalf("run() = %d, want 0; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	if out := migrate(); !strings.Contains(out, "database migrated") || !strings.Contains(out, "site keys: re-keyed") {
		t.Errorf("first migrate: want the migration and the re-key logged; stdout:\n%s", out)
	}
	if out := migrate(); strings.Contains(out, "re-keyed") {
		t.Errorf("second migrate: re-keyed again; stdout:\n%s", out)
	}
}

func TestRunRekey(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"rekey"}, noEnv, io.Discard, &stderr); code != 2 {
		t.Fatalf("without a DSN: run() = %d, want 2", code)
	}

	dsn := psqltest.NewSchemaDSN(t)
	if code := run([]string{"migrate", "-db-dsn", dsn}, noEnv, io.Discard, io.Discard); code != 0 {
		t.Fatalf("migrate: run() = %d, want 0", code)
	}
	var stdout bytes.Buffer
	if code := run([]string{"rekey", "-db-dsn", dsn}, noEnv, &stdout, &stderr); code != 0 {
		t.Fatalf("rekey: run() = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if out := stdout.String(); strings.Contains(out, "database migrated") || strings.Contains(out, "re-keyed") {
		t.Errorf("rekey after migrate: want neither migrations nor a re-key; stdout:\n%s", out)
	}

	// An older stored version: rekey runs the re-key.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE site_key_rules SET version = 0`); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := run([]string{"rekey", "-db-dsn", dsn}, noEnv, &stdout, &stderr); code != 0 {
		t.Fatalf("rekey: run() = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "site keys: re-keyed") {
		t.Errorf("rekey of an older version: want the re-key logged; stdout:\n%s", out)
	}
}
