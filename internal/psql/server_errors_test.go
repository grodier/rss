package psql

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/grodier/rss/internal/rss"
)

// newServerErrorRepo returns a repository on a freshly migrated schema of
// its own: Trim works on the whole table.
func newServerErrorRepo(t *testing.T) (*ServerErrorRepository, *sql.DB) {
	t.Helper()
	db := newMigrateDB(t)
	mustMigrate(t, db)
	return NewServerErrorRepository(db), db
}

// serverErrorMessages returns the stored messages, newest first.
func serverErrorMessages(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT message FROM server_errors ORDER BY occurred_at DESC, id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestServerErrorRecord(t *testing.T) {
	repo, db := newServerErrorRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

	in := rss.ServerError{OccurredAt: at, Instance: "web-1", Route: "/feeds/{id}", Method: "GET", Message: "boom", Panic: true}
	if err := repo.Record(ctx, in); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var got rss.ServerError
	err := db.QueryRow(`SELECT occurred_at, instance, route, method, message, panic FROM server_errors`).
		Scan(&got.OccurredAt, &got.Instance, &got.Route, &got.Method, &got.Message, &got.Panic)
	if err != nil {
		t.Fatal(err)
	}
	if !got.OccurredAt.Equal(at) {
		t.Errorf("occurred_at = %v, want %v", got.OccurredAt, at)
	}
	got.OccurredAt = in.OccurredAt
	if got != in {
		t.Errorf("stored %+v, want %+v", got, in)
	}
}

func TestServerErrorRecordCutsMessage(t *testing.T) {
	repo, db := newServerErrorRepo(t)
	ctx := context.Background()

	tests := []struct {
		name, message string
		wantLen       int
	}{
		{"short", "boom", 4},
		{"exactly the limit", strings.Repeat("a", MaxServerErrorMessage), MaxServerErrorMessage},
		{"over the limit", strings.Repeat("a", MaxServerErrorMessage+500), MaxServerErrorMessage},
		// After the "a", each two-byte "é" starts at an odd offset, so byte
		// 2,000 is the middle of one: the cut comes before it.
		{"multibyte at the limit", "a" + strings.Repeat("é", MaxServerErrorMessage), MaxServerErrorMessage - 1},
		{"invalid UTF-8", "bad \xff byte", len("bad � byte")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := db.Exec(`DELETE FROM server_errors`); err != nil {
				t.Fatal(err)
			}
			err := repo.Record(ctx, rss.ServerError{Instance: "web-1", Route: "/", Method: "GET", Message: tt.message})
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			msgs := serverErrorMessages(t, db)
			if len(msgs) != 1 {
				t.Fatalf("stored %d rows, want 1", len(msgs))
			}
			if len(msgs[0]) != tt.wantLen || !utf8.ValidString(msgs[0]) {
				t.Errorf("stored %d bytes (valid UTF-8: %t), want %d", len(msgs[0]), utf8.ValidString(msgs[0]), tt.wantLen)
			}
		})
	}
}

func TestServerErrorTrim(t *testing.T) {
	repo, db := newServerErrorRepo(t)
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	for i, msg := range []string{"oldest", "older", "newer", "newest"} {
		e := rss.ServerError{OccurredAt: start.Add(time.Duration(i) * time.Minute), Instance: "web-1", Route: "/", Method: "GET", Message: msg}
		if err := repo.Record(ctx, e); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	if err := repo.Trim(ctx, 2); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if got, want := serverErrorMessages(t, db), []string{"newest", "newer"}; !slices.Equal(got, want) {
		t.Errorf("after Trim(2): %v, want %v", got, want)
	}

	// Keeping more than there are deletes nothing.
	if err := repo.Trim(ctx, 10); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if got := serverErrorMessages(t, db); len(got) != 2 {
		t.Errorf("after Trim(10): %v, want 2 rows", got)
	}
}
