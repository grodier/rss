package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
)

// Fetcher is satisfied by *fetch.Client.
type Fetcher interface {
	Get(ctx context.Context, rawURL string) (*fetch.Response, error)
}

var _ Fetcher = (*fetch.Client)(nil)

// ErrUnreachable is returned by Discover when the looked-up URL answers with
// a non-2xx status.
var ErrUnreachable = errors.New("discovery: site returned an error status")

const (
	defaultMaxCandidates = 15
	defaultMaxFeeds      = 10
	defaultConcurrency   = 4
)

// SiteInfo describes the site a lookup ended up on.
type SiteInfo struct {
	Key         string // SiteKey of the final page URL (after redirects)
	URL         string // origin of the final page URL + "/", e.g. "https://www.example.com/"
	Title       string // PageInfo.Title, else feed title (direct-feed case), else Key
	Description string
}

// FeedInfo is a feed that was fetched and parsed successfully.
type FeedInfo struct {
	URL         string // CanonicalFeedURL of the feed's final URL (after redirects)
	Title       string // feedparse Meta.Title, else Candidate.Title, else URL
	Description string
	SiteURL     string // Meta.SiteURL (may be "")
}

// Result is what Discover found.
type Result struct {
	Site  SiteInfo
	Feeds []FeedInfo // may be empty: "site reachable, no feeds found" is not an error
}

// Discoverer finds the feeds of a site. Zero limits use the defaults.
type Discoverer struct {
	Fetcher       Fetcher
	MaxCandidates int // candidates fetched at most; default 15
	MaxFeeds      int // feeds returned at most; default 10
	Concurrency   int // parallel candidate fetches; default 4
}

// Discover fetches u (from ParseInput) and finds its feeds. The caller sets
// the overall deadline on ctx.
//
// Failing to fetch u, or a non-2xx status, is an error. An HTML page is never
// an error, however malformed or truncated: Discover uses whatever title,
// description and feed links it could read, and when the page advertises no
// feeds it tries CommonFeedPaths.
func (d *Discoverer) Discover(ctx context.Context, u *url.URL) (Result, error) {
	resp, err := d.Fetcher.Get(ctx, u.String())
	if err != nil {
		return Result{}, fmt.Errorf("discovery: fetching %s: %w", u, err)
	}
	if !ok2xx(resp.StatusCode) {
		return Result{}, fmt.Errorf("%w: %s: status %d", ErrUnreachable, u, resp.StatusCode)
	}

	// The input is itself a feed.
	if feedparse.Sniff(resp.Header.Get("Content-Type"), resp.Body) {
		if meta, err := feedparse.Parse(resp.URL, resp.Body); err == nil {
			feed := feedInfo(resp.URL, meta, "")
			siteURL := resp.URL
			if meta.SiteURL != "" {
				if su, err := url.Parse(meta.SiteURL); err == nil && su.Host != "" {
					siteURL = su
				}
			}
			site := SiteInfo{
				Key:         SiteKey(siteURL),
				URL:         origin(siteURL),
				Title:       meta.Title,
				Description: meta.Description,
			}
			if site.Title == "" {
				site.Title = site.Key
			}
			return Result{Site: site, Feeds: []FeedInfo{feed}}, nil
		}
	}

	// An HTML page.
	info := ParsePage(resp.URL, resp.Body)
	site := SiteInfo{
		Key:         SiteKey(resp.URL),
		URL:         origin(resp.URL),
		Title:       info.Title,
		Description: info.Description,
	}
	if site.Title == "" {
		site.Title = site.Key
	}

	candidates := info.Feeds
	if len(candidates) == 0 {
		base, _ := url.Parse(site.URL)
		for _, p := range CommonFeedPaths {
			ref, err := url.Parse(p)
			if err != nil {
				continue
			}
			candidates = append(candidates, Candidate{URL: base.ResolveReference(ref)})
		}
	}

	feeds, err := d.checkCandidates(ctx, candidates)
	if err != nil {
		return Result{}, err
	}
	return Result{Site: site, Feeds: feeds}, nil
}

// checkCandidates fetches and parses up to MaxCandidates candidates,
// Concurrency at a time, and returns the valid feeds in candidate order,
// deduplicated by canonical URL and capped at MaxFeeds.
func (d *Discoverer) checkCandidates(ctx context.Context, candidates []Candidate) ([]FeedInfo, error) {
	maxCandidates := orDefault(d.MaxCandidates, defaultMaxCandidates)
	maxFeeds := orDefault(d.MaxFeeds, defaultMaxFeeds)
	concurrency := orDefault(d.Concurrency, defaultConcurrency)

	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	// results[i] is the feed for candidates[i], or nil if it isn't valid.
	results := make([]*FeedInfo, len(candidates))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

loop:
	for i, c := range candidates {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = d.checkCandidate(ctx, c)
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discovery: checking feeds: %w", err)
	}

	var feeds []FeedInfo
	seen := make(map[string]bool)
	for _, f := range results {
		if f == nil || seen[f.URL] {
			continue
		}
		seen[f.URL] = true
		feeds = append(feeds, *f)
		if len(feeds) == maxFeeds {
			break
		}
	}
	return feeds, nil
}

// checkCandidate returns the feed at c, or nil if it can't be fetched or
// isn't a feed.
func (d *Discoverer) checkCandidate(ctx context.Context, c Candidate) *FeedInfo {
	resp, err := d.Fetcher.Get(ctx, c.URL.String())
	if err != nil || !ok2xx(resp.StatusCode) {
		return nil
	}
	meta, err := feedparse.Parse(resp.URL, resp.Body)
	if err != nil {
		return nil
	}
	f := feedInfo(resp.URL, meta, c.Title)
	return &f
}

// feedInfo builds the FeedInfo for a feed fetched from finalURL.
func feedInfo(finalURL *url.URL, meta feedparse.Meta, candidateTitle string) FeedInfo {
	f := FeedInfo{
		URL:         CanonicalFeedURL(finalURL),
		Title:       meta.Title,
		Description: meta.Description,
		SiteURL:     meta.SiteURL,
	}
	if f.Title == "" {
		f.Title = candidateTitle
	}
	if f.Title == "" {
		f.Title = f.URL
	}
	return f
}

// origin returns u's canonical scheme and host followed by "/".
func origin(u *url.URL) string {
	return CanonicalFeedURL(&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"})
}

func ok2xx(status int) bool {
	return status >= 200 && status < 300
}

func orDefault(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
