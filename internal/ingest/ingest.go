// Package ingest fetches feeds and turns their items into articles.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/rss"
)

// MaxArticles is the most articles kept from one fetch of a feed.
const MaxArticles = 200

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
	Get(ctx context.Context, rawURL string) (*fetch.Response, error)
}

var _ Fetcher = (*fetch.Client)(nil)

// Store records fetch outcomes; satisfied by *psql.FeedRepository.
type Store interface {
	SaveFetch(ctx context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error)
	RecordFailure(ctx context.Context, id, msg string, next time.Time) error
}

// recordTimeout bounds recording a failed fetch, which uses a context that
// outlives ctx's deadline so a fetch that timed out is still recorded.
const recordTimeout = 5 * time.Second

var (
	// ErrUnreachable: the feed couldn't be fetched (network error, timeout,
	// blocked address, too large) or answered with a non-2xx status.
	ErrUnreachable = errors.New("ingest: feed unreachable")
	// ErrNotFeed: the feed URL answered, but not with a feed.
	ErrNotFeed = errors.New("ingest: not a feed")
)

// Refresher fetches feeds and saves their articles.
type Refresher struct {
	Fetcher Fetcher
	Store   Store
}

// Refresh fetches feed.Url, parses it and saves its metadata and articles,
// scheduling the feed's next fetch (see NextFetch). The caller sets the
// deadline on ctx. Fetch failures wrap ErrUnreachable and parse failures
// ErrNotFeed; both are recorded with Store.RecordFailure and reschedule the
// feed with backoff (see RetryAt), unless ctx was canceled (shutdown or a
// disconnected user), which records nothing. If recording fails, its error
// is joined to the fetch error. Any other error is from Store.SaveFetch and
// is not recorded as a feed failure.
func (r *Refresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	update, articles, err := r.fetch(ctx, feed)
	if err != nil {
		return rss.FetchResult{}, r.recordFailure(ctx, feed, err)
	}
	update.NextFetch = NextFetch(time.Now())
	return r.Store.SaveFetch(ctx, update, articles)
}

// fetch fetches and parses feed.Url and returns the feed's new metadata and
// its articles. Errors wrap ErrUnreachable or ErrNotFeed.
func (r *Refresher) fetch(ctx context.Context, feed rss.Feed) (rss.Feed, []rss.Article, error) {
	resp, err := r.Fetcher.Get(ctx, feed.Url)
	if err != nil {
		return rss.Feed{}, nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return rss.Feed{}, nil, fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	}

	parsed, err := feedparse.Parse(resp.URL, resp.Body)
	if err != nil {
		return rss.Feed{}, nil, fmt.Errorf("%w: %w", ErrNotFeed, err)
	}

	update := rss.Feed{
		ID:          feed.ID,
		Title:       parsed.Title,
		Description: parsed.Description,
		SiteUrl:     parsed.SiteURL,
	}
	return update, Articles(parsed.Items), nil
}

// recordFailure records fetchErr as a failed fetch of feed, unless ctx was
// canceled, and returns fetchErr joined with any error recording it.
func (r *Refresher) recordFailure(ctx context.Context, feed rss.Feed, fetchErr error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		// The lease or the next click retries.
		return fetchErr
	}

	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	next := RetryAt(time.Now(), feed.ConsecutiveFailures+1)
	if err := r.Store.RecordFailure(recCtx, feed.ID, fetchErr.Error(), next); err != nil {
		return errors.Join(fetchErr, fmt.Errorf("record failure: %w", err))
	}
	return fetchErr
}
