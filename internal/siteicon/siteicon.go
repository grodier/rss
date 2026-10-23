// Package siteicon finds, checks and stores sites' icons in the background.
package siteicon

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // registered for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers      = 1
	defaultPollInterval = 5 * time.Second
	defaultJobTimeout   = 30 * time.Second
	defaultLease        = 5 * time.Minute

	// MaxBytes is the largest icon kept; give the IconFetcher's client this
	// as its fetch.Options.MaxBodyBytes.
	MaxBytes = 256 << 10
	// MinSize is the smallest an icon's shorter side may be.
	MinSize = 32
	// maxCandidates is how many icons a check downloads at most.
	maxCandidates = 4

	// RecheckInterval is how long after a check that reached the site its
	// icon is checked again (±10% jitter).
	RecheckInterval = 30 * 24 * time.Hour
	// RetryDelay is how long after a failed check it is tried again.
	RetryDelay = 24 * time.Hour
)

// Store hands out sites whose icon is due and records the outcome of their
// checks; satisfied by *psql.SiteRepository.
type Store interface {
	ClaimIconCheck(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error) // rss.ErrNoRecord when none is due
	SaveIcon(ctx context.Context, icon rss.SiteIcon, next time.Time) error
	RemoveIcon(ctx context.Context, siteID string, next time.Time) error
	KeepIcon(ctx context.Context, siteID string, checked bool, next time.Time) error
}

// Fetcher fetches a URL; satisfied by *fetch.Client.
type Fetcher interface {
	Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error)
}

// Runner checks due sites' icons with a pool of workers. Zero values use the
// defaults.
type Runner struct {
	Store        Store
	Fetcher      Fetcher // fetches home pages
	IconFetcher  Fetcher // downloads icons; its responses are capped at MaxBytes
	Logger       *slog.Logger
	Workers      int           // default 1
	PollInterval time.Duration // default 5s; wait after finding nothing due
	JobTimeout   time.Duration // default 30s
	Lease        time.Duration // default 5m (must exceed JobTimeout)
}

// Run runs Workers goroutines until ctx is canceled and returns after they
// have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.Lease <= r.JobTimeout {
		return fmt.Errorf("siteicon: Lease (%s) must exceed JobTimeout (%s)", r.Lease, r.JobTimeout)
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

		site, current, err := r.Store.ClaimIconCheck(ctx, r.Lease)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("site icon: claim icon check", "error", err)
			}
			if !r.wait(ctx) {
				return
			}
			continue
		}

		r.check(ctx, site, current)
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

// Outcomes of a check, as logged.
const (
	outcomeSaved     = "saved"
	outcomeUnchanged = "unchanged"
	outcomeRemoved   = "removed"
	outcomeFailed    = "failed"
)

// result is what a check found.
type result struct {
	outcome string
	icon    rss.SiteIcon // when saved
	err     error        // why it failed
}

// check finds site's icon, records the outcome and logs it. current is the
// site's stored icon (zero if none). Nothing is recorded if ctx is canceled:
// the lease retries the site. A panic is logged and swallowed, and the lease
// retries the site too.
func (r *Runner) check(ctx context.Context, site rss.Site, current rss.SiteIcon) {
	defer func() {
		if rec := recover(); rec != nil {
			r.Logger.Error("site icon: panic", "site_id", site.ID, "panic", rec)
		}
	}()

	start := time.Now()
	jobCtx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	res := r.find(jobCtx, site, current)
	cancel()
	if ctx.Err() != nil {
		return
	}

	now := time.Now()
	var err error
	switch res.outcome {
	case outcomeSaved:
		res.icon.SiteID = site.ID
		err = r.Store.SaveIcon(ctx, res.icon, nextCheck(now))
	case outcomeUnchanged:
		err = r.Store.KeepIcon(ctx, site.ID, true, nextCheck(now))
	case outcomeRemoved:
		err = r.Store.RemoveIcon(ctx, site.ID, nextCheck(now))
	default:
		err = r.Store.KeepIcon(ctx, site.ID, false, now.Add(RetryDelay))
	}
	if err != nil {
		if ctx.Err() == nil {
			r.Logger.Error("site icon: record check", "site_id", site.ID, "host", site.Host, "error", err)
		}
		return
	}

	attrs := []any{"site_id", site.ID, "host", site.Host, "outcome", res.outcome}
	if res.outcome == outcomeSaved {
		attrs = append(attrs, "size", fmt.Sprintf("%dx%d", res.icon.Width, res.icon.Height), "url", res.icon.SourceURL)
	}
	if res.err != nil {
		attrs = append(attrs, "error", res.err)
	}
	attrs = append(attrs, "duration", time.Since(start))
	r.Logger.Info("site icon checked", attrs...)
}

// find fetches site's home page and downloads the icons it declares, in
// order of preference, until one is usable.
func (r *Runner) find(ctx context.Context, site rss.Site, current rss.SiteIcon) result {
	page, err := r.Fetcher.Get(ctx, site.URL, nil)
	if err != nil {
		return result{outcome: outcomeFailed, err: fmt.Errorf("home page: %w", err)}
	}
	if page.StatusCode < 200 || page.StatusCode > 299 {
		return result{outcome: outcomeFailed, err: fmt.Errorf("home page: status %d", page.StatusCode)}
	}

	var declared []discovery.Icon
	if isHTML(page) {
		declared = discovery.ParsePage(page.URL, page.Body).Icons
	}

	unsure := false // a candidate failed in a way that may not last
	for _, u := range candidates(page.URL, declared) {
		var header http.Header
		if current.SourceURL != "" && u == current.SourceURL {
			header = validators(current)
		}
		resp, err := r.IconFetcher.Get(ctx, u, header)
		if err != nil {
			if ctx.Err() != nil {
				return result{outcome: outcomeFailed, err: err}
			}
			unsure = unsure || transient(err)
			continue
		}
		if header != nil && resp.StatusCode == http.StatusNotModified {
			return result{outcome: outcomeUnchanged}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			unsure = unsure || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
			continue
		}
		if icon, ok := usable(resp); ok {
			return result{outcome: outcomeSaved, icon: icon}
		}
	}
	if unsure {
		return result{outcome: outcomeFailed, err: errors.New("no usable icon, and some candidates could not be fetched")}
	}
	return result{outcome: outcomeRemoved}
}

// isHTML reports whether a response is an HTML page, by its Content-Type, or
// by its bytes when it has none.
func isHTML(resp *fetch.Response) bool {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(resp.Body)
	}
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "text/html" || mt == "application/xhtml+xml")
}

// candidates returns the URLs to try, best first, deduplicated and at most
// maxCandidates: apple-touch icons, largest declared size first; then icon
// links that aren't SVG; then /apple-touch-icon.png and /favicon.ico on
// pageURL's origin. Declared icons are ordered by size with unknown sizes
// last, and skipped when they declare a size under MinSize.
func candidates(pageURL *url.URL, declared []discovery.Icon) []string {
	var apple, other []discovery.Icon
	for _, ic := range declared {
		if (ic.Size > 0 && ic.Size < MinSize) || isSVG(ic) {
			continue
		}
		if ic.Apple {
			apple = append(apple, ic)
		} else {
			other = append(other, ic)
		}
	}
	bySize := func(a, b discovery.Icon) int {
		if (a.Size == 0) != (b.Size == 0) {
			if a.Size == 0 {
				return 1
			}
			return -1
		}
		return cmp.Compare(b.Size, a.Size)
	}
	slices.SortStableFunc(apple, bySize)
	slices.SortStableFunc(other, bySize)

	var urls []string
	for _, ic := range append(apple, other...) {
		urls = append(urls, ic.URL.String())
	}
	origin := &url.URL{Scheme: pageURL.Scheme, Host: pageURL.Host}
	urls = append(urls,
		origin.JoinPath("apple-touch-icon.png").String(),
		origin.JoinPath("favicon.ico").String(),
	)

	urls = dedupe(urls)
	return urls[:min(len(urls), maxCandidates)]
}

// dedupe returns urls without repeats, keeping the first of each.
func dedupe(urls []string) []string {
	seen := make(map[string]bool, len(urls))
	var out []string
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func isSVG(ic discovery.Icon) bool {
	return ic.Type == "image/svg+xml" || strings.HasSuffix(strings.ToLower(ic.URL.Path), ".svg")
}

// validators returns the conditional request headers for re-downloading
// the current icon.
func validators(current rss.SiteIcon) http.Header {
	h := http.Header{}
	if current.ETag != "" {
		h.Set("If-None-Match", current.ETag)
	}
	if current.LastModified != "" {
		h.Set("If-Modified-Since", current.LastModified)
	}
	return h
}

// transient reports whether a download error may not last: a network
// error, rather than a response the fetch client refuses.
func transient(err error) bool {
	return !errors.Is(err, fetch.ErrTooLarge) &&
		!errors.Is(err, fetch.ErrBlockedAddress) &&
		!errors.Is(err, fetch.ErrUnsupportedScheme) &&
		!errors.Is(err, fetch.ErrTooManyRedirects)
}

// usable returns the downloaded icon if it is a PNG, JPEG, GIF or ICO (by
// its bytes, not its Content-Type) of at most MaxBytes, at least MinSize on
// its shorter side and at most twice as long as it is wide, or wide as it
// is long.
func usable(resp *fetch.Response) (rss.SiteIcon, bool) {
	data := resp.Body
	if len(data) > MaxBytes {
		return rss.SiteIcon{}, false
	}

	var (
		contentType   string
		width, height int
	)
	if bytes.HasPrefix(data, icoSignature) {
		w, h, err := icoSize(data)
		if err != nil {
			return rss.SiteIcon{}, false
		}
		contentType, width, height = "image/x-icon", w, h
	} else {
		switch ct := http.DetectContentType(data); ct {
		case "image/png", "image/jpeg", "image/gif":
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return rss.SiteIcon{}, false
			}
			contentType, width, height = ct, cfg.Width, cfg.Height
		default:
			return rss.SiteIcon{}, false
		}
	}

	short, long := min(width, height), max(width, height)
	if short < MinSize || long > 2*short {
		return rss.SiteIcon{}, false
	}

	sum := sha256.Sum256(data)
	return rss.SiteIcon{
		SourceURL:    resp.URL.String(),
		ContentType:  contentType,
		Width:        width,
		Height:       height,
		Data:         data,
		Hash:         hex.EncodeToString(sum[:]),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, true
}

// nextCheck returns when a site checked at now is checked again:
// now + RecheckInterval, ±10% jitter.
func nextCheck(now time.Time) time.Time {
	return now.Add(time.Duration(float64(RecheckInterval) * (0.9 + 0.2*rand.Float64())))
}
