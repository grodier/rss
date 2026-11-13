package psql

import (
	"context"
	"database/sql"
	"errors"
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

// recordCheck stores a check at at: ok as "available", else status.
func recordCheck(t *testing.T, repo *AvailabilityRepository, at time.Time, ok bool, status, version string) {
	t.Helper()
	if ok {
		status = "available"
	}
	c := rss.AvailabilityCheck{CheckedAt: at, OK: ok, Status: status, Version: version}
	if err := repo.Record(context.Background(), c); err != nil {
		t.Fatalf("Record(%+v): %v", c, err)
	}
}

func TestAvailabilityUptime(t *testing.T) {
	repo, _ := newAvailabilityRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	if got, err := repo.Uptime(ctx, now); err != nil || got != (rss.Uptime{}) {
		t.Fatalf("Uptime on no checks = %+v, %v; want zeros", got, err)
	}

	recordCheck(t, repo, now.Add(-time.Hour), true, "", "")
	recordCheck(t, repo, now.Add(-3*day), false, rss.CheckTimeout, "")
	recordCheck(t, repo, now.Add(-20*day), true, "", "")
	recordCheck(t, repo, now.Add(-40*day), false, "degraded", "")
	recordCheck(t, repo, now.Add(-100*day), true, "", "")  // older than every window
	recordCheck(t, repo, now.Add(time.Hour), true, "", "") // after now

	got, err := repo.Uptime(ctx, now)
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	want := rss.Uptime{
		Day:     rss.UptimeWindow{Checks: 1, OK: 1},
		Week:    rss.UptimeWindow{Checks: 2, OK: 1},
		Month:   rss.UptimeWindow{Checks: 3, OK: 2},
		Quarter: rss.UptimeWindow{Checks: 4, OK: 2},
	}
	if got != want {
		t.Errorf("Uptime = %+v, want %+v", got, want)
	}
}

func TestAvailabilityIncidents(t *testing.T) {
	repo, _ := newAvailabilityRepo(t)
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	// ok, timeout, error, ok, degraded, ok, error, (no check), error.
	recordCheck(t, repo, at(0), true, "", "")
	recordCheck(t, repo, at(1), false, rss.CheckTimeout, "")
	recordCheck(t, repo, at(2), false, rss.CheckError, "")
	recordCheck(t, repo, at(3), true, "", "")
	recordCheck(t, repo, at(4), false, "degraded", "")
	recordCheck(t, repo, at(5), true, "", "")
	recordCheck(t, repo, at(6), false, rss.CheckError, "")
	recordCheck(t, repo, at(8), false, rss.CheckError, "")

	tests := []struct {
		name  string
		since time.Time
		limit int
		want  []rss.Incident
	}{
		{
			name: "all", since: t0, limit: 50,
			want: []rss.Incident{
				// A missing minute doesn't split the ongoing one.
				{StartedAt: at(6), LastFailedAt: at(8), Checks: 2, Statuses: []string{"error"}},
				{StartedAt: at(4), LastFailedAt: at(4), EndedAt: at(5), Checks: 1, Statuses: []string{"degraded"}},
				{StartedAt: at(1), LastFailedAt: at(2), EndedAt: at(3), Checks: 2, Statuses: []string{"error", "timeout"}},
			},
		},
		{
			name: "limit", since: t0, limit: 1,
			want: []rss.Incident{{StartedAt: at(6), LastFailedAt: at(8), Checks: 2, Statuses: []string{"error"}}},
		},
		{
			name: "since in the middle of one", since: at(2), limit: 50,
			want: []rss.Incident{
				{StartedAt: at(6), LastFailedAt: at(8), Checks: 2, Statuses: []string{"error"}},
				{StartedAt: at(4), LastFailedAt: at(4), EndedAt: at(5), Checks: 1, Statuses: []string{"degraded"}},
				{StartedAt: at(2), LastFailedAt: at(2), EndedAt: at(3), Checks: 1, Statuses: []string{"error"}},
			},
		},
		{name: "none since", since: at(9), limit: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Incidents(ctx, tt.since, tt.limit)
			if err != nil {
				t.Fatalf("Incidents: %v", err)
			}
			for i := range got {
				got[i].StartedAt = got[i].StartedAt.UTC()
				got[i].LastFailedAt = got[i].LastFailedAt.UTC()
				if !got[i].EndedAt.IsZero() {
					got[i].EndedAt = got[i].EndedAt.UTC()
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Incidents:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestAvailabilityLatestVersion(t *testing.T) {
	repo, _ := newAvailabilityRepo(t)
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := repo.LatestVersion(ctx); !errors.Is(err, rss.ErrNoRecord) {
		t.Fatalf("LatestVersion on no checks: err = %v, want ErrNoRecord", err)
	}
	recordCheck(t, repo, t0, false, rss.CheckTimeout, "")
	if _, err := repo.LatestVersion(ctx); !errors.Is(err, rss.ErrNoRecord) {
		t.Fatalf("LatestVersion with no version reported: err = %v, want ErrNoRecord", err)
	}

	recordCheck(t, repo, t0.Add(time.Minute), true, "", "old")
	recordCheck(t, repo, t0.Add(2*time.Minute), true, "", "new")
	recordCheck(t, repo, t0.Add(3*time.Minute), false, rss.CheckTimeout, "") // didn't say
	got, err := repo.LatestVersion(ctx)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got.Version != "new" || !got.CheckedAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("LatestVersion = %q at %v, want new at %v", got.Version, got.CheckedAt, t0.Add(2*time.Minute))
	}
}
