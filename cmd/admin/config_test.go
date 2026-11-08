package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

// noEnv is a getenv with no variables set, so the developer's environment
// can't leak into tests.
func noEnv(string) string { return "" }

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestParseConfig(t *testing.T) {
	const dsn = "postgres://u:p@localhost/db"
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
		check   func(t *testing.T, cfg config)
	}{
		{
			name: "defaults",
			env:  map[string]string{"RSS_DB_DSN": dsn},
			check: func(t *testing.T, cfg config) {
				if cfg.env != "development" || cfg.port != 4001 || cfg.baseURL != "http://localhost:4001" || cfg.proxyHops != 0 {
					t.Errorf("env, port, base URL, proxy hops = %q, %d, %q, %d; want development, 4001, http://localhost:4001, 0", cfg.env, cfg.port, cfg.baseURL, cfg.proxyHops)
				}
				if cfg.db.dsn != dsn || cfg.db.maxOpenConns != 3 || cfg.db.maxIdleConns != 3 {
					t.Errorf("db = %+v, want the DSN from RSS_DB_DSN and 3 connections", cfg.db)
				}
			},
		},
		{
			name: "flags",
			args: []string{"-db-dsn", dsn, "-port", "5000", "-proxy-hops", "1", "-db-max-open-conns", "2", "-base-url", "https://admin.example.com/"},
			check: func(t *testing.T, cfg config) {
				if cfg.port != 5000 || cfg.proxyHops != 1 || cfg.db.maxOpenConns != 2 || cfg.db.maxIdleConns != 2 {
					t.Errorf("port, proxy hops, conns = %d, %d, %d/%d; want 5000, 1, 2/2", cfg.port, cfg.proxyHops, cfg.db.maxOpenConns, cfg.db.maxIdleConns)
				}
				if cfg.baseURL != "https://admin.example.com" {
					t.Errorf("baseURL = %q, want the trailing slash trimmed", cfg.baseURL)
				}
			},
		},
		{
			name: "env variables",
			env:  map[string]string{"RSS_DB_DSN": dsn, "RSS_PORT": "5001", "RSS_PROXY_HOPS": "2", "RSS_DB_MAX_OPEN_CONNS": "4", "RSS_BASE_URL": "http://admin.localhost:5001"},
			check: func(t *testing.T, cfg config) {
				if cfg.port != 5001 || cfg.proxyHops != 2 || cfg.db.maxOpenConns != 4 || cfg.baseURL != "http://admin.localhost:5001" {
					t.Errorf("config = %+v", cfg)
				}
			},
		},
		{
			name: "base URL defaults to the port",
			env:  map[string]string{"RSS_DB_DSN": dsn, "RSS_PORT": "5002"},
			check: func(t *testing.T, cfg config) {
				if cfg.baseURL != "http://localhost:5002" {
					t.Errorf("baseURL = %q, want http://localhost:5002", cfg.baseURL)
				}
			},
		},
		{
			name: "flag wins over env",
			args: []string{"-port", "9001"},
			env:  map[string]string{"RSS_DB_DSN": dsn, "RSS_PORT": "9000"},
			check: func(t *testing.T, cfg config) {
				if cfg.port != 9001 {
					t.Errorf("port = %d, want 9001", cfg.port)
				}
			},
		},
		{
			name: "version",
			args: []string{"-version"},
			check: func(t *testing.T, cfg config) {
				if !cfg.version {
					t.Error("version = false, want true")
				}
			},
		},
		{name: "no dsn", wantErr: "database DSN must not be empty"},
		{name: "invalid env variable", env: map[string]string{"RSS_DB_DSN": dsn, "RSS_PORT": "abc"}, wantErr: `invalid value "abc" for RSS_PORT`},
		{name: "invalid environment", args: []string{"-env", "staging"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid environment "staging"`},
		{name: "port too high", args: []string{"-port", "70000"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "invalid port 70000"},
		{name: "proxy hops too high", args: []string{"-proxy-hops", "4"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "proxy hops must be between 0 and 3"},
		{name: "no connections", args: []string{"-db-max-open-conns", "0"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "invalid database max open connections 0"},
		{name: "relative base URL", args: []string{"-base-url", "admin.example.com"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "must be an absolute http or https URL"},
		{name: "base URL with a path", args: []string{"-base-url", "https://example.com/admin"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "must not have a path"},
		{name: "http base URL in production", args: []string{"-env", "production"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "must be set to an https URL in production"},
		{name: "extra argument", args: []string{"-db-dsn", dsn, "now"}, wantErr: `unexpected argument "now"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			cfg, err := parseConfig(tt.args, envFrom(tt.env), &out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(out.String(), tt.wantErr) {
					t.Fatalf("parseConfig() error = %v, output %q; want an error saying %q", err, out.String(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() error = %v; output:\n%s", err, out.String())
			}
			tt.check(t, cfg)
		})
	}
}

func TestParseConfigUsageNamesEnv(t *testing.T) {
	var out bytes.Buffer
	_, err := parseConfig([]string{"-help"}, noEnv, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseConfig(-help) error = %v, want flag.ErrHelp", err)
	}
	for _, name := range []string{"RSS_DB_DSN", "RSS_PORT", "RSS_BASE_URL", "RSS_PROXY_HOPS", "RSS_DB_MAX_OPEN_CONNS"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("-help output doesn't mention %s:\n%s", name, out.String())
		}
	}
}

func TestRun(t *testing.T) {
	const dsn = "postgres://u:p@localhost/db"
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "unknown command", args: []string{"frobnicate"}, wantCode: 2, wantStderr: `unknown command "frobnicate"`},
		{name: "unknown command prints usage", args: []string{"frobnicate"}, wantCode: 2, wantStderr: "admin [serve] [flags]"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "admin [serve] [flags]"},
		{name: "serve -help", args: []string{"serve", "-help"}, wantCode: 0, wantStderr: "-db-dsn"},
		{name: "flags without serve", args: []string{"-help"}, wantCode: 0, wantStderr: "-db-dsn"},
		{name: "version", args: []string{"serve", "-version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "invalid configuration", args: []string{"serve"}, wantCode: 2, wantStderr: "database DSN must not be empty"},
		// The admin has no login until #567, so it never runs in production.
		{
			name:       "serve refuses production",
			args:       []string{"serve", "-env", "production", "-base-url", "https://admin.example.com"},
			env:        map[string]string{"RSS_DB_DSN": dsn},
			wantCode:   2,
			wantStderr: "admin has no login yet (#567); it only runs locally",
		},
		{
			name:       "serve refuses production from the environment",
			env:        map[string]string{"RSS_DB_DSN": dsn, "RSS_ENV": "production", "RSS_BASE_URL": "https://admin.example.com"},
			wantCode:   2,
			wantStderr: "admin has no login yet (#567); it only runs locally",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, envFrom(tt.env), &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr.String())
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
