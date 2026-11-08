package metrics

import (
	"database/sql"
	"math"
	"testing"
	"time"
)

type fakeDBStats struct{ stats sql.DBStats }

func (f fakeDBStats) Stats() sql.DBStats { return f.stats }

func TestRuntimeSample(t *testing.T) {
	r := &Runtime{
		Instance: "web-1",
		DB:       fakeDBStats{sql.DBStats{OpenConnections: 3, InUse: 1, WaitCount: 4, WaitDuration: 1500 * time.Millisecond}},
	}
	now := time.Date(2026, 3, 1, 10, 5, 30, 0, time.UTC)
	row := r.Sample(now)

	if row.HeapBytes <= 0 || row.TotalBytes < row.HeapBytes {
		t.Errorf("heap = %d, total = %d; want heap > 0 and total >= heap", row.HeapBytes, row.TotalBytes)
	}
	if row.Goroutines <= 0 {
		t.Errorf("goroutines = %d, want > 0", row.Goroutines)
	}
	if row.GCPauseP99Ms < 0 {
		t.Errorf("GC pause p99 = %v, want >= 0", row.GCPauseP99Ms)
	}
	if !row.PeriodStart.Equal(time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)) || row.Instance != "web-1" {
		t.Errorf("key = %v %q", row.PeriodStart, row.Instance)
	}
	if row.DBOpen != 3 || row.DBInUse != 1 || row.DBWaitCount != 4 || row.DBWaitMs != 1500 {
		t.Errorf("pool = %+v", row)
	}
}

func TestP99(t *testing.T) {
	buckets := []float64{math.Inf(-1), 0.001, 0.01, math.Inf(1)}
	tests := []struct {
		name   string
		counts []uint64
		want   float64
	}{
		{"empty", []uint64{0, 0, 0}, 0},
		{"all in one bucket", []uint64{0, 5, 0}, 0.01},
		// The unbounded bucket reports its lower bound.
		{"in the last bucket", []uint64{0, 98, 2}, 0.01},
		{"under 1% in the last bucket", []uint64{0, 990, 9}, 0.01},
		{"first bucket", []uint64{100, 0, 0}, 0.001},
	}
	for _, tt := range tests {
		if got := p99(tt.counts, buckets); got != tt.want {
			t.Errorf("%s: p99 = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSince(t *testing.T) {
	cur := []uint64{5, 7, 1}
	prevCounts := []uint64{2, 7, 0}
	if got := since(cur, prevCounts); got[0] != 3 || got[1] != 0 || got[2] != 1 {
		t.Errorf("since = %v, want [3 0 1]", got)
	}
	if got := since(cur, nil); got[0] != 5 {
		t.Errorf("since without prev = %v, want cur", got)
	}
}
