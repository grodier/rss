package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// JobRow is one job_metrics row: the jobs one runner of an instance finished
// with one outcome in one period.
type JobRow struct {
	PeriodStart   time.Time
	Instance      string
	Runner        string // lookup, import, refresh, icon, enrich or mail
	Outcome       string // the runner's, e.g. refresh's saved, not_modified, gone or failed
	Jobs          int
	DurationMsSum float64
	Buckets       []int64 // len(DurationBounds)+1
}

// JobStore saves minute rows; satisfied by *psql.MetricsRepository. A row
// for a period, instance, runner and outcome that's already there adds to it.
type JobStore interface {
	SaveJobs(ctx context.Context, rows []JobRow) error
}

type jobKey struct {
	period  time.Time
	runner  string
	outcome string
}

// Jobs counts the background runners' jobs in memory and writes them to its
// Store once a minute. A nil *Jobs observes nothing.
type Jobs struct {
	Store    JobStore
	Instance string // os.Hostname()
	Logger   *slog.Logger
	Interval time.Duration    // default 1m; tests shorten it
	Now      func() time.Time // default time.Now; tests fix it

	mu     sync.Mutex
	counts map[jobKey]*JobRow
}

// Observe counts one job of runner, which took d and ended with outcome, in
// the minute it finished.
func (j *Jobs) Observe(runner, outcome string, d time.Duration) {
	if j == nil {
		return
	}
	k := jobKey{period: j.now().Truncate(time.Minute), runner: runner, outcome: outcome}
	ms := float64(d) / float64(time.Millisecond)

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.counts == nil {
		j.counts = make(map[jobKey]*JobRow)
	}
	row := j.counts[k]
	if row == nil {
		row = &JobRow{
			PeriodStart: k.period,
			Instance:    j.Instance,
			Runner:      k.runner,
			Outcome:     k.outcome,
			Buckets:     make([]int64, len(DurationBounds)+1),
		}
		j.counts[k] = row
	}
	row.Jobs++
	row.DurationMsSum += ms
	row.Buckets[Bucket(ms)]++
}

// Run writes the counts every Interval, at the start of each minute by
// default, until ctx is canceled, then writes them once more and returns.
func (j *Jobs) Run(ctx context.Context) error {
	if j.Interval <= 0 {
		j.Interval = defaultInterval
	}
	return flushEvery(ctx, j.Interval, j.now, j.Flush)
}

// Flush writes the counts so far and starts counting afresh. Counts that
// fail to save are logged and dropped, so a database outage can't grow them.
func (j *Jobs) Flush(ctx context.Context) {
	rows := j.take()
	if len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := j.Store.SaveJobs(ctx, rows); err != nil {
		logger(j.Logger).Error("metrics: save job metrics", "rows", len(rows), "error", err)
	}
}

// take returns the counts and empties them.
func (j *Jobs) take() []JobRow {
	j.mu.Lock()
	counts := j.counts
	j.counts = nil
	j.mu.Unlock()

	rows := make([]JobRow, 0, len(counts))
	for _, row := range counts {
		rows = append(rows, *row)
	}
	return rows
}

func (j *Jobs) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}
