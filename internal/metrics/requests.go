// Package metrics records www's request and runtime metrics and its recent
// server errors in Postgres, for the admin, and rolls up and deletes old rows,
// the real-user views' included (see RUMBounds). See "Observability" in
// docs/decisions.md.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DurationBounds are the upper bounds, in milliseconds, of the latency
// histogram's buckets; a last bucket counts everything above the last bound.
var DurationBounds = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// Unmatched is the route of requests outside every route.
const Unmatched = "(unmatched)"

const (
	defaultInterval = time.Minute
	// flushTimeout bounds one write, including the last one on shutdown,
	// when the context Run was given is already canceled.
	flushTimeout = 10 * time.Second
)

// RequestRow is one request_metrics row: the requests an instance served on
// one route, with one method and status class, in one period.
type RequestRow struct {
	PeriodStart   time.Time
	Instance      string
	Route         string
	Method        string
	StatusClass   int
	Requests      int
	DurationMsSum float64
	BytesSum      int64
	Buckets       []int64 // len(DurationBounds)+1
}

// RequestStore saves minute rows; satisfied by *psql.MetricsRepository. A
// row for a period, instance and key that's already there adds to it.
type RequestStore interface {
	SaveRequests(ctx context.Context, rows []RequestRow) error
}

type requestKey struct {
	period      time.Time
	route       string
	method      string
	statusClass int
}

// Requests counts requests in memory and writes them to its Store once a minute.
type Requests struct {
	Store    RequestStore
	Instance string // os.Hostname()
	Logger   *slog.Logger
	Interval time.Duration    // default 1m; tests shorten it
	Now      func() time.Time // default time.Now; tests fix it

	mu     sync.Mutex
	counts map[requestKey]*RequestRow
}

// Observe counts one request in the minute it finished. route is the chi
// route pattern, or Unmatched; status 0 counts as 200, as net/http sends.
func (r *Requests) Observe(route, method string, status int, d time.Duration, bytes int) {
	now := r.now()
	k := requestKey{
		period:      now.Truncate(time.Minute),
		route:       route,
		method:      NormalizeMethod(method),
		statusClass: StatusClass(status),
	}
	ms := float64(d) / float64(time.Millisecond)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = make(map[requestKey]*RequestRow)
	}
	row := r.counts[k]
	if row == nil {
		row = &RequestRow{
			PeriodStart: k.period,
			Instance:    r.Instance,
			Route:       k.route,
			Method:      k.method,
			StatusClass: k.statusClass,
			Buckets:     make([]int64, len(DurationBounds)+1),
		}
		r.counts[k] = row
	}
	row.Requests++
	row.DurationMsSum += ms
	row.BytesSum += int64(bytes)
	row.Buckets[Bucket(ms)]++
}

// Run writes the counts every Interval, at the start of each minute by
// default, until ctx is canceled, then writes them once more and returns.
func (r *Requests) Run(ctx context.Context) error {
	r.setDefaults()
	for {
		t := time.NewTimer(untilNext(r.now(), r.Interval))
		select {
		case <-ctx.Done():
			t.Stop()
			r.Flush(context.WithoutCancel(ctx))
			return nil
		case <-t.C:
			r.Flush(ctx)
		}
	}
}

// Flush writes the counts so far and starts counting afresh. Counts that
// fail to save are logged and dropped, so a database outage can't grow them.
func (r *Requests) Flush(ctx context.Context) {
	rows := r.take()
	if len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := r.Store.SaveRequests(ctx, rows); err != nil {
		logger(r.Logger).Error("metrics: save request metrics", "rows", len(rows), "error", err)
	}
}

// take returns the counts and empties them.
func (r *Requests) take() []RequestRow {
	r.mu.Lock()
	counts := r.counts
	r.counts = nil
	r.mu.Unlock()

	rows := make([]RequestRow, 0, len(counts))
	for _, row := range counts {
		rows = append(rows, *row)
	}
	return rows
}

func (r *Requests) setDefaults() {
	if r.Interval <= 0 {
		r.Interval = defaultInterval
	}
}

func (r *Requests) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Bucket returns the index of the DurationBounds bucket that counts a
// request of ms milliseconds: the first whose bound is at least ms, or the
// last, above every bound.
func Bucket(ms float64) int {
	for i, bound := range DurationBounds {
		if ms <= bound {
			return i
		}
	}
	return len(DurationBounds)
}

// StatusClass returns 2 for a 2xx status, and so on, between 1 and 5. 0
// counts as 200, which net/http sends when a handler writes nothing.
func StatusClass(status int) int {
	if status == 0 {
		return 2
	}
	return min(max(status/100, 1), 5)
}

// NormalizeMethod keeps the standard methods and folds the rest into
// OTHER, so a client can't add rows by inventing methods.
func NormalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return m
	}
	return "OTHER"
}

// untilNext returns how long from now until the next multiple of interval
// (the next minute's start, for a minute), so rows are written soon after
// their minute ends.
func untilNext(now time.Time, interval time.Duration) time.Duration {
	return now.Truncate(interval).Add(interval).Sub(now)
}
