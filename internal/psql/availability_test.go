package psql

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// newAvailabilityRepo returns a repository on a freshly migrated schema of
// its own: DeleteBefore works on the whole table.
func newAvailabilityRepo(t *testing.T) (*AvailabilityRepository, *sql.DB) {
	t.Helper()
	db := newMigrateDB(t)
	mustMigrate(t, db)
	return NewAvailabilityRepository(db), db
}

// availabilityChecks returns the stored checks, oldest first.
func availabilityChecks(t *testing.T, db *sql.DB) []rss.AvailabilityCheck {
	t.Helper()
	rows, err := db.Query(`SELECT checked_at, ok, status, latency_ms, version FROM availability_checks ORDER BY checked_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []rss.AvailabilityCheck
	for rows.Next() {
		var c rss.AvailabilityCheck
		var latency sql.NullFloat64
		if err := rows.Scan(&c.CheckedAt, &c.OK, &c.Status, &latency, &c.Version); err != nil {
			t.Fatal(err)
		}
		c.CheckedAt = c.CheckedAt.UTC()
		if latency.Valid {
			c.LatencyMs = &latency.Float64
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAvailabilityRecord(t *testing.T) {
	repo, db := newAvailabilityRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	latency := 12.5

	want := []rss.AvailabilityCheck{
		{CheckedAt: at, OK: true, Status: "available", LatencyMs: &latency, Version: "abc123"},
		{CheckedAt: at.Add(time.Minute), OK: false, Status: "degraded", LatencyMs: &latency, Version: "abc123"},
		{CheckedAt: at.Add(2 * time.Minute), OK: false, Status: rss.CheckTimeout},
	}
	for _, c := range want {
		if err := repo.Record(ctx, c); err != nil {
			t.Fatalf("Record(%+v): %v", c, err)
		}
	}
	if got := availabilityChecks(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("stored checks:\n got %+v\nwant %+v", got, want)
	}
}

func TestAvailabilityRecordCutsText(t *testing.T) {
	repo, db := newAvailabilityRepo(t)
	at := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	c := rss.AvailabilityCheck{CheckedAt: at, Status: strings.Repeat("é", 80), Version: "v\xff" + strings.Repeat("v", 150)}
	if err := repo.Record(context.Background(), c); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := availabilityChecks(t, db)[0]
	if want := strings.Repeat("é", MaxCheckText/2); got.Status != want {
		t.Errorf("status = %q, want %q", got.Status, want)
	}
	if want := "v\uFFFD" + strings.Repeat("v", MaxCheckText-4); got.Version != want {
		t.Errorf("version = %q, want %q", got.Version, want)
	}
}

func TestAvailabilityDeleteBefore(t *testing.T) {
	repo, db := newAvailabilityRepo(t)
	ctx := context.Background()
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	for _, at := range []time.Time{cutoff.Add(-time.Minute), cutoff, cutoff.Add(time.Minute)} {
		if err := repo.Record(ctx, rss.AvailabilityCheck{CheckedAt: at, Status: rss.CheckError}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteBefore(ctx, cutoff); err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	var got []time.Time
	for _, c := range availabilityChecks(t, db) {
		got = append(got, c.CheckedAt)
	}
	if want := []time.Time{cutoff, cutoff.Add(time.Minute)}; !reflect.DeepEqual(got, want) {
		t.Errorf("kept checks at %v, want %v", got, want)
	}
}
