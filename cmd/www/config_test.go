package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"maps"
	"slices"
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
		{"invite code 15 characters", func(c *config) { c.server.inviteCode = strings.Repeat("a", 15) }, true},
		{"invite code 16 characters", func(c *config) { c.server.inviteCode = strings.Repeat("a", 16) }, false},
		{"proxy hops max", func(c *config) { c.server.proxyHops = 3 }, false},
		{"proxy hops negative", func(c *config) { c.server.proxyHops = -1 }, true},
		{"proxy hops too high", func(c *config) { c.server.proxyHops = 4 }, true},
		{"lookup workers zero", func(c *config) { c.lookup.workers = 0 }, false},
		{"lookup workers max", func(c *config) { c.lookup.workers = 16 }, false},
		{"lookup workers negative", func(c *config) { c.lookup.workers = -1 }, true},
		{"lookup workers too high", func(c *config) { c.lookup.workers = 17 }, true},
		{"refresh workers zero", func(c *config) { c.refresh.workers = 0 }, false},
		{"refresh workers max", func(c *config) { c.refresh.workers = 16 }, false},
		{"refresh workers negative", func(c *config) { c.refresh.workers = -1 }, true},
		{"refresh workers too high", func(c *config) { c.refresh.workers = 17 }, true},
		{"import workers zero", func(c *config) { c.importer.workers = 0 }, false},
		{"import workers max", func(c *config) { c.importer.workers = 16 }, false},
		{"import workers negative", func(c *config) { c.importer.workers = -1 }, true},
		{"import workers too high", func(c *config) { c.importer.workers = 17 }, true},
		{"icon workers zero", func(c *config) { c.icons.workers = 0 }, false},
		{"icon workers max", func(c *config) { c.icons.workers = 4 }, false},
		{"icon workers negative", func(c *config) { c.icons.workers = -1 }, true},
		{"icon workers too high", func(c *config) { c.icons.workers = 5 }, true},
		{"enrich workers zero", func(c *config) { c.enrich.workers = 0 }, false},
		{"enrich workers max", func(c *config) { c.enrich.workers = 4 }, false},
		{"enrich workers negative", func(c *config) { c.enrich.workers = -1 }, true},
		{"enrich workers too high", func(c *config) { c.enrich.workers = 5 }, true},
		{"mail workers zero", func(c *config) { c.mailRunner.workers = 0 }, false},
		{"mail workers max", func(c *config) { c.mailRunner.workers = 4 }, false},
		{"mail workers negative", func(c *config) { c.mailRunner.workers = -1 }, true},
		{"mail workers too high", func(c *config) { c.mailRunner.workers = 5 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := defaultConfig()
			c.db.dsn = "postgres://u:p@localhost/db"
			c.mail.baseURL = "http://localhost:8080"
			tt.mutate(&c)

			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseConfigDebugEmails(t *testing.T) {
	dsn := []string{"-db-dsn", "postgres://x"}

	want := []string{"a@example.com", "b@example.com"}
	cfg, err := parseConfig(append(dsn, "-debug-emails", " A@example.com , ,b@example.com"), noEnv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.server.debugEmails, want) {
		t.Errorf("debugEmails = %q, want %q", cfg.server.debugEmails, want)
	}

	for _, bad := range []string{"not-an-email", "A <a@example.com>"} {
		_, err = parseConfig(append(dsn, "-debug-emails", bad), noEnv, io.Discard)
		msg := `debug-emails: "` + strings.ToLower(bad) + `" is not an email address`
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: parseConfig() error = %v, want %q", bad, err, msg)
		}
	}
}

// noEnv is a getenv with no variables set, so the developer's environment
// can't leak into tests.
func noEnv(string) string { return "" }

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestParseConfigEnv(t *testing.T) {
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
			name: "import workers default",
			env:  map[string]string{"RSS_DB_DSN": dsnEnv},
			check: func(t *testing.T, cfg config) {
				if cfg.importer.workers != 6 {
					t.Errorf("importer.workers = %d, want 6", cfg.importer.workers)
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
			cfg, err := parseConfig(tt.args, envFrom(tt.env), io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseConfig() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() error = %v", err)
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

func TestParseConfigUsageNamesEnv(t *testing.T) {
	var out bytes.Buffer
	_, err := parseConfig([]string{"-help"}, noEnv, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseConfig(-help) error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), "RSS_DB_DSN") {
		t.Errorf("-help output doesn't mention RSS_DB_DSN:\n%s", out.String())
	}
}

func TestParseConfigMail(t *testing.T) {
	dsn := []string{"-db-dsn", "postgres://x"}
	production := map[string]string{
		"RSS_ENV":           "production",
		"RSS_BASE_URL":      "https://rss.example.com",
		"RSS_SMTP_HOST":     "smtp.example.com",
		"RSS_MAIL_FROM":     "rss <hello@rss.example.com>",
		"RSS_SUPPORT_EMAIL": "help@example.com",
	}
	without := func(key string) map[string]string {
		env := maps.Clone(production)
		delete(env, key)
		return env
	}

	t.Run("development defaults", func(t *testing.T) {
		cfg, err := parseConfig(dsn, noEnv, io.Discard)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		want := mailConfig{baseURL: "http://localhost:8080", smtpPort: 587, from: "rss <hello@localhost>"}
		if cfg.mail != want {
			t.Errorf("mail = %+v, want %+v", cfg.mail, want)
		}
	})

	t.Run("base URL follows -port", func(t *testing.T) {
		cfg, err := parseConfig(append(dsn, "-port", "9000"), noEnv, io.Discard)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if want := "http://localhost:9000"; cfg.mail.baseURL != want {
			t.Errorf("baseURL = %q, want %q", cfg.mail.baseURL, want)
		}
	})

	t.Run("trailing slash trimmed", func(t *testing.T) {
		cfg, err := parseConfig(append(dsn, "-base-url", "https://rss.example.com/"), noEnv, io.Discard)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		if want := "https://rss.example.com"; cfg.mail.baseURL != want {
			t.Errorf("baseURL = %q, want %q", cfg.mail.baseURL, want)
		}
	})

	t.Run("complete production set", func(t *testing.T) {
		cfg, err := parseConfig(dsn, envFrom(production), io.Discard)
		if err != nil {
			t.Fatalf("parseConfig() error = %v", err)
		}
		want := mailConfig{
			baseURL:      "https://rss.example.com",
			supportEmail: "help@example.com",
			smtpHost:     "smtp.example.com",
			smtpPort:     587,
			from:         "rss <hello@rss.example.com>",
		}
		if cfg.mail != want {
			t.Errorf("mail = %+v, want %+v", cfg.mail, want)
		}
	})

	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"production without base URL", nil, without("RSS_BASE_URL"), "-base-url (RSS_BASE_URL) must be set to an https URL in production"},
		{"production http base URL", []string{"-base-url", "http://rss.example.com"}, production, "-base-url (RSS_BASE_URL) must be set to an https URL in production"},
		{"production without SMTP host", nil, without("RSS_SMTP_HOST"), "-smtp-host (RSS_SMTP_HOST) must be set in production"},
		{"production without mail from", nil, without("RSS_MAIL_FROM"), "-mail-from (RSS_MAIL_FROM) must be set in production"},
		{"production without support email", nil, without("RSS_SUPPORT_EMAIL"), "-support-email (RSS_SUPPORT_EMAIL) must be set in production"},
		{"base URL with path", []string{"-base-url", "https://example.com/rss"}, nil, `invalid base URL "https://example.com/rss": must not have a path, query or fragment`},
		{"base URL with query", []string{"-base-url", "https://example.com?a=1"}, nil, "must not have a path, query or fragment"},
		{"base URL with fragment", []string{"-base-url", "https://example.com#top"}, nil, "must not have a path, query or fragment"},
		{"relative base URL", []string{"-base-url", "example.com"}, nil, `invalid base URL "example.com": must be an absolute http or https URL`},
		{"ftp base URL", []string{"-base-url", "ftp://example.com"}, nil, "must be an absolute http or https URL"},
		{"invalid mail from", []string{"-mail-from", "not an address"}, nil, `invalid mail from "not an address"`},
		{"invalid support email", []string{"-support-email", "help"}, nil, `invalid support email "help"`},
		{"SMTP port zero", []string{"-smtp-port", "0"}, nil, "invalid SMTP port 0: must be 1-65535"},
		{"SMTP port too high", []string{"-smtp-port", "65536"}, nil, "invalid SMTP port 65536: must be 1-65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig(append(dsn, tt.args...), envFrom(tt.env), io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseConfig() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}

	t.Run("production without any mail settings", func(t *testing.T) {
		_, err := parseConfig(append(dsn, "-env", "production"), noEnv, io.Discard)
		for _, want := range []string{"-base-url", "-smtp-host", "-mail-from", "-support-email"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("parseConfig() error = %v, want it to name %s", err, want)
			}
		}
	})
}
