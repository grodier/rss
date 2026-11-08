package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"math"
	"runtime"
	rtmetrics "runtime/metrics"
	"time"
)

// RuntimeRow is one runtime_metrics row: a sample of an instance's Go
// runtime and connection pool.
type RuntimeRow struct {
	PeriodStart  time.Time
	Instance     string
	HeapBytes    int64
	TotalBytes   int64 // memory the Go runtime holds from the OS
	Goroutines   int
	GCPauseP99Ms float64 // over the minute, since the previous sample
	DBOpen       int
	DBInUse      int
	DBWaitCount  int64 // cumulative, as sql.DBStats reports it
	DBWaitMs     int64 // cumulative
}

// RuntimeStore saves samples; satisfied by *psql.MetricsRepository.
type RuntimeStore interface {
	SaveRuntime(ctx context.Context, row RuntimeRow) error
}

// DBStatser reports a connection pool's statistics; *sql.DB is one.
type DBStatser interface {
	Stats() sql.DBStats
}

const (
	metricHeap     = "/memory/classes/heap/objects:bytes"
	metricTotal    = "/memory/classes/total:bytes"
	metricGCPauses = "/sched/pauses/total/gc:seconds" // the newer name of /gc/pauses:seconds
)

// Runtime writes a sample of the process's memory, goroutines, GC pauses and
// connection pool once a minute.
type Runtime struct {
	Store    RuntimeStore
	DB       DBStatser
	Instance string // os.Hostname()
	Logger   *slog.Logger
	Interval time.Duration // default 1m

	samples    []rtmetrics.Sample
	lastPauses []uint64 // the cumulative GC pause counts at the previous sample
}

// Run writes a sample at the end of every minute until ctx is canceled. A
// sample is stamped with the minute it ends, as request rows are with the
// minute their requests finished in.
func (r *Runtime) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = defaultInterval
	}
	r.Sample(time.Now()) // so the first written sample's GC pauses cover one minute
	for {
		t := time.NewTimer(untilNext(time.Now(), r.Interval))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case now := <-t.C:
			// Halfway back through the interval it covers, so the stamp is
			// the minute just ended even if the timer fires a little early
			// or late by the wall clock.
			row := r.Sample(now.Add(-r.Interval / 2))
			sctx, cancel := context.WithTimeout(ctx, flushTimeout)
			if err := r.Store.SaveRuntime(sctx, row); err != nil && ctx.Err() == nil {
				logger(r.Logger).Error("metrics: save runtime metrics", "error", err)
			}
			cancel()
		}
	}
}

// Sample reads the runtime and pool statistics now, stamped with the minute
// that at is in.
func (r *Runtime) Sample(at time.Time) RuntimeRow {
	if r.samples == nil {
		r.samples = []rtmetrics.Sample{{Name: metricHeap}, {Name: metricTotal}, {Name: metricGCPauses}}
	}
	rtmetrics.Read(r.samples)

	row := RuntimeRow{
		PeriodStart: at.Truncate(time.Minute),
		Instance:    r.Instance,
		HeapBytes:   int64(r.samples[0].Value.Uint64()),
		TotalBytes:  int64(r.samples[1].Value.Uint64()),
		Goroutines:  runtime.NumGoroutine(),
	}
	pauses := r.samples[2].Value.Float64Histogram()
	row.GCPauseP99Ms = p99(since(pauses.Counts, r.lastPauses), pauses.Buckets) * 1000
	// Value's histogram is reused by the next Read: keep a copy.
	r.lastPauses = append(r.lastPauses[:0], pauses.Counts...)

	if r.DB != nil {
		s := r.DB.Stats()
		row.DBOpen = s.OpenConnections
		row.DBInUse = s.InUse
		row.DBWaitCount = s.WaitCount
		row.DBWaitMs = s.WaitDuration.Milliseconds()
	}
	return row
}

// since returns the counts of cur that weren't in prev (both cumulative,
// with the same buckets), or cur's if there's no prev.
func since(cur, prev []uint64) []uint64 {
	counts := append([]uint64(nil), cur...)
	if len(prev) == len(counts) {
		for i := range counts {
			counts[i] -= prev[i]
		}
	}
	return counts
}

// p99 returns the 99th percentile of a histogram, in its buckets' unit: the
// upper bound of the bucket it falls in (the lower one for the last,
// unbounded bucket), or 0 if the histogram is empty. buckets has one more
// element than counts: bucket i is [buckets[i], buckets[i+1]).
func p99(counts []uint64, buckets []float64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(float64(total) * 0.99))
	var seen uint64
	for i, c := range counts {
		seen += c
		if seen >= rank {
			if hi := buckets[i+1]; !math.IsInf(hi, 1) {
				return hi
			}
			return max(buckets[i], 0)
		}
	}
	return 0
}

func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
}
