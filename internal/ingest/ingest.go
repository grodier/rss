// Package ingest fetches feeds and turns their items into articles.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

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

// Store saves a fetch; satisfied by *psql.FeedRepository.
type Store interface {
	SaveFetch(ctx context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error)
}

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

// Refresh fetches feed.Url, parses it and saves its metadata and articles.
// The caller sets the deadline on ctx. Fetch failures wrap ErrUnreachable
// and parse failures ErrNotFeed; any other error is from the Store.
func (r *Refresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	resp, err := r.Fetcher.Get(ctx, feed.Url)
	if err != nil {
		return rss.FetchResult{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return rss.FetchResult{}, fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	}

	parsed, err := feedparse.Parse(resp.URL, resp.Body)
	if err != nil {
		return rss.FetchResult{}, fmt.Errorf("%w: %w", ErrNotFeed, err)
	}

	update := rss.Feed{
		ID:          feed.ID,
		Title:       parsed.Title,
		Description: parsed.Description,
		SiteUrl:     parsed.SiteURL,
	}
	return r.Store.SaveFetch(ctx, update, Articles(parsed.Items))
}
