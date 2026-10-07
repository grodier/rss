// Package lookup runs site lookups queued in the lookups table.
package lookup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers      = 2
	defaultPollInterval = 500 * time.Millisecond
	defaultJobTimeout   = 30 * time.Second
	defaultStaleAfter   = 2 * time.Minute
	defaultMaxAttempts  = 3

	// recordTimeout bounds writing a job's outcome, which uses a fresh
	// context so an expired job context doesn't prevent it.
	recordTimeout = 5 * time.Second
)

// Store is the lookup queue; satisfied by *psql.LookupRepository.
type Store interface {
	ClaimNext(ctx context.Context) (rss.Lookup, error) // rss.ErrNoRecord when empty
	Finish(ctx context.Context, id, siteID string) error
	Fail(ctx context.Context, id, msg string) error
	ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error)
}

// Saver stores a discovered site and its feeds; satisfied by
// *psql.DiscoveryRepository.
type Saver interface {
	Save(ctx context.Context, site rss.Site, feeds []rss.FeedWithArticles) (string, error)
}

// Discoverer finds a site's feeds; satisfied by *discovery.Discoverer.
type Discoverer interface {
	Discover(ctx context.Context, u *url.URL) (discovery.Result, error)
}

// Runner processes queued lookups with a pool of workers. Zero values use
// the defaults.
type Runner struct {
	Store        Store
	Saver        Saver
	Discoverer   Discoverer
	Logger       *slog.Logger
	Workers      int           // default 2
	PollInterval time.Duration // default 500ms
	JobTimeout   time.Duration // default 30s
	StaleAfter   time.Duration // default 2m (must exceed JobTimeout)
	MaxAttempts  int           // default 3
}

// Run resets stale jobs once, then runs Workers goroutines until ctx is
// canceled, and returns after they have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.StaleAfter <= r.JobTimeout {
		return fmt.Errorf("lookup: StaleAfter (%s) must exceed JobTimeout (%s)", r.StaleAfter, r.JobTimeout)
	}

	n, err := r.Store.ResetStale(ctx, r.StaleAfter, r.MaxAttempts)
	if err != nil {
		r.Logger.Error("lookup: reset stale jobs", "error", err)
	} else if n > 0 {
		r.Logger.Info("lookup: reset stale jobs", "count", n)
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
	if r.StaleAfter <= 0 {
		r.StaleAfter = defaultStaleAfter
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = defaultMaxAttempts
	}
}

func (r *Runner) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		job, err := r.Store.ClaimNext(ctx)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("lookup: claim next job", "error", err)
			}
			if !r.wait(ctx) {
				return
			}
			continue
		}

		r.process(ctx, job)
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

// process runs one job and records its outcome, unless ctx was canceled
// meanwhile: the row is then left running for ResetStale to re-queue.
func (r *Runner) process(ctx context.Context, job rss.Lookup) {
	start := time.Now()
	siteID, feeds, articles, err := r.run(ctx, job)

	if ctx.Err() != nil {
		r.Logger.Info("lookup interrupted by shutdown", "site_key", job.SiteKey)
		return
	}

	recCtx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()

	status := rss.LookupDone
	if err != nil {
		status = rss.LookupFailed
		r.Logger.Info("lookup failed", "site_key", job.SiteKey, "error", err)
		if ferr := r.Store.Fail(recCtx, job.ID, err.Error()); ferr != nil {
			r.Logger.Error("lookup: record failure", "site_key", job.SiteKey, "error", ferr)
		}
	} else if ferr := r.Store.Finish(recCtx, job.ID, siteID); ferr != nil {
		r.Logger.Error("lookup: record result", "site_key", job.SiteKey, "error", ferr)
	}

	r.Logger.Info("lookup finished",
		"site_key", job.SiteKey,
		"status", status,
		"feeds", feeds,
		"articles", articles,
		"duration", time.Since(start),
	)
}

// run discovers the job's feeds and saves them. It returns the saved site's
// ID ("" if no feeds were found), the number of feeds and the total number
// of articles saved with them. A panic is returned as an error.
func (r *Runner) run(ctx context.Context, job rss.Lookup) (siteID string, feeds, articles int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("lookup: panic", "site_key", job.SiteKey, "panic", rec)
			siteID, feeds, articles, err = "", 0, 0, fmt.Errorf("panic: %v", rec)
		}
	}()

	u, err := url.Parse(job.URL)
	if err != nil {
		return "", 0, 0, err
	}

	jobCtx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	defer cancel()

	res, err := r.Discoverer.Discover(jobCtx, u)
	if err != nil {
		return "", 0, 0, err
	}
	if len(res.Feeds) == 0 {
		return "", 0, 0, nil
	}

	site := rss.Site{
		Host:        res.Site.Key,
		URL:         res.Site.URL,
		Title:       res.Site.Title,
		Description: res.Site.Description,
	}
	fs := make([]rss.FeedWithArticles, len(res.Feeds))
	for i, f := range res.Feeds {
		fs[i] = rss.FeedWithArticles{
			Feed: rss.Feed{
				Url:         f.URL,
				SiteUrl:     f.SiteURL,
				Title:       f.Title,
				Description: f.Description,
				NextFetch:   ingest.NextFetch(time.Now()), // discovery just fetched it
			},
			Articles: ingest.Articles(f.Items),
		}
		articles += len(fs[i].Articles)
	}

	siteID, err = r.Saver.Save(jobCtx, site, fs)
	if err != nil {
		return "", 0, 0, err
	}
	return siteID, len(fs), articles, nil
}
