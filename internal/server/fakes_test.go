package server

import (
	"context"
	"time"

	"github.com/grodier/rss/internal/rss"
)

type fakeFeedStore struct {
	getByIDFn    func(ctx context.Context, id string) (rss.Feed, error)
	getLatestFn  func(ctx context.Context) ([]rss.Feed, error)
	listBySiteFn func(ctx context.Context, siteID string) ([]rss.Feed, error)
}

func (f *fakeFeedStore) GetByID(ctx context.Context, id string) (rss.Feed, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeFeedStore) GetLatest(ctx context.Context) ([]rss.Feed, error) {
	return f.getLatestFn(ctx)
}

func (f *fakeFeedStore) ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error) {
	return f.listBySiteFn(ctx, siteID)
}

type fakeSiteStore struct {
	getByIDFn func(ctx context.Context, id string) (rss.Site, error)
}

func (f *fakeSiteStore) GetByID(ctx context.Context, id string) (rss.Site, error) {
	return f.getByIDFn(ctx, id)
}

type fakeUserStore struct {
	createFn       func(ctx context.Context, name, email, password string) (string, time.Time, error)
	authenticateFn func(ctx context.Context, email, password string) (string, error)
	existsFn       func(ctx context.Context, id string) (bool, error)
}

func (f *fakeUserStore) Create(ctx context.Context, name, email, password string) (string, time.Time, error) {
	return f.createFn(ctx, name, email, password)
}

func (f *fakeUserStore) Authenticate(ctx context.Context, email, password string) (string, error) {
	return f.authenticateFn(ctx, email, password)
}

func (f *fakeUserStore) Exists(ctx context.Context, id string) (bool, error) {
	return f.existsFn(ctx, id)
}

type fakeSearchStore struct {
	searchFn func(ctx context.Context, q string, limit int) ([]rss.SiteWithFeeds, error)
}

func (f *fakeSearchStore) Search(ctx context.Context, q string, limit int) ([]rss.SiteWithFeeds, error) {
	return f.searchFn(ctx, q, limit)
}

type fakeLookupStore struct {
	requestFn      func(ctx context.Context, siteKey, url string, doneTTL, failedTTL time.Duration) (rss.Lookup, error)
	getByIDFn      func(ctx context.Context, id string) (rss.Lookup, error)
	getBySiteKeyFn func(ctx context.Context, siteKey string) (rss.Lookup, error)
}

func (f *fakeLookupStore) Request(ctx context.Context, siteKey, url string, doneTTL, failedTTL time.Duration) (rss.Lookup, error) {
	return f.requestFn(ctx, siteKey, url, doneTTL, failedTTL)
}

func (f *fakeLookupStore) GetByID(ctx context.Context, id string) (rss.Lookup, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeLookupStore) GetBySiteKey(ctx context.Context, siteKey string) (rss.Lookup, error) {
	return f.getBySiteKeyFn(ctx, siteKey)
}

type fakeRefresher struct {
	refreshFn func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error)
	calls     []rss.Feed
}

func (f *fakeRefresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	f.calls = append(f.calls, feed)
	return f.refreshFn(ctx, feed)
}

type fakeArticleStore struct {
	listByFeedFn func(ctx context.Context, feedID string, limit int) ([]rss.Article, error)
}

func (f *fakeArticleStore) ListByFeed(ctx context.Context, feedID string, limit int) ([]rss.Article, error) {
	if f.listByFeedFn == nil {
		return nil, nil
	}
	return f.listByFeedFn(ctx, feedID, limit)
}
