package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/admin"
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
				if cfg.appURL != "http://localhost:8080" || cfg.supportEmail != "" || cfg.appInternalURL != "" {
					t.Errorf("app URL, support email, app internal URL = %q, %q, %q; want http://localhost:8080, none and none", cfg.appURL, cfg.supportEmail, cfg.appInternalURL)
				}
				if want := (admin.MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}); cfg.mailLimits != want {
					t.Errorf("mail limits = %+v, want %+v", cfg.mailLimits, want)
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
			env: map[string]string{"RSS_DB_DSN": dsn, "RSS_PORT": "5001", "RSS_PROXY_HOPS": "2", "RSS_DB_MAX_OPEN_CONNS": "4", "RSS_BASE_URL": "http://admin.localhost:5001",
				"RSS_APP_URL": "http://app.localhost:5000/", "RSS_SUPPORT_EMAIL": "help@example.com", "RSS_APP_INTERNAL_URL": "http://rss-abcd:8080/",
				"RSS_MAIL_DAILY_LIMIT": "5", "RSS_MAIL_MONTHLY_LIMIT": "50", "RSS_MAIL_RESERVE": "1"},
			check: func(t *testing.T, cfg config) {
				if cfg.port != 5001 || cfg.proxyHops != 2 || cfg.db.maxOpenConns != 4 || cfg.baseURL != "http://admin.localhost:5001" {
					t.Errorf("config = %+v", cfg)
				}
				if cfg.appURL != "http://app.localhost:5000" || cfg.supportEmail != "help@example.com" {
					t.Errorf("app URL, support email = %q, %q; want the trailing slash trimmed, and help@example.com", cfg.appURL, cfg.supportEmail)
				}
				if cfg.appInternalURL != "http://rss-abcd:8080" {
					t.Errorf("app internal URL = %q, want the trailing slash trimmed", cfg.appInternalURL)
				}
				if want := (admin.MailLimits{Daily: 5, Monthly: 50, Reserve: 1}); cfg.mailLimits != want {
					t.Errorf("mail limits = %+v, want %+v", cfg.mailLimits, want)
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
			name: "production",
			env:  map[string]string{"RSS_DB_DSN": dsn, "RSS_ENV": "production", "RSS_BASE_URL": "https://admin.example.com", "RSS_APP_URL": "https://example.com", "RSS_SECRET_KEY": strings.Repeat("ab", 32)},
			check: func(t *testing.T, cfg config) {
				if cfg.env != "production" || cfg.secretKey != strings.Repeat("ab", 32) {
					t.Errorf("env, secret key = %q, %q; want production and the key", cfg.env, cfg.secretKey)
				}
			},
		},
		{
			name: "render settings",
			env:  map[string]string{"RSS_DB_DSN": dsn, "RSS_RENDER_API_KEY": "rnd_test", "RSS_RENDER_SERVICE_ID": "srv-abc123"},
			check: func(t *testing.T, cfg config) {
				if cfg.renderAPIKey != "rnd_test" || cfg.renderServiceID != "srv-abc123" {
					t.Errorf("render settings = %q, %q; want rnd_test and srv-abc123", cfg.renderAPIKey, cfg.renderServiceID)
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
		{name: "http app URL in production", env: map[string]string{"RSS_DB_DSN": dsn, "RSS_ENV": "production", "RSS_BASE_URL": "https://admin.example.com", "RSS_SECRET_KEY": strings.Repeat("ab", 32)}, wantErr: "-app-url (RSS_APP_URL) must be an https URL in production"},
		{name: "app URL with a path", args: []string{"-app-url", "https://example.com/app"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app URL "https://example.com/app"`},
		{name: "app internal URL with a path", args: []string{"-app-internal-url", "http://rss-abcd:8080/healthcheck"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app internal URL "http://rss-abcd:8080/healthcheck"`},
		{name: "app internal URL with an empty fragment", args: []string{"-app-internal-url", "http://rss-abcd:8080#"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app internal URL "http://rss-abcd:8080#"`},
		{name: "app internal URL with an empty query", args: []string{"-app-internal-url", "http://rss-abcd:8080?"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app internal URL "http://rss-abcd:8080?"`},
		{name: "app internal URL with a user", args: []string{"-app-internal-url", "http://u:p@rss-abcd:8080"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app internal URL "http://u:p@rss-abcd:8080"`},
		{name: "app internal URL without a scheme", args: []string{"-app-internal-url", "rss-abcd:8080"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid app internal URL "rss-abcd:8080"`},
		{name: "Render service ID with a path", args: []string{"-render-service-id", "srv-abc/../x"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid Render service ID "srv-abc/../x"`},
		{name: "Render service ID without its prefix", args: []string{"-render-service-id", "abc123"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid Render service ID "abc123"`},
		{name: "invalid support email", args: []string{"-support-email", "nope"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: `invalid support email "nope"`},
		{name: "reserve as big as the day", args: []string{"-mail-daily-limit", "10", "-mail-reserve", "10"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "invalid mail limits"},
		{name: "month under the day", args: []string{"-mail-monthly-limit", "50"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "invalid mail limits"},
		{name: "secret key not hex", args: []string{"-secret-key", "nope"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "invalid -secret-key (RSS_SECRET_KEY)"},
		{name: "secret key too short", args: []string{"-secret-key", "abcd"}, env: map[string]string{"RSS_DB_DSN": dsn}, wantErr: "must be 32 bytes, hex-encoded"},
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
	for _, name := range []string{"RSS_DB_DSN", "RSS_PORT", "RSS_BASE_URL", "RSS_PROXY_HOPS", "RSS_DB_MAX_OPEN_CONNS", "RSS_SECRET_KEY",
		"RSS_APP_URL", "RSS_SUPPORT_EMAIL", "RSS_MAIL_DAILY_LIMIT", "RSS_MAIL_MONTHLY_LIMIT", "RSS_MAIL_RESERVE"} {
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
		{name: "enroll -help", args: []string{"enroll", "-help"}, wantCode: 0, wantStderr: "Usage: admin enroll -email <email>"},
		{
			name:       "production needs a secret key",
			args:       []string{"serve", "-env", "production", "-base-url", "https://admin.example.com"},
			env:        map[string]string{"RSS_DB_DSN": dsn},
			wantCode:   2,
			wantStderr: "-secret-key (RSS_SECRET_KEY) must be set in production",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, envFrom(tt.env), nil, &stdout, &stderr)
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
