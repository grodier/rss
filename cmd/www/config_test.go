package main

import "testing"

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
