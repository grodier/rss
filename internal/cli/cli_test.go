package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestNewLoggerJSONInProduction(t *testing.T) {
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
			NewLogger(&out, tt.env).Info("hello", "key", "value")

			line := bytes.TrimSpace(out.Bytes())
			if len(line) == 0 {
				t.Fatal("logger wrote nothing")
			}
			if got := json.Valid(line); got != tt.wantJSON {
				t.Errorf("JSON = %v, want %v; output: %s", got, tt.wantJSON, line)
			}
		})
	}
}

func TestSetFlagsFromEnv(t *testing.T) {
	newFlags := func() (*flag.FlagSet, *string, *int) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dsn := fs.String("db-dsn", "", "PostgreSQL DSN")
		port := fs.Int("port", 4001, "Server port")
		return fs, dsn, port
	}
	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}

	fs, dsn, port := newFlags()
	if err := SetFlagsFromEnv(fs, env(map[string]string{"RSS_DB_DSN": "postgres://x", "RSS_PORT": ""})); err != nil {
		t.Fatal(err)
	}
	if *dsn != "postgres://x" {
		t.Errorf("-db-dsn = %q, want it from RSS_DB_DSN", *dsn)
	}
	if *port != 4001 {
		t.Errorf("-port = %d, want the default: an empty variable counts as unset", *port)
	}
	if u := fs.Lookup("db-dsn").Usage; !strings.HasSuffix(u, "(env RSS_DB_DSN)") {
		t.Errorf("usage = %q, want it to name RSS_DB_DSN", u)
	}

	// A flag on the command line wins.
	fs, _, port = newFlags()
	if err := SetFlagsFromEnv(fs, env(map[string]string{"RSS_PORT": "9000"})); err != nil {
		t.Fatal(err)
	}
	if err := fs.Parse([]string{"-port", "9001"}); err != nil {
		t.Fatal(err)
	}
	if *port != 9001 {
		t.Errorf("-port = %d, want the command line's 9001", *port)
	}

	fs, _, _ = newFlags()
	err := SetFlagsFromEnv(fs, env(map[string]string{"RSS_PORT": "abc"}))
	if err == nil || !strings.Contains(err.Error(), `invalid value "abc" for RSS_PORT`) {
		t.Errorf("error = %v, want the invalid RSS_PORT named", err)
	}
}
