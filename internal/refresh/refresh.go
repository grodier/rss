// Package refresh fetches feeds in the background when they're due.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers      = 2
	defaultPollInterval = 15 * time.Second
	defaultJobTimeout   = 30 * time.Second
	defaultLease        = 5 * time.Minute
)

// Store hands out due feeds; satisfied by *psql.FeedRepository.
type Store interface {
	ClaimDue(ctx context.Context, lease time.Duration) (rss.Feed, error) // rss.ErrNoRecord when none is due
}

// Refresher fetches a feed and records the outcome; satisfied by *ingest.Refresher.
type Refresher interface {
	Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error)
}

// JobObserver counts the runner's jobs; satisfied by *metrics.Jobs.
type JobObserver interface {
	Observe(runner, outcome string, d time.Duration)
}

// Runner refreshes due feeds with a pool of workers. Zero values use the defaults.
type Runner struct {
	Store        Store
	Refresher    Refresher
	Logger       *slog.Logger
	Metrics      JobObserver   // counts jobs by outcome: saved, not_modified (304), gone (410) or failed; nil counts nothing
	Workers      int           // default 2
	PollInterval time.Duration // default 15s; wait after finding nothing due
	JobTimeout   time.Duration // default 30s
	Lease        time.Duration // default 5m (must exceed JobTimeout)
}

// Run runs Workers goroutines until ctx is canceled and returns after they
// have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.Lease <= r.JobTimeout {
		return fmt.Errorf("refresh: Lease (%s) must exceed JobTimeout (%s)", r.Lease, r.JobTimeout)
	}

	var wg sync.WaitGroup
	for range r.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.work(ctx)
		}()
	}
	wg.Wait()
	return nil
}

func (r *Runner) setDefaults() {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Workers <= 0 {
		r.Workers = defaultWorkers
	}
	if r.PollInterval <= 0 {
		r.PollInterval = defaultPollInterval
	}
	if r.JobTimeout <= 0 {
		r.JobTimeout = defaultJobTimeout
	}
	if r.Lease <= 0 {
		r.Lease = defaultLease
	}
}

func (r *Runner) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		feed, err := r.Store.ClaimDue(ctx, r.Lease)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("refresh: claim due feed", "error", err)
			}
			if !r.wait(ctx) {
				return
			}
			continue
		}

		r.refresh(ctx, feed)
	}
}

// wait sleeps for PollInterval and reports whether ctx is still live.
func (r *Runner) wait(ctx context.Context) bool {
	t := time.NewTimer(r.PollInterval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// refresh refreshes one feed, logs the outcome and counts it, unless ctx
// was canceled meanwhile. A panic is logged, counted as a failure and
// swallowed: the feed's lease runs out and it is retried later.
func (r *Runner) refresh(ctx context.Context, feed rss.Feed) {
	start := time.Now()
	outcome := "" // nothing to count on shutdown
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("refresh: panic", "feed_id", feed.ID, "panic", rec)
			outcome = "failed"
		}
		if outcome != "" {
			r.observe(outcome, start)
		}
	}()

	jobCtx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	defer cancel()

	res, err := r.Refresher.Refresh(jobCtx, feed)
	switch {
	case ctx.Err() != nil:
		// Shutdown; Refresh recorded nothing and the lease retries the feed.
	case err == nil:
		outcome = "saved"
		if res.NotModified {
			outcome = "not_modified"
		}
		r.Logger.Info("feed refreshed",
			"feed_id", feed.ID,
			"url", feed.Url,
			"new", res.New,
			"updated", res.Updated,
			"not_modified", res.NotModified,
			"behind", res.Behind,
			"duration", time.Since(start),
		)
	case errors.Is(err, ingest.ErrGone):
		outcome = "gone"
		r.Logger.Info("feed gone", "feed_id", feed.ID, "url", feed.Url, "error", err)
	case errors.Is(err, ingest.ErrUnreachable), errors.Is(err, ingest.ErrNotFeed):
		outcome = "failed"
		r.Logger.Info("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "error", err)
	default:
		outcome = "failed"
		r.Logger.Error("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "error", err)
	}
}

// observe counts a job that started at start and ended with outcome.
func (r *Runner) observe(outcome string, start time.Time) {
	if r.Metrics != nil {
		r.Metrics.Observe("refresh", outcome, time.Since(start))
	}
}
