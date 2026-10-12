package main

import (
	"bytes"
	"encoding/json"
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
			newLogger(&out, tt.env).Info("hello", "key", "value")

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
