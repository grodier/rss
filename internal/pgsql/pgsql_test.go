package pgsql

import (
	"testing"
	"time"
)

func TestOpenDB_InvalidDSN(t *testing.T) {
	_, err := OpenDB("postgres://127.0.0.1:1/nonexistent?connect_timeout=1", 5, 5, time.Minute)
	if err == nil {
		t.Fatal("expected error for invalid DSN, got nil")
	}
}
