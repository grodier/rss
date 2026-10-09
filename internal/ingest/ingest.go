// Package ingest fetches feeds and turns their items into articles.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/rss"
)

// MaxArticles is the most articles kept from one fetch of a feed.
const MaxArticles = 200

// ExcerptLen is the most runes an article's excerpt has, not counting
// the trailing "…".
const ExcerptLen = 300

// excerpt returns plain text from summary, else content (both raw HTML),
// shortened to ExcerptLen runes at a word boundary with "…" appended.
func excerpt(summary, content string) string {
	text := feedparse.HTMLToText(summary)
	if text == "" {
		text = feedparse.HTMLToText(content)
	}
	return rss.Shorten(text, ExcerptLen)
}

// Articles converts parsed items to articles, in document order: each gets
// an ExternalID (see externalID), items with a duplicate ExternalID after
// the first are dropped, and at most MaxArticles are returned. FeedID, ID
// and the timestamps are left for the caller and the database.
func Articles(items []feedparse.Item) []rss.Article {
	articles := []rss.Article{}
	seen := make(map[string]bool)
	for _, it := range items {
		if len(articles) == MaxArticles {
			break
		}
		id := externalID(it)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		articles = append(articles, rss.Article{
			ExternalID:  id,
			URL:         it.URL,
			Title:       it.Title,
			Summary:     it.Summary,
			Content:     it.Content,
			Excerpt:     excerpt(it.Summary, it.Content),
			PublishedAt: it.Published,
		})
	}
	return articles
}

// externalID returns an item's identity within its feed: its ID, else its
// URL, else "sha256:" and the hex SHA-256 of its title, summary and content
// joined with "\x00". It returns "" for an item with none of these.
func externalID(it feedparse.Item) string {
	switch {
	case it.ID != "":
		return it.ID
	case it.URL != "":
		return it.URL
	case it.Title == "" && it.Summary == "" && it.Content == "":
		return ""
	}
	sum := sha256.Sum256([]byte(it.Title + "\x00" + it.Summary + "\x00" + it.Content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Fetcher is satisfied by *fetch.Client.
type Fetcher interface {
	Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error)
}

var _ Fetcher = (*fetch.Client)(nil)

// Store records fetch outcomes; satisfied by *psql.FeedRepository.
type Store interface {
	SaveFetch(ctx context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error)
	RecordFailure(ctx context.Context, id, msg string, next time.Time) error
	RecordNotModified(ctx context.Context, id string, next time.Time) error
	MarkGone(ctx context.Context, id string) error
}

// recordTimeout bounds recording a failed or gone fetch, which uses a context that
// outlives ctx's deadline so a fetch that timed out is still recorded.
const recordTimeout = 5 * time.Second

var (
	// ErrUnreachable: the feed couldn't be fetched (network error, timeout,
	// blocked address, too large) or answered with a non-2xx status.
	ErrUnreachable = errors.New("ingest: feed unreachable")
	// ErrNotFeed: the feed URL answered, but not with a feed.
	ErrNotFeed = errors.New("ingest: not a feed")
	// ErrGone: the feed answered 410 Gone and is no longer fetched.
	ErrGone = errors.New("ingest: feed gone")
)

// Refresher fetches feeds and saves their articles.
type Refresher struct {
	Fetcher Fetcher
	Store   Store
}

// Refresh fetches feed.Url, parses it and saves its metadata, validators
// (ETag, Last-Modified) and articles, scheduling the feed's next fetch (see
// NextFetch, delayed until the response is no longer fresh per Cache-Control
// max-age or Expires, up to MaxRetryDelay). It sends the feed's stored
// validators, and when the server answers 304 it records that with
// Store.RecordNotModified, without parsing, and returns a result with
// NotModified set. The caller sets the deadline on ctx. Fetch failures wrap
// ErrUnreachable and parse failures ErrNotFeed; both are recorded with
// Store.RecordFailure and reschedule the feed with backoff (see RetryAt,
// delayed by a 429's or 503's Retry-After, up to MaxRetryDelay). A 410 Gone is
// recorded with Store.MarkGone, which stops background fetches of the feed,
// and returns an error wrapping ErrGone. Neither is recorded if ctx was
// canceled (shutdown or a disconnected user). If recording fails, its error is
// joined to the fetch error. Any other error is from Store.SaveFetch or
// Store.RecordNotModified and is not recorded as a feed failure.
func (r *Refresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	resp, update, articles, err := r.fetch(ctx, feed)
	if errors.Is(err, ErrGone) {
		return rss.FetchResult{}, r.record(ctx, err, func(ctx context.Context) error {
			return r.Store.MarkGone(ctx, feed.ID)
		})
	}
	now := time.Now()
	if err != nil {
		next := RetryAt(now, feed.ConsecutiveFailures+1)
		if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
			next = notBefore(next, now, retryAfter(resp.Header, now))
		}
		return rss.FetchResult{}, r.record(ctx, err, func(ctx context.Context) error {
			return r.Store.RecordFailure(ctx, feed.ID, err.Error(), next)
		})
	}
	next := notBefore(NextFetch(now), now, freshness(resp.Header, now))
	if update == nil {
		if err := r.Store.RecordNotModified(ctx, feed.ID, next); err != nil {
			return rss.FetchResult{}, err
		}
		return rss.FetchResult{NotModified: true}, nil
	}
	update.NextFetch = next
	return r.Store.SaveFetch(ctx, *update, articles)
}

// notBefore returns the later of next and now + d, with d capped at
// MaxRetryDelay. A server's hint only ever delays the next fetch.
func notBefore(next, now time.Time, d time.Duration) time.Time {
	if hinted := now.Add(min(d, MaxRetryDelay)); hinted.After(next) {
		return hinted
	}
	return next
}

// fetch fetches and parses feed.Url and returns the response, the feed's new
// metadata and its articles, or a nil feed if the server answered 304. Errors
// wrap ErrUnreachable, ErrNotFeed or ErrGone; the response is nil only if
// the request itself failed.
func (r *Refresher) fetch(ctx context.Context, feed rss.Feed) (*fetch.Response, *rss.Feed, []rss.Article, error) {
	header := http.Header{}
	if feed.ETag != "" {
		header.Set("If-None-Match", feed.ETag)
	}
	if feed.LastModified != "" {
		header.Set("If-Modified-Since", feed.LastModified)
	}

	resp, err := r.Fetcher.Get(ctx, feed.Url, header)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if resp.StatusCode == http.StatusNotModified {
		return resp, nil, nil, nil
	}
	if resp.StatusCode == http.StatusGone {
		return resp, nil, nil, fmt.Errorf("%w: status %d", ErrGone, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, nil, nil, fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	}

	parsed, err := feedparse.Parse(resp.URL, resp.Body)
	if err != nil {
		return resp, nil, nil, fmt.Errorf("%w: %w", ErrNotFeed, err)
	}

	update := &rss.Feed{
		ID:           feed.ID,
		Title:        parsed.Title,
		Description:  parsed.Description,
		SiteUrl:      parsed.SiteURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	return resp, update, Articles(parsed.Items), nil
}

// record records the outcome of a fetch that failed with fetchErr by calling
// fn, unless ctx was canceled, and returns fetchErr joined with any error
// from fn. fn gets a context that outlives ctx's deadline.
func (r *Refresher) record(ctx context.Context, fetchErr error, fn func(ctx context.Context) error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		// The lease or the next click retries.
		return fetchErr
	}

	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	if err := fn(recCtx); err != nil {
		return errors.Join(fetchErr, fmt.Errorf("record outcome: %w", err))
	}
	return fetchErr
}
