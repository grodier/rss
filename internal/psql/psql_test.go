package psql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
)

// Requires a database; see psqltest.DSN.
func TestOpenDBTurnsOffJIT(t *testing.T) {
	t.Parallel()
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

func TestPgErrorCodes(t *testing.T) {
	t.Parallel()
	wrap := func(code pqerror.Code) error {
		return fmt.Errorf("insert: %w", &pq.Error{Code: code})
	}
	tests := []struct {
		name           string
		err            error
		wantUnique     bool
		wantForeignKey bool
	}{
		{"unique_violation", wrap("23505"), true, false},
		{"foreign_key_violation", wrap("23503"), false, true},
		{"check_violation", wrap("23514"), false, false},
		{"not a pq error", errors.New("23505"), false, false},
		{"nil", nil, false, false},
	}
	for _, tt := range tests {
		if got := isUniqueViolation(tt.err); got != tt.wantUnique {
			t.Errorf("%s: isUniqueViolation = %v, want %v", tt.name, got, tt.wantUnique)
		}
		if got := isForeignKeyViolation(tt.err); got != tt.wantForeignKey {
			t.Errorf("%s: isForeignKeyViolation = %v, want %v", tt.name, got, tt.wantForeignKey)
		}
	}
}
