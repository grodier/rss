package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

const unreachableDSN = "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"

func TestRunReturnsErrorWhenDBUnreachable(t *testing.T) {
	app := NewApplication(io.Discard)

	err := app.Run([]string{"-db-dsn", unreachableDSN})
	if err == nil {
		t.Fatal("expected an error when the database is unreachable, got nil")
	}
}

func TestRunLogsJSONInProduction(t *testing.T) {
	tests := []struct {
		env      string
		wantJSON bool
	}{
		{"production", true},
		{"development", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			var out bytes.Buffer
			if err := NewApplication(&out).Run([]string{"-env", tt.env, "-db-dsn", unreachableDSN}); err == nil {
				t.Fatal("expected an error when the database is unreachable, got nil")
			}

			var lines, jsonLines int
			sc := bufio.NewScanner(&out)
			for sc.Scan() {
				lines++
				if json.Valid(sc.Bytes()) {
					jsonLines++
				}
			}
			if lines == 0 {
				t.Fatal("Run wrote no log lines")
			}
			if tt.wantJSON && jsonLines == 0 {
				t.Errorf("no JSON log lines in output:\n%s", out.String())
			}
			if !tt.wantJSON && jsonLines > 0 {
				t.Errorf("got JSON log lines in %s:\n%s", tt.env, out.String())
			}
		})
	}
}
