package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// ErrAppLink is returned by Discover for an address, or a result, on the
// app's own site (Discoverer.AppSiteKey): a link to the app, such as a shared
// timeline's, never becomes a feed in the catalog.
var ErrAppLink = errors.New("discovery: link to the app itself")

const (
	defaultMaxCandidates = 15
	defaultMaxFeeds      = 10
	defaultConcurrency   = 4
)

// SiteInfo describes the site a lookup ended up on.
type SiteInfo struct {
	Key         string // SiteKey of the final page URL (after redirects), or of its first feed's link (see Discover)
	URL         string // SiteRoot of the same URL, e.g. "https://www.example.com/" or "https://medium.com/@alice"
	Title       string // PageInfo.Title, else feed title (direct-feed case), else Key
	Description string
}

// FeedInfo is a feed that was fetched and parsed successfully.
type FeedInfo struct {
	URL         string // CanonicalFeedURL of the feed's final URL (after redirects)
	Title       string // feedparse Meta.Title, else Candidate.Title, else URL
	Description string
	SiteURL     string           // Meta.SiteURL (may be "")
	Categories  []string         // Meta.Categories
	Items       []feedparse.Item // the feed's items as parsed during discovery
}

// Result is what Discover found.
type Result struct {
	Site   SiteInfo
	Feeds  []FeedInfo // may be empty: "site reachable, no feeds found" is not an error
	Direct bool       // the looked-up URL was itself a feed; Checked is empty
	// HomePage says Site was read from the site's home page (the looked-up
	// URL or where it led), so it describes the site; otherwise it was read
	// from a feed or another page, and only stands in for a new site's.
	HomePage bool
	Checked  []CandidateCheck // every candidate, in order
}

// Sources of a candidate, as CandidateCheck.Source reports them.
const (
	SourcePage       = "page"        // a <link rel="alternate"> on the page
	SourceCommonPath = "common path" // CommonFeedPaths
	SourcePlatform   = "platform"    // the site's platform's own feeds (platformFeeds)
)

// CandidateCheck is the outcome of one candidate feed URL.
type CandidateCheck struct {
	Candidate Candidate
	Source    string // SourcePage, SourceCommonPath or SourcePlatform
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
	MaxCandidates int    // candidates fetched at most; default 15
	MaxFeeds      int    // feeds returned at most; default 10
	Concurrency   int    // parallel candidate fetches; default 4
	AppSiteKey    string // the app's own site key; addresses and results on it are refused ("" turns the check off)
}

// Discover fetches u (from ParseInput) and finds its feeds. The caller sets
// the overall deadline on ctx.
//
// An address on AppSiteKey is refused with ErrAppLink before it is fetched,
// and so is a result on it (u redirected there, or a feed declares it as its
// site).
//
// Failing to fetch u is an error. An HTML page is never an error, however
// malformed or truncated: Discover uses whatever title, description and feed
// links it could read. When the page advertises no feeds it tries
// CommonFeedPaths, or, when its site key has a path (a site on a shared
// host, see SiteKey), the feeds its platform publishes for the site
// (platformFeeds) instead, since common paths would find the platform's own.
//
// A non-2xx status is ErrUnreachable, unless u's site key has a path and one
// of its platform's feeds parses (a platform's page can refuse servers while
// its feeds answer): the result is then the feeds found, as if the first had
// been looked up, with HomePage false.
//
// The site is the page's (SiteKey and SiteRoot of where u led), or for a
// feed, that of its link, else of the feed itself. A page whose key has a
// path takes the key of its first feed's link instead when that is another
// key with a path on the same host. A site on a shared host is named as it
// names itself (OwnName); its platform is shown apart (Platform).
//
// Result.Checked records every candidate. If ctx ends while candidates are
// being checked, Discover returns the Site and the Checked so far alongside
// the error, and so does a non-2xx status whose platform feeds were tried;
// the lookup and import runners return on any error without reading the
// result.
func (d *Discoverer) Discover(ctx context.Context, u *url.URL) (Result, error) {
	if d.isAppSite(SiteKey(u)) {
		return Result{}, fmt.Errorf("%w: %s", ErrAppLink, u)
	}
	res, err := d.discover(ctx, u)
	if err == nil && d.isAppSite(res.Site.Key) {
		return Result{}, fmt.Errorf("%w: %s led to %s", ErrAppLink, u, res.Site.URL)
	}
	return res, err
}

// isAppSite reports whether key is on the app's own site, with a path
// ("paperround.app/@x") or without.
func (d *Discoverer) isAppSite(key string) bool {
	return d.AppSiteKey != "" && KeyHost(key) == d.AppSiteKey
}

// discover is Discover without the AppSiteKey checks.
func (d *Discoverer) discover(ctx context.Context, u *url.URL) (Result, error) {
	resp, err := d.Fetcher.Get(ctx, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("discovery: fetching %s: %w", u, err)
	}
	if !ok2xx(resp.StatusCode) {
		return d.refused(ctx, u, resp.StatusCode)
	}

	// The input is itself a feed.
	if feedparse.Sniff(resp.Header.Get("Content-Type"), resp.Body) {
		if f, err := feedparse.Parse(resp.URL, resp.Body); err == nil {
			site := siteFromFeed(resp.URL, f.SiteURL, f.Title, f.Description)
			return Result{Site: site, Feeds: []FeedInfo{feedInfo(resp.URL, f, "")}, Direct: true}, nil
		}
	}

	// An HTML page.
	info := ParsePage(resp.URL, resp.Body)
	site := SiteInfo{
		Key:         SiteKey(resp.URL),
		URL:         SiteRoot(resp.URL).String(),
		Title:       info.Title,
		Description: info.Description,
	}
	if site.Title == "" {
		site.Title = site.Key
	}

	// A site with a path in its key is one of many on a shared host, whose
	// common paths would find the platform's own feed (medium.com/feed);
	// it tries the feeds its platform publishes for it instead.
	candidates, source := info.Feeds, SourcePage
	if len(candidates) == 0 {
		base, _ := url.Parse(site.URL)
		if hasPath(site.Key) {
			source = SourcePlatform
			candidates = platformCandidates(base, site.Key)
		} else {
			source = SourceCommonPath
			for _, p := range CommonFeedPaths {
				ref, err := url.Parse(p)
				if err != nil {
					continue
				}
				candidates = append(candidates, Candidate{URL: base.ResolveReference(ref)})
			}
		}
	}

	feeds, checked, err := d.checkCandidates(ctx, candidates, source)
	if len(feeds) > 0 {
		site = feedSite(site, feeds[0])
	}
	site.Title = OwnName(site.Key, site.Title, false)
	if err != nil {
		return Result{Site: site, Checked: checked}, err
	}
	return Result{Site: site, Feeds: feeds, Checked: checked, HomePage: IsHomePage(u) || IsHomePage(resp.URL)}, nil
}

// refused is discover's result for u, whose page answered with a non-2xx
// status: the feeds its platform publishes for its site, when any parses,
// with the site found from the first as if it had been looked up (Direct
// and HomePage false, since the page itself wasn't read); else
// ErrUnreachable, with the candidates checked, if any. Only the page's
// status is retried this way: a page that couldn't be fetched at all means
// the host is likely down. Neither is a u that is one of those feeds: it
// was asked for, not its site, so another of the site's feeds (an import's
// releases.atom answered by tags.atom) mustn't stand in for it.
func (d *Discoverer) refused(ctx context.Context, u *url.URL, status int) (Result, error) {
	unreachable := fmt.Errorf("%w: %s: status %d", ErrUnreachable, u, status)
	candidates := platformCandidates(SiteRoot(u), SiteKey(u))
	if len(candidates) == 0 || isCandidate(u, candidates) {
		return Result{}, unreachable
	}
	feeds, checked, err := d.checkCandidates(ctx, candidates, SourcePlatform)
	if err != nil {
		return Result{Checked: checked}, err
	}
	if len(feeds) == 0 {
		return Result{Checked: checked}, unreachable
	}
	first := feeds[0]
	feedURL, err := url.Parse(first.URL)
	if err != nil {
		return Result{Checked: checked}, fmt.Errorf("discovery: feed %s: %w", first.URL, err)
	}
	// A platform's candidate has no title of its own, so a feed titled by
	// its URL has none (feedInfo).
	title := first.Title
	if title == first.URL {
		title = ""
	}
	site := siteFromFeed(feedURL, first.SiteURL, title, first.Description)
	return Result{Site: site, Feeds: feeds, Checked: checked}, nil
}

// platformCandidates are the feeds the platform publishes for the site with
// root and key (platformFeeds), as candidates.
func platformCandidates(root *url.URL, key string) []Candidate {
	var candidates []Candidate
	for _, raw := range platformFeeds(root, key) {
		if c, err := url.Parse(raw); err == nil {
			candidates = append(candidates, Candidate{URL: c})
		}
	}
	return candidates
}

// isCandidate reports whether u is one of candidates, ignoring the
// differences CanonicalFeedURL ignores.
func isCandidate(u *url.URL, candidates []Candidate) bool {
	want := CanonicalFeedURL(u)
	for _, c := range candidates {
		if CanonicalFeedURL(c.URL) == want {
			return true
		}
	}
	return false
}

// siteFromFeed is the site of a feed found at feedURL whose link to its site
// is link: that of its link (channelLink), else of the feed itself, named by
// the feed's title as the site names itself (OwnName), else by its key.
func siteFromFeed(feedURL *url.URL, link, title, description string) SiteInfo {
	siteURL := feedURL
	if su, ok := channelLink(link); ok {
		siteURL = su
	}
	site := SiteInfo{
		Key:         SiteKey(siteURL),
		URL:         SiteRoot(siteURL).String(),
		Title:       title,
		Description: description,
	}
	if site.Title == "" {
		site.Title = site.Key
	}
	site.Title = OwnName(site.Key, site.Title, true)
	return site
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
		Categories:  parsed.Categories,
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

// channelLink parses a feed's link to its site (feedparse.Meta.SiteURL)
// as a site's address: ok is false when it has no host or its host isn't a
// valid domain name (validHost), and the feed's own address stands in. A
// feed can't name a site discovery wouldn't accept as input: "www.newsletters"
// would key to rss.NewsletterSiteHost.
func channelLink(raw string) (u *url.URL, ok bool) {
	if raw == "" {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !validHost(strings.ToLower(u.Hostname())) {
		return nil, false
	}
	return u, true
}

// feedSite returns site, a page's, with the key and root of the site that
// feed, the first found on the page, links to when both keys have a path on
// the same host but differ: a YouTube /@handle page's feed links to its
// /channel/ID, so a channel is one site whichever address was looked up.
// Any other site is returned as it is.
func feedSite(site SiteInfo, feed FeedInfo) SiteInfo {
	if !hasPath(site.Key) || feed.SiteURL == "" {
		return site
	}
	su, err := url.Parse(feed.SiteURL)
	if err != nil || su.Host == "" || (su.Scheme != "http" && su.Scheme != "https") {
		return site
	}
	key := SiteKey(su)
	if key == site.Key || !hasPath(key) || KeyHost(key) != KeyHost(site.Key) {
		return site
	}
	if site.Title == site.Key {
		site.Title = key
	}
	site.Key, site.URL = key, SiteRoot(su).String()
	return site
}

// hasPath reports whether a site key has a path: the site is one of many on
// a shared host ("medium.com/@alice").
func hasPath(key string) bool {
	return strings.Contains(key, "/")
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
