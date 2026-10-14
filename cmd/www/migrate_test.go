package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestRunMigratePrintFingerprint(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"migrate", "-print-fingerprint"}, noEnv, nil, &out, io.Discard); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Errorf("printed %q, want a 64-character hex fingerprint", got)
	}
}

func TestRunMigrateRequiresDSN(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"migrate"}, noEnv, nil, io.Discard, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if want := "database DSN must not be empty"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}
