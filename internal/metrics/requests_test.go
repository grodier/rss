package metrics

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeRequestStore records the rows it's asked to save.
type fakeRequestStore struct {
	mu    sync.Mutex
	saved [][]RequestRow
	err   error
}

func (f *fakeRequestStore) SaveRequests(ctx context.Context, rows []RequestRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, rows)
	return f.err
}

func (f *fakeRequestStore) calls() [][]RequestRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.saved)
}

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestObserveBuckets(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{5 * time.Millisecond, 0},
		{7 * time.Millisecond, 1}, // the 10 ms bucket
		{10 * time.Millisecond, 1},
		{10*time.Millisecond + time.Microsecond, 2},
		{10 * time.Second, len(DurationBounds) - 1},
		{11 * time.Second, len(DurationBounds)}, // above every bound
	}
	for _, tt := range tests {
		r := &Requests{Now: fixedNow(time.Now())}
		r.Observe("/", "GET", 200, tt.d, 0)
		rows := r.take()
		if len(rows) != 1 {
			t.Fatalf("%v: got %d rows, want 1", tt.d, len(rows))
		}
		want := make([]int64, len(DurationBounds)+1)
		want[tt.want] = 1
		if !slices.Equal(rows[0].Buckets, want) {
			t.Errorf("%v: buckets = %v, want %v", tt.d, rows[0].Buckets, want)
		}
	}
}

func TestStatusClass(t *testing.T) {
	tests := []struct{ status, want int }{
		{0, 2}, // nothing written: net/http sends 200
		{101, 1},
		{200, 2},
		{204, 2},
		{303, 3},
		{404, 4},
		{500, 5},
		{999, 5},
	}
	for _, tt := range tests {
		if got := StatusClass(tt.status); got != tt.want {
			t.Errorf("StatusClass(%d) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestFlush(t *testing.T) {
	store := &fakeRequestStore{}
	now := time.Date(2026, 3, 1, 10, 5, 30, 0, time.UTC)
	r := &Requests{Store: store, Instance: "web-1", Now: fixedNow(now)}

	r.Observe("/feeds/{id}", "GET", 200, 7*time.Millisecond, 100)
	r.Observe("/feeds/{id}", "GET", 204, 30*time.Millisecond, 0)
	r.Observe("/feeds/{id}", "POST", 303, 20*time.Millisecond, 10)
	r.Observe("/", "GET", 500, 2*time.Millisecond, 50)
	r.Observe("(unmatched)", "BREW", 405, time.Millisecond, 5)
	r.Flush(context.Background())

	calls := store.calls()
	if len(calls) != 1 {
		t.Fatalf("SaveRequests called %d times, want 1", len(calls))
	}
	got := calls[0]
	sort.Slice(got, func(i, j int) bool {
		if got[i].Route != got[j].Route {
			return got[i].Route < got[j].Route
		}
		return got[i].Method < got[j].Method
	})
	minute := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)
	buckets := func(idx ...int) []int64 {
		b := make([]int64, len(DurationBounds)+1)
		for _, i := range idx {
			b[i]++
		}
		return b
	}
	want := []RequestRow{
		{minute, "web-1", "(unmatched)", "OTHER", 4, 1, 1, 5, buckets(0)},
		{minute, "web-1", "/", "GET", 5, 1, 2, 50, buckets(0)},
		{minute, "web-1", "/feeds/{id}", "GET", 2, 2, 37, 100, buckets(1, 3)},
		{minute, "web-1", "/feeds/{id}", "POST", 3, 1, 20, 10, buckets(2)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.PeriodStart.Equal(w.PeriodStart) || g.Instance != w.Instance || g.Route != w.Route || g.Method != w.Method ||
			g.StatusClass != w.StatusClass || g.Requests != w.Requests || g.DurationMsSum != w.DurationMsSum ||
			g.BytesSum != w.BytesSum || !slices.Equal(g.Buckets, w.Buckets) {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}

	// The counts start afresh: nothing more to write.
	r.Flush(context.Background())
	if n := len(store.calls()); n != 1 {
		t.Errorf("SaveRequests called %d times after an empty flush, want 1", n)
	}
}

func TestFlushKeepsMinutesApart(t *testing.T) {
	store := &fakeRequestStore{}
	now := time.Date(2026, 3, 1, 10, 5, 59, 0, time.UTC)
	r := &Requests{Store: store, Now: func() time.Time { return now }}
	r.Observe("/", "GET", 200, time.Millisecond, 0)
	now = now.Add(2 * time.Second)
	r.Observe("/", "GET", 200, time.Millisecond, 0)
	r.Flush(context.Background())

	rows := store.calls()[0]
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one per minute: %+v", len(rows), rows)
	}
	if rows[0].PeriodStart.Equal(rows[1].PeriodStart) {
		t.Errorf("both rows in %v", rows[0].PeriodStart)
	}
}

func TestFlushDropsFailedCounts(t *testing.T) {
	store := &fakeRequestStore{err: errors.New("database down")}
	r := &Requests{Store: store}
	r.Observe("/", "GET", 200, time.Millisecond, 0)
	r.Flush(context.Background())
	r.Flush(context.Background())
	if n := len(store.calls()); n != 1 {
		t.Errorf("SaveRequests called %d times, want 1: failed counts are dropped", n)
	}
}

func TestFlushOnShutdown(t *testing.T) {
	store := &fakeRequestStore{}
	r := &Requests{Store: store, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	r.Observe("/", "GET", 200, time.Millisecond, 0)
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
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0].Requests != 1 {
		t.Errorf("saved %+v, want the one request on shutdown", calls)
	}
}

func TestRunFlushesEveryInterval(t *testing.T) {
	store := &fakeRequestStore{}
	r := &Requests{Store: store, Interval: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Observe("/", "GET", 200, time.Millisecond, 0)
	deadline := time.Now().Add(5 * time.Second)
	for len(store.calls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no flush within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
