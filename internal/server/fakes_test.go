package server

import (
	"context"
	"time"

	"github.com/grodier/rss/internal/rss"
)

type fakeFeedStore struct {
	getByIDFn        func(ctx context.Context, id string) (rss.Feed, error)
	listBySiteFn     func(ctx context.Context, siteID string) ([]rss.Feed, error)
	requestRefreshFn func(ctx context.Context, id string) error
}

func (f *fakeFeedStore) GetByID(ctx context.Context, id string) (rss.Feed, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeFeedStore) ListBySite(ctx context.Context, siteID string) ([]rss.Feed, error) {
	return f.listBySiteFn(ctx, siteID)
}

func (f *fakeFeedStore) RequestRefresh(ctx context.Context, id string) error {
	if f.requestRefreshFn == nil {
		return nil
	}
	return f.requestRefreshFn(ctx, id)
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
	getByIDFn      func(ctx context.Context, id string) (rss.Article, error)
	listByFeedFn   func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error)
	listTimelineFn func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error)
}

func (f *fakeArticleStore) GetByID(ctx context.Context, id string) (rss.Article, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeArticleStore) ListByFeed(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
	if f.listByFeedFn == nil {
		return nil, nil
	}
	return f.listByFeedFn(ctx, feedID, before, limit)
}

func (f *fakeArticleStore) ListTimeline(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
	if f.listTimelineFn == nil {
		return nil, nil
	}
	return f.listTimelineFn(ctx, userID, before, limit)
}

type fakeReadStore struct {
	markReadFn       func(ctx context.Context, userID, articleID string) error
	readArticleIDsFn func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
}

func (f *fakeReadStore) MarkRead(ctx context.Context, userID, articleID string) error {
	if f.markReadFn == nil {
		return nil
	}
	return f.markReadFn(ctx, userID, articleID)
}

func (f *fakeReadStore) ReadArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	if f.readArticleIDsFn == nil {
		return map[string]bool{}, nil
	}
	return f.readArticleIDsFn(ctx, userID, articleIDs)
}

type fakeSubscriptionStore struct {
	subscribeFn         func(ctx context.Context, userID, feedID string) error
	unsubscribeFn       func(ctx context.Context, userID, feedID string) error
	subscribedFeedIDsFn func(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error)
	listByUserFn        func(ctx context.Context, userID string) ([]rss.Subscription, error)
}

func (f *fakeSubscriptionStore) Subscribe(ctx context.Context, userID, feedID string) error {
	if f.subscribeFn == nil {
		return nil
	}
	return f.subscribeFn(ctx, userID, feedID)
}

func (f *fakeSubscriptionStore) Unsubscribe(ctx context.Context, userID, feedID string) error {
	if f.unsubscribeFn == nil {
		return nil
	}
	return f.unsubscribeFn(ctx, userID, feedID)
}

func (f *fakeSubscriptionStore) SubscribedFeedIDs(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
	if f.subscribedFeedIDsFn == nil {
		return map[string]bool{}, nil
	}
	return f.subscribedFeedIDsFn(ctx, userID, feedIDs)
}

func (f *fakeSubscriptionStore) ListByUser(ctx context.Context, userID string) ([]rss.Subscription, error) {
	if f.listByUserFn == nil {
		return nil, nil
	}
	return f.listByUserFn(ctx, userID)
}
