package psql

import (
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
)

// Requires a database; see psqltest.DSN.
func TestOpenDBTurnsOffJIT(t *testing.T) {
	db, err := OpenDB(psqltest.DSN(t), 1, 1, time.Minute)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var jit string
	if err := db.QueryRowContext(t.Context(), `SHOW jit`).Scan(&jit); err != nil {
		t.Fatalf("SHOW jit: %v", err)
	}
	if jit != "off" {
		t.Errorf("jit = %q, want off", jit)
	}
}
