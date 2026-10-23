// Package enrich fetches the web pages of articles whose feed left out their
// image, title or excerpt, and stores what the pages' Open Graph and other
// meta tags say, in the background.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers      = 1
	defaultPollInterval = 5 * time.Second
	defaultJobTimeout   = 20 * time.Second
	defaultLease        = 2 * time.Minute

	// MaxAttempts is how many times a job is tried before it is given up.
	MaxAttempts = 3

	// recordTimeout bounds recording a job's outcome.
	recordTimeout = 5 * time.Second
)

// Store hands out enrichment jobs and records their outcomes; satisfied by
// *psql.EnrichRepository.
type Store interface {
	ClaimNext(ctx context.Context, lease time.Duration) (rss.EnrichJob, error) // rss.ErrNoRecord when none is due
	SavePage(ctx context.Context, articleID string, p rss.ArticlePage) error
	Retry(ctx context.Context, articleID, msg string, next time.Time) error
	GiveUp(ctx context.Context, articleID, msg string) error
}

// Fetcher fetches a URL; satisfied by *fetch.Client.
type Fetcher interface {
	Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error)
}

// Runner fetches due articles' web pages with a pool of workers. Zero values
// use the defaults.
type Runner struct {
	Store        Store
	Fetcher      Fetcher
	Logger       *slog.Logger
	Workers      int           // default 1
	PollInterval time.Duration // default 5s; wait after finding nothing due
	JobTimeout   time.Duration // default 20s
	Lease        time.Duration // default 2m (must exceed JobTimeout)
}

// Run runs Workers goroutines until ctx is canceled and returns after they
// have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.Lease <= r.JobTimeout {
		return fmt.Errorf("enrich: Lease (%s) must exceed JobTimeout (%s)", r.Lease, r.JobTimeout)
	}

	var wg sync.WaitGroup
	for range r.Workers {
		wg.Go(func() { r.work(ctx) })
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

		job, err := r.Store.ClaimNext(ctx, r.Lease)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("enrich: claim job", "error", err)
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

// Outcomes of a job, as logged.
const (
	outcomeSaved  = "saved"
	outcomeRetry  = "retry"
	outcomeGaveUp = "gave up"
)

// result is what an attempt at a job found.
type result struct {
	outcome string
	page    rss.ArticlePage // when saved
	next    time.Time       // when retried
	err     error           // why it was retried or given up
}

// process fetches job's page, records the outcome and logs it. Nothing is
// recorded if ctx is canceled: the lease retries the job. A panic is logged
// and swallowed, and the lease retries the job too.
func (r *Runner) process(ctx context.Context, job rss.EnrichJob) {
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("enrich: panic", "article_id", job.ArticleID, "panic", rec)
		}
	}()

	start := time.Now()
	jobCtx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	res := r.attempt(jobCtx, job)
	cancel()
	if ctx.Err() != nil {
		return
	}

	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	var err error
	switch res.outcome {
	case outcomeSaved:
		err = r.Store.SavePage(recCtx, job.ArticleID, res.page)
	case outcomeRetry:
		err = r.Store.Retry(recCtx, job.ArticleID, res.err.Error(), res.next)
	default:
		err = r.Store.GiveUp(recCtx, job.ArticleID, res.err.Error())
	}
	host := urlHost(job.URL)
	if err != nil {
		r.Logger.Error("enrich: record outcome", "article_id", job.ArticleID, "host", host, "error", err)
		return
	}

	attrs := []any{"article_id", job.ArticleID, "host", host, "outcome", res.outcome, "attempts", job.Attempts}
	if res.err != nil {
		attrs = append(attrs, "error", res.err)
	}
	attrs = append(attrs, "duration", time.Since(start))
	r.Logger.Info("article enriched", attrs...)
}

// attempt fetches job.URL and reads the page's metadata. A network error,
// 429 or 5xx is retried (see retryAt) until the job's MaxAttempts-th
// attempt; any other non-2xx response, or one that isn't HTML, is given up.
func (r *Runner) attempt(ctx context.Context, job rss.EnrichJob) result {
	resp, err := r.Fetcher.Get(ctx, job.URL, nil)
	if err != nil {
		if !transient(err) {
			return result{outcome: outcomeGaveUp, err: err}
		}
		return r.retry(job, nil, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return r.retry(job, resp, fmt.Errorf("status %d", resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return result{outcome: outcomeGaveUp, err: fmt.Errorf("status %d", resp.StatusCode)}
	}
	if !isHTML(resp) {
		return result{outcome: outcomeGaveUp, err: fmt.Errorf("not HTML: %q", resp.Header.Get("Content-Type"))}
	}

	info := discovery.ParsePage(resp.URL, resp.Body)
	return result{outcome: outcomeSaved, page: rss.ArticlePage{
		ImageURL:    info.Image,
		Title:       info.PageTitle,
		Description: rss.Shorten(strings.Join(strings.Fields(info.Description), " "), ingest.ExcerptLen),
	}}
}

// retry returns a retry of job after a failure with err, or gives it up
// after its MaxAttempts-th attempt. resp is the response, nil if there was
// none.
func (r *Runner) retry(job rss.EnrichJob, resp *fetch.Response, err error) result {
	if job.Attempts >= MaxAttempts {
		return result{outcome: outcomeGaveUp, err: err}
	}
	return result{outcome: outcomeRetry, next: retryAt(time.Now(), job.Attempts, resp), err: err}
}

// retryAt returns when a job whose attempt-th attempt failed at now is tried
// again: 1 hour after the first attempt and 6 hours after later ones, or
// later if a 429's or 503's Retry-After says so (capped at
// ingest.MaxRetryDelay).
func retryAt(now time.Time, attempt int, resp *fetch.Response) time.Time {
	d := time.Hour
	if attempt > 1 {
		d = 6 * time.Hour
	}
	if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
		d = max(d, min(ingest.RetryAfter(resp.Header, now), ingest.MaxRetryDelay))
	}
	return now.Add(d)
}

// transient reports whether a fetch error may not last: a network error,
// rather than a response or address the fetch client refuses.
func transient(err error) bool {
	return !errors.Is(err, fetch.ErrTooLarge) &&
		!errors.Is(err, fetch.ErrBlockedAddress) &&
		!errors.Is(err, fetch.ErrUnsupportedScheme) &&
		!errors.Is(err, fetch.ErrTooManyRedirects)
}

// isHTML reports whether a response is an HTML page by its Content-Type, or
// by its bytes when it has none.
func isHTML(resp *fetch.Response) bool {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(resp.Body)
	}
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "text/html" || mt == "application/xhtml+xml")
}

// urlHost returns rawURL's host, for logs; "" if it doesn't parse.
func urlHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}
