package metrics

import (
	"context"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeJobStore records the rows it's asked to save.
type fakeJobStore struct {
	mu    sync.Mutex
	saved [][]JobRow
}

func (f *fakeJobStore) SaveJobs(ctx context.Context, rows []JobRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, rows)
	return nil
}

func (f *fakeJobStore) calls() [][]JobRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.saved)
}

func TestJobsFlush(t *testing.T) {
	store := &fakeJobStore{}
	now := time.Date(2026, 3, 1, 10, 5, 30, 0, time.UTC)
	j := &Jobs{Store: store, Instance: "web-1", Now: fixedNow(now)}

	j.Observe("refresh", "saved", 7*time.Millisecond)
	j.Observe("refresh", "saved", 30*time.Millisecond)
	j.Observe("refresh", "not_modified", 20*time.Millisecond)
	j.Observe("mail", "sent", 2*time.Second)
	j.Flush(context.Background())

	calls := store.calls()
	if len(calls) != 1 {
		t.Fatalf("SaveJobs called %d times, want 1", len(calls))
	}
	got := calls[0]
	sort.Slice(got, func(i, k int) bool {
		if got[i].Runner != got[k].Runner {
			return got[i].Runner < got[k].Runner
		}
		return got[i].Outcome < got[k].Outcome
	})
	minute := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	buckets := func(idx ...int) []int64 {
		b := make([]int64, len(DurationBounds)+1)
		for _, i := range idx {
			b[i]++
		}
		return b
	}
	want := []JobRow{
		{minute, "web-1", "mail", "sent", 1, 2000, buckets(8)},
		{minute, "web-1", "refresh", "not_modified", 1, 20, buckets(2)},
		{minute, "web-1", "refresh", "saved", 2, 37, buckets(1, 3)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.PeriodStart.Equal(w.PeriodStart) || g.Instance != w.Instance || g.Runner != w.Runner || g.Outcome != w.Outcome ||
			g.Jobs != w.Jobs || g.DurationMsSum != w.DurationMsSum || !slices.Equal(g.Buckets, w.Buckets) {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}

	// The counts start afresh: nothing more to write.
	j.Flush(context.Background())
	if n := len(store.calls()); n != 1 {
		t.Errorf("SaveJobs called %d times after an empty flush, want 1", n)
	}
}

func TestJobsNilObservesNothing(t *testing.T) {
	var j *Jobs
	j.Observe("refresh", "saved", time.Millisecond) // doesn't panic
}

func TestJobsFlushOnShutdown(t *testing.T) {
	store := &fakeJobStore{}
	j := &Jobs{Store: store, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- j.Run(ctx) }()

	j.Observe("lookup", "found", time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return after cancel")
	}

	calls := store.calls()
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0].Jobs != 1 {
		t.Errorf("saved %+v, want the one job on shutdown", calls)
	}
}
