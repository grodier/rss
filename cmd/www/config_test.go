package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *config)
		wantErr bool
	}{
		{"valid", func(c *config) {}, false},
		{"invalid env", func(c *config) { c.env = "staging" }, true},
		{"port zero", func(c *config) { c.server.port = 0 }, true},
		{"port too high", func(c *config) { c.server.port = 70000 }, true},
		{"empty dsn", func(c *config) { c.db.dsn = "" }, true},
		{"lookup workers zero", func(c *config) { c.lookup.workers = 0 }, false},
		{"lookup workers max", func(c *config) { c.lookup.workers = 16 }, false},
		{"lookup workers negative", func(c *config) { c.lookup.workers = -1 }, true},
		{"lookup workers too high", func(c *config) { c.lookup.workers = 17 }, true},
		{"refresh workers zero", func(c *config) { c.refresh.workers = 0 }, false},
		{"refresh workers max", func(c *config) { c.refresh.workers = 16 }, false},
		{"refresh workers negative", func(c *config) { c.refresh.workers = -1 }, true},
		{"refresh workers too high", func(c *config) { c.refresh.workers = 17 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := defaultConfig()
			c.db.dsn = "postgres://u:p@localhost/db"
			tt.mutate(&c)

			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseConfigsRefreshWorkers(t *testing.T) {
	dsn := []string{"-db-dsn", "postgres://u:p@localhost/db"}

	t.Run("default", func(t *testing.T) {
		cfg, err := NewApplication(io.Discard).ParseConfigs(dsn, noEnv)
		if err != nil {
			t.Fatalf("ParseConfigs() error = %v", err)
		}
		if cfg.refresh.workers != 2 {
			t.Errorf("refresh.workers = %d, want 2", cfg.refresh.workers)
		}
	})

	for _, n := range []string{"0", "16"} {
		t.Run("valid "+n, func(t *testing.T) {
			if _, err := NewApplication(io.Discard).ParseConfigs(append(dsn, "-refresh-workers", n), noEnv); err != nil {
				t.Fatalf("ParseConfigs() error = %v, want nil", err)
			}
		})
	}

	for _, n := range []string{"-1", "17"} {
		t.Run("invalid "+n, func(t *testing.T) {
			_, err := NewApplication(io.Discard).ParseConfigs(append(dsn, "-refresh-workers", n), noEnv)
			want := "invalid refresh workers " + n + ": must be 0-16"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("ParseConfigs() error = %v, want %q", err, want)
			}
		})
	}
}

func TestParseConfigsSignup(t *testing.T) {
	dsn := []string{"-db-dsn", "postgres://x"}
	cfg, err := NewApplication(io.Discard).ParseConfigs(dsn, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.server.signup {
		t.Error("signup defaults to true, want false")
	}
	cfg, err = NewApplication(io.Discard).ParseConfigs(append(dsn, "-signup"), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.server.signup {
		t.Error("-signup did not set server.signup")
	}
}

// noEnv is a getenv with no variables set, so the developer's environment
// can't leak into tests.
func noEnv(string) string { return "" }

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestParseConfigsEnv(t *testing.T) {
	dsnEnv := "postgres://u:p@localhost/db"

	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		wantPort int
		wantErr  string
		check    func(t *testing.T, cfg config)
	}{
		{
			name: "dsn alone",
			env:  map[string]string{"RSS_DB_DSN": dsnEnv},
			check: func(t *testing.T, cfg config) {
				if cfg.db.dsn != dsnEnv {
					t.Errorf("db.dsn = %q, want %q", cfg.db.dsn, dsnEnv)
				}
			},
		},
		{
			name:     "port from env",
			env:      map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_PORT": "9000"},
			wantPort: 9000,
		},
		{
			name:     "flag wins over env",
			args:     []string{"-port", "9001"},
			env:      map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_PORT": "9000"},
			wantPort: 9001,
		},
		{
			name: "signup from env",
			env:  map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_SIGNUP": "true"},
			check: func(t *testing.T, cfg config) {
				if !cfg.server.signup {
					t.Error("RSS_SIGNUP=true did not set server.signup")
				}
			},
		},
		{
			name:     "empty counts as unset",
			env:      map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_PORT": ""},
			wantPort: 8080,
		},
		{
			name:    "invalid value",
			env:     map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_PORT": "abc"},
			wantErr: `invalid value "abc" for RSS_PORT`,
		},
		{
			name:     "unknown variable ignored",
			env:      map[string]string{"RSS_DB_DSN": dsnEnv, "RSS_UNKNOWN": "1"},
			wantPort: 8080,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewApplication(io.Discard).ParseConfigs(tt.args, envFrom(tt.env))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseConfigs() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConfigs() error = %v", err)
			}
			if tt.wantPort != 0 && cfg.server.port != tt.wantPort {
				t.Errorf("server.port = %d, want %d", cfg.server.port, tt.wantPort)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestParseConfigsUsageNamesEnv(t *testing.T) {
	var out bytes.Buffer
	_, err := NewApplication(&out).ParseConfigs([]string{"-help"}, noEnv)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("ParseConfigs(-help) error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), "RSS_DB_DSN") {
		t.Errorf("-help output doesn't mention RSS_DB_DSN:\n%s", out.String())
	}
}
