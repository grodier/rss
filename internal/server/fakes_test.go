package server

import (
	"context"
	"time"

	"github.com/grodier/rss/internal/rss"
)

type fakeFeedStore struct {
	createFn    func(ctx context.Context, f rss.Feed) (string, error)
	getByIDFn   func(ctx context.Context, id string) (rss.Feed, error)
	getLatestFn func(ctx context.Context) ([]rss.Feed, error)
}

func (f *fakeFeedStore) Create(ctx context.Context, feed rss.Feed) (string, error) {
	return f.createFn(ctx, feed)
}

func (f *fakeFeedStore) GetByID(ctx context.Context, id string) (rss.Feed, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeFeedStore) GetLatest(ctx context.Context) ([]rss.Feed, error) {
	return f.getLatestFn(ctx)
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
