package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
)

// Fetcher is satisfied by *fetch.Client.
type Fetcher interface {
	Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error)
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
	SiteURL     string           // Meta.SiteURL (may be "")
	Items       []feedparse.Item // the feed's items as parsed during discovery
}

// Result is what Discover found.
type Result struct {
	Site    SiteInfo
	Feeds   []FeedInfo       // may be empty: "site reachable, no feeds found" is not an error
	Direct  bool             // the looked-up URL was itself a feed; Checked is empty
	Checked []CandidateCheck // every candidate, in order
}

// Sources of a candidate, as CandidateCheck.Source reports them.
const (
	SourcePage       = "page"        // a <link rel="alternate"> on the page
	SourceCommonPath = "common path" // CommonFeedPaths
)

// CandidateCheck is the outcome of one candidate feed URL.
type CandidateCheck struct {
	Candidate Candidate
	Source    string // "page" (a <link rel="alternate"> on the page) or "common path" (CommonFeedPaths)
	FinalURL  string // after redirects; "" if the request failed or wasn't made
	Status    int    // 0 if the request failed or wasn't made
	Feed      string // the FeedInfo.URL it became; "" if not a feed
	Outcome   string // "feed", "duplicate" (an earlier candidate had its URL), "over the feed limit", "not fetched (over the candidate limit)", "not fetched (deadline)", or why it isn't a feed: the request error, "status 404", or the parse error
}

// Outcomes of a CandidateCheck, besides why a candidate isn't a feed.
const (
	OutcomeFeed           = "feed"
	OutcomeDuplicate      = "duplicate"
	OutcomeOverFeedLimit  = "over the feed limit"
	OutcomeOverCandidates = "not fetched (over the candidate limit)"
	OutcomeDeadline       = "not fetched (deadline)"
)

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
//
// Result.Checked records every candidate. If ctx ends while candidates are
// being checked, Discover returns the Site and the Checked so far alongside
// the error; the lookup and import runners return on any error without
// reading the result.
func (d *Discoverer) Discover(ctx context.Context, u *url.URL) (Result, error) {
	resp, err := d.Fetcher.Get(ctx, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("discovery: fetching %s: %w", u, err)
	}
	if !ok2xx(resp.StatusCode) {
		return Result{}, fmt.Errorf("%w: %s: status %d", ErrUnreachable, u, resp.StatusCode)
	}

	// The input is itself a feed.
	if feedparse.Sniff(resp.Header.Get("Content-Type"), resp.Body) {
		if f, err := feedparse.Parse(resp.URL, resp.Body); err == nil {
			feed := feedInfo(resp.URL, f, "")
			siteURL := resp.URL
			if f.SiteURL != "" {
				if su, err := url.Parse(f.SiteURL); err == nil && su.Host != "" {
					siteURL = su
				}
			}
			site := SiteInfo{
				Key:         SiteKey(siteURL),
				URL:         origin(siteURL),
				Title:       f.Title,
				Description: f.Description,
			}
			if site.Title == "" {
				site.Title = site.Key
			}
			return Result{Site: site, Feeds: []FeedInfo{feed}, Direct: true}, nil
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

	candidates, source := info.Feeds, SourcePage
	if len(candidates) == 0 {
		source = SourceCommonPath
		base, _ := url.Parse(site.URL)
		for _, p := range CommonFeedPaths {
			ref, err := url.Parse(p)
			if err != nil {
				continue
			}
			candidates = append(candidates, Candidate{URL: base.ResolveReference(ref)})
		}
	}

	feeds, checked, err := d.checkCandidates(ctx, candidates, source)
	if err != nil {
		return Result{Site: site, Checked: checked}, err
	}
	return Result{Site: site, Feeds: feeds, Checked: checked}, nil
}

// checkCandidates fetches and parses up to MaxCandidates candidates,
// Concurrency at a time, and returns the valid feeds in candidate order,
// deduplicated by canonical URL and capped at MaxFeeds, and the outcome of
// every candidate. On an error (ctx ended) it still returns the outcomes,
// with the candidates it didn't fetch marked OutcomeDeadline.
func (d *Discoverer) checkCandidates(ctx context.Context, candidates []Candidate, source string) ([]FeedInfo, []CandidateCheck, error) {
	maxCandidates := orDefault(d.MaxCandidates, defaultMaxCandidates)
	maxFeeds := orDefault(d.MaxFeeds, defaultMaxFeeds)
	concurrency := orDefault(d.Concurrency, defaultConcurrency)

	checked := make([]CandidateCheck, len(candidates))
	for i, c := range candidates {
		checked[i] = CandidateCheck{Candidate: c, Source: source, Outcome: OutcomeDeadline}
		if i >= maxCandidates {
			checked[i].Outcome = OutcomeOverCandidates
		}
	}
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	// results[i] is the feed for candidates[i], or nil if it isn't valid.
	results := make([]*FeedInfo, len(candidates))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

loop:
	for i := range candidates {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		if ctx.Err() != nil {
			// Both cases were ready, and select picked the semaphore.
			<-sem
			break loop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = d.checkCandidate(ctx, &checked[i])
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, checked, fmt.Errorf("discovery: checking feeds: %w", err)
	}

	var feeds []FeedInfo
	seen := make(map[string]bool)
	for i, f := range results {
		switch {
		case f == nil:
		case seen[f.URL]:
			checked[i].Outcome = OutcomeDuplicate
		case len(feeds) == maxFeeds:
			checked[i].Outcome = OutcomeOverFeedLimit
		default:
			seen[f.URL] = true
			feeds = append(feeds, *f)
		}
	}
	return feeds, checked, nil
}

// checkCandidate returns the feed at check's candidate, or nil if it can't
// be fetched or isn't a feed, and fills in check.
func (d *Discoverer) checkCandidate(ctx context.Context, check *CandidateCheck) *FeedInfo {
	c := check.Candidate
	resp, err := d.Fetcher.Get(ctx, c.URL.String(), nil)
	if err != nil {
		check.Outcome = err.Error()
		return nil
	}
	if resp.URL != nil {
		check.FinalURL = resp.URL.String()
	}
	check.Status = resp.StatusCode
	if !ok2xx(resp.StatusCode) {
		check.Outcome = fmt.Sprintf("status %d", resp.StatusCode)
		return nil
	}
	parsed, err := feedparse.Parse(resp.URL, resp.Body)
	if err != nil {
		check.Outcome = err.Error()
		return nil
	}
	f := feedInfo(resp.URL, parsed, c.Title)
	check.Feed, check.Outcome = f.URL, OutcomeFeed
	return &f
}

// feedInfo builds the FeedInfo for a feed fetched from finalURL.
func feedInfo(finalURL *url.URL, parsed feedparse.Feed, candidateTitle string) FeedInfo {
	f := FeedInfo{
		URL:         CanonicalFeedURL(finalURL),
		Title:       parsed.Title,
		Description: parsed.Description,
		SiteURL:     parsed.SiteURL,
		Items:       parsed.Items,
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
