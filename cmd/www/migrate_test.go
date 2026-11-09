package main

import (
	"bytes"
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

func TestRunMigrateSeed(t *testing.T) {
	dsn := psqltest.NewSchemaDSN(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"migrate", "-seed", "-db-dsn", dsn}, noEnv, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// The seed list may be empty, so check the seed step ran, after migrating.
	out := stdout.String()
	migrated, seeded := strings.Index(out, "database migrated"), strings.Index(out, "seed list queued")
	if migrated < 0 || seeded < migrated {
		t.Errorf("want the migration logged, then the seed list queued; stdout:\n%s", out)
	}
}
