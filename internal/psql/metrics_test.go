package psql

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/metrics"
)

// newMetricsRepo returns a repository on a freshly migrated schema of its
// own: RollUp and DeleteBefore work on the whole table, and RollUp starts
// after the newest hourly row, so other tests' rows would change the outcome.
func newMetricsRepo(t *testing.T) (*MetricsRepository, *sql.DB) {
	t.Helper()
	db := newMigrateDB(t)
	mustMigrate(t, db)
	return NewMetricsRepository(db), db
}

func requestRow(period time.Time, instance, route string, requests int, buckets ...int64) metrics.RequestRow {
	b := make([]int64, len(metrics.DurationBounds)+1)
	copy(b, buckets)
	return metrics.RequestRow{
		PeriodStart:   period,
		Instance:      instance,
		Route:         route,
		Method:        "GET",
		StatusClass:   2,
		Requests:      requests,
		DurationMsSum: float64(requests) * 10,
		BytesSum:      int64(requests) * 100,
		Buckets:       b,
	}
}

func TestMetricsSaveRequests(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	minute := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

	rows := []metrics.RequestRow{
		requestRow(minute, "web-1", "/feeds/{id}", 3, 1, 2),
		requestRow(minute, "web-1", "/", 1, 0, 1),
	}
	if err := repo.SaveRequests(ctx, rows); err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}
	// The same minute again, as after a restart on the same host: adds up.
	if err := repo.SaveRequests(ctx, rows[:1]); err != nil {
		t.Fatalf("SaveRequests again: %v", err)
	}

	got, err := repo.ListRequests(ctx, "minute", minute, minute.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	byRoute := map[string]metrics.RequestRow{}
	for _, r := range got {
		byRoute[r.Route] = r
	}
	feed := byRoute["/feeds/{id}"]
	if feed.Requests != 6 || feed.DurationMsSum != 60 || feed.BytesSum != 600 {
		t.Errorf("/feeds/{id} = %+v, want 6 requests, 60 ms, 600 bytes", feed)
	}
	if want := requestRow(minute, "", "", 0, 2, 4).Buckets; !slices.Equal(feed.Buckets, want) {
		t.Errorf("/feeds/{id} buckets = %v, want %v", feed.Buckets, want)
	}
	if !feed.PeriodStart.Equal(minute) || feed.Instance != "web-1" || feed.Method != "GET" || feed.StatusClass != 2 {
		t.Errorf("/feeds/{id} key = %+v", feed)
	}
	if root := byRoute["/"]; root.Requests != 1 {
		t.Errorf("/ requests = %d, want 1", root.Requests)
	}
}

func TestMetricsRollUp(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	hour := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	err := repo.SaveRequests(ctx, []metrics.RequestRow{
		requestRow(hour.Add(1*time.Minute), "web-1", "/", 2, 1, 1),
		requestRow(hour.Add(2*time.Minute), "web-1", "/", 3, 0, 2, 1),
		requestRow(hour.Add(2*time.Minute), "web-2", "/", 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1),
		requestRow(hour.Add(3*time.Minute), "web-2", "/feeds", 4, 4),
		// The next hour isn't over at the cut-off below: left alone.
		requestRow(hour.Add(61*time.Minute), "web-1", "/", 5, 5),
	})
	if err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}

	before := hour.Add(90 * time.Minute)
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	// Idempotent: a second run (or another instance's) adds nothing.
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp again: %v", err)
	}

	got, err := repo.ListRequests(ctx, "hour", hour.Add(-24*time.Hour), hour.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d hourly rows, want 2: %+v", len(got), got)
	}
	byRoute := map[string]metrics.RequestRow{}
	for _, r := range got {
		if !r.PeriodStart.Equal(hour) || r.Instance != "" {
			t.Errorf("hourly row %+v: want period %v and instance ''", r, hour)
		}
		byRoute[r.Route] = r
	}
	root := byRoute["/"]
	if root.Requests != 6 || root.DurationMsSum != 60 || root.BytesSum != 600 {
		t.Errorf("/ = %+v, want 6 requests, 60 ms, 600 bytes", root)
	}
	if want := requestRow(hour, "", "", 0, 1, 3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1).Buckets; !slices.Equal(root.Buckets, want) {
		t.Errorf("/ buckets = %v, want %v", root.Buckets, want)
	}
	if feeds := byRoute["/feeds"]; feeds.Requests != 4 || feeds.Buckets[0] != 4 {
		t.Errorf("/feeds = %+v, want 4 requests in the first bucket", feeds)
	}

	// Once the next hour is over, a later run rolls it up too.
	if err := repo.RollUp(ctx, hour.Add(150*time.Minute)); err != nil {
		t.Fatalf("RollUp next hour: %v", err)
	}
	got, err = repo.ListRequests(ctx, "hour", hour.Add(time.Hour), hour.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(got) != 1 || got[0].Requests != 5 {
		t.Errorf("next hour = %+v, want one row of 5 requests", got)
	}
}

func TestMetricsDeleteBefore(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	day := 24 * time.Hour

	err := repo.SaveRequests(ctx, []metrics.RequestRow{
		requestRow(ago(8*day), "web-1", "/old-minute", 1),
		requestRow(ago(6*day), "web-1", "/new-minute", 1),
	})
	if err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}
	// Hourly rows, by rolling up minute rows saved for the purpose.
	err = repo.SaveRequests(ctx, []metrics.RequestRow{
		requestRow(ago(91*day), "web-1", "/old-hour", 1),
		requestRow(ago(89*day), "web-1", "/new-hour", 1),
	})
	if err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}
	if err := repo.RollUp(ctx, ago(80*day)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	err = repo.SaveJobs(ctx, []metrics.JobRow{
		jobRow(ago(8*day), "web-1", "refresh", "old-minute", 1),
		jobRow(ago(6*day), "web-1", "refresh", "new-minute", 1),
		jobRow(ago(91*day), "web-1", "refresh", "old-hour", 1),
		jobRow(ago(89*day), "web-1", "refresh", "new-hour", 1),
	})
	if err != nil {
		t.Fatalf("SaveJobs: %v", err)
	}
	if err := repo.RollUp(ctx, ago(80*day)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	for _, d := range []time.Duration{8 * day, 6 * day} {
		if err := repo.SaveRuntime(ctx, metrics.RuntimeRow{PeriodStart: ago(d), Instance: "web-1", HeapBytes: 1}); err != nil {
			t.Fatalf("SaveRuntime: %v", err)
		}
	}

	if err := repo.DeleteBefore(ctx, ago(metrics.MinuteRetention), ago(metrics.HourRetention), ago(metrics.RuntimeRetention)); err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}

	routes := func(resolution string) []string {
		rows, err := repo.ListRequests(ctx, resolution, ago(365*day), now)
		if err != nil {
			t.Fatalf("ListRequests: %v", err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Route)
		}
		slices.Sort(out)
		return out
	}
	if got, want := routes("minute"), []string{"/new-minute"}; !slices.Equal(got, want) {
		t.Errorf("minute routes = %v, want %v", got, want)
	}
	if got, want := routes("hour"), []string{"/new-hour"}; !slices.Equal(got, want) {
		t.Errorf("hour routes = %v, want %v", got, want)
	}
	outcomes := func(resolution string) []string {
		rows, err := repo.ListJobs(ctx, resolution, ago(365*day), now)
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Outcome)
		}
		slices.Sort(out)
		return out
	}
	if got, want := outcomes("minute"), []string{"new-minute"}; !slices.Equal(got, want) {
		t.Errorf("job minute outcomes = %v, want %v", got, want)
	}
	if got, want := outcomes("hour"), []string{"new-hour"}; !slices.Equal(got, want) {
		t.Errorf("job hour outcomes = %v, want %v", got, want)
	}
	runtime, err := repo.ListRuntime(ctx, ago(365*day), now)
	if err != nil {
		t.Fatalf("ListRuntime: %v", err)
	}
	if len(runtime) != 1 || !runtime[0].PeriodStart.Equal(ago(6*day)) {
		t.Errorf("runtime rows = %+v, want only the one from 6 days ago", runtime)
	}
}

func TestMetricsSaveRuntime(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	minute := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	row := metrics.RuntimeRow{
		PeriodStart: minute, Instance: "web-1", HeapBytes: 1 << 20, TotalBytes: 4 << 20, Goroutines: 12,
		GCPauseP99Ms: 0.5, DBOpen: 3, DBInUse: 1, DBWaitCount: 7, DBWaitMs: 40,
	}
	if err := repo.SaveRuntime(ctx, row); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}
	// A second sample in the same minute is dropped.
	second := row
	second.Goroutines = 99
	if err := repo.SaveRuntime(ctx, second); err != nil {
		t.Fatalf("SaveRuntime again: %v", err)
	}
	got, err := repo.ListRuntime(ctx, minute, minute.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListRuntime: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	got[0].PeriodStart = got[0].PeriodStart.UTC()
	if got[0] != row {
		t.Errorf("got %+v, want %+v", got[0], row)
	}
}

func jobRow(period time.Time, instance, runner, outcome string, jobs int, buckets ...int64) metrics.JobRow {
	b := make([]int64, len(metrics.DurationBounds)+1)
	copy(b, buckets)
	return metrics.JobRow{
		PeriodStart:   period,
		Instance:      instance,
		Runner:        runner,
		Outcome:       outcome,
		Jobs:          jobs,
		DurationMsSum: float64(jobs) * 10,
		Buckets:       b,
	}
}

func TestMetricsSaveJobs(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	minute := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

	rows := []metrics.JobRow{
		jobRow(minute, "web-1", "refresh", "saved", 3, 1, 2),
		jobRow(minute, "web-1", "refresh", "not_modified", 1, 1),
		jobRow(minute, "web-1", "mail", "sent", 2, 0, 2),
	}
	if err := repo.SaveJobs(ctx, rows); err != nil {
		t.Fatalf("SaveJobs: %v", err)
	}
	// The same minute again, as after a restart on the same host: adds up.
	if err := repo.SaveJobs(ctx, rows[:1]); err != nil {
		t.Fatalf("SaveJobs again: %v", err)
	}

	got, err := repo.ListJobs(ctx, "minute", minute, minute.Add(time.Minute))
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(got), got)
	}
	byKey := map[string]metrics.JobRow{}
	for _, r := range got {
		byKey[r.Runner+" "+r.Outcome] = r
	}
	saved := byKey["refresh saved"]
	if saved.Jobs != 6 || saved.DurationMsSum != 60 || !saved.PeriodStart.Equal(minute) || saved.Instance != "web-1" {
		t.Errorf("refresh saved = %+v, want 6 jobs, 60 ms, in %v on web-1", saved, minute)
	}
	if want := jobRow(minute, "", "", "", 0, 2, 4).Buckets; !slices.Equal(saved.Buckets, want) {
		t.Errorf("refresh saved buckets = %v, want %v", saved.Buckets, want)
	}
	if n := byKey["refresh not_modified"].Jobs; n != 1 {
		t.Errorf("refresh not_modified jobs = %d, want 1", n)
	}
	if n := byKey["mail sent"].Jobs; n != 2 {
		t.Errorf("mail sent jobs = %d, want 2", n)
	}
}

func TestMetricsSaveJobsRejectsUnknownRunner(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	err := repo.SaveJobs(context.Background(), []metrics.JobRow{jobRow(time.Now(), "web-1", "nope", "saved", 1)})
	if err == nil {
		t.Error("SaveJobs saved a row for an unknown runner")
	}
}

func TestMetricsRollUpJobs(t *testing.T) {
	repo, _ := newMetricsRepo(t)
	ctx := context.Background()
	hour := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	err := repo.SaveJobs(ctx, []metrics.JobRow{
		jobRow(hour.Add(1*time.Minute), "web-1", "refresh", "saved", 2, 1, 1),
		jobRow(hour.Add(2*time.Minute), "web-2", "refresh", "saved", 3, 0, 2, 1),
		jobRow(hour.Add(2*time.Minute), "web-1", "refresh", "failed", 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1),
		// The next hour isn't over at the cut-off below: left alone.
		jobRow(hour.Add(61*time.Minute), "web-1", "refresh", "saved", 5, 5),
	})
	if err != nil {
		t.Fatalf("SaveJobs: %v", err)
	}

	before := hour.Add(90 * time.Minute)
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	// Idempotent: a second run (or another instance's) adds nothing.
	if err := repo.RollUp(ctx, before); err != nil {
		t.Fatalf("RollUp again: %v", err)
	}

	got, err := repo.ListJobs(ctx, "hour", hour.Add(-24*time.Hour), hour.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d hourly rows, want 2: %+v", len(got), got)
	}
	byOutcome := map[string]metrics.JobRow{}
	for _, r := range got {
		if !r.PeriodStart.Equal(hour) || r.Instance != "" || r.Runner != "refresh" {
			t.Errorf("hourly row %+v: want refresh in %v with instance ''", r, hour)
		}
		byOutcome[r.Outcome] = r
	}
	saved := byOutcome["saved"]
	if saved.Jobs != 5 || saved.DurationMsSum != 50 {
		t.Errorf("saved = %+v, want 5 jobs, 50 ms", saved)
	}
	if want := jobRow(hour, "", "", "", 0, 1, 3, 1).Buckets; !slices.Equal(saved.Buckets, want) {
		t.Errorf("saved buckets = %v, want %v", saved.Buckets, want)
	}
	if failed := byOutcome["failed"]; failed.Jobs != 1 || failed.Buckets[len(metrics.DurationBounds)] != 1 {
		t.Errorf("failed = %+v, want 1 job above the last bound", failed)
	}

	// Once the next hour is over, a later run rolls it up too.
	if err := repo.RollUp(ctx, hour.Add(150*time.Minute)); err != nil {
		t.Fatalf("RollUp next hour: %v", err)
	}
	got, err = repo.ListJobs(ctx, "hour", hour.Add(time.Hour), hour.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(got) != 1 || got[0].Jobs != 5 {
		t.Errorf("next hour = %+v, want one row of 5 jobs", got)
	}
}
