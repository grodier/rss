package main

import (
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
		cfg, err := NewApplication(nil).ParseConfigs(dsn)
		if err != nil {
			t.Fatalf("ParseConfigs() error = %v", err)
		}
		if cfg.refresh.workers != 2 {
			t.Errorf("refresh.workers = %d, want 2", cfg.refresh.workers)
		}
	})

	for _, n := range []string{"0", "16"} {
		t.Run("valid "+n, func(t *testing.T) {
			if _, err := NewApplication(nil).ParseConfigs(append(dsn, "-refresh-workers", n)); err != nil {
				t.Fatalf("ParseConfigs() error = %v, want nil", err)
			}
		})
	}

	for _, n := range []string{"-1", "17"} {
		t.Run("invalid "+n, func(t *testing.T) {
			_, err := NewApplication(nil).ParseConfigs(append(dsn, "-refresh-workers", n))
			want := "invalid refresh workers " + n + ": must be 0-16"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("ParseConfigs() error = %v, want %q", err, want)
			}
		})
	}
}
