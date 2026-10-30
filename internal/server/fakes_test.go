package server

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/mail"
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
	getByIDFn    func(ctx context.Context, id string) (rss.Site, error)
	getIconFn    func(ctx context.Context, siteID string) (rss.SiteIcon, error)
	iconHashesFn func(ctx context.Context, siteIDs []string) (map[string]string, error)
}

func (f *fakeSiteStore) GetByID(ctx context.Context, id string) (rss.Site, error) {
	return f.getByIDFn(ctx, id)
}

func (f *fakeSiteStore) GetIcon(ctx context.Context, siteID string) (rss.SiteIcon, error) {
	return f.getIconFn(ctx, siteID)
}

// IconHashes reports no icons when iconHashesFn is nil.
func (f *fakeSiteStore) IconHashes(ctx context.Context, siteIDs []string) (map[string]string, error) {
	if f.iconHashesFn == nil {
		return map[string]string{}, nil
	}
	return f.iconHashesFn(ctx, siteIDs)
}

type fakeUserStore struct {
	createFn         func(ctx context.Context, name, email, password string) (string, time.Time, error)
	authenticateFn   func(ctx context.Context, email, password string) (string, error)
	getByEmailFn     func(ctx context.Context, email string) (rss.User, error)
	updateNameFn     func(ctx context.Context, id, name string) error
	updateThemeFn    func(ctx context.Context, id, theme string) error
	changePasswordFn func(ctx context.Context, userID, keepSessionID, current, next string) error
	confirmEmailFn   func(ctx context.Context, hash []byte) (string, error)
	resetPasswordFn  func(ctx context.Context, hash []byte, newPassword string) (rss.User, error)
	deleteFn         func(ctx context.Context, userID, password string) (rss.User, error)
	checkPasswordFn  func(ctx context.Context, userID, password string) error
	changeEmailFn    func(ctx context.Context, hash []byte) (rss.User, string, error)
}

func (f *fakeUserStore) Create(ctx context.Context, name, email, password string) (string, time.Time, error) {
	return f.createFn(ctx, name, email, password)
}

func (f *fakeUserStore) Authenticate(ctx context.Context, email, password string) (string, error) {
	return f.authenticateFn(ctx, email, password)
}

func (f *fakeUserStore) GetByEmail(ctx context.Context, email string) (rss.User, error) {
	return f.getByEmailFn(ctx, email)
}

func (f *fakeUserStore) UpdateName(ctx context.Context, id, name string) error {
	return f.updateNameFn(ctx, id, name)
}

func (f *fakeUserStore) UpdateTheme(ctx context.Context, id, theme string) error {
	return f.updateThemeFn(ctx, id, theme)
}

func (f *fakeUserStore) ChangePassword(ctx context.Context, userID, keepSessionID, current, next string) error {
	return f.changePasswordFn(ctx, userID, keepSessionID, current, next)
}

func (f *fakeUserStore) ConfirmEmail(ctx context.Context, hash []byte) (string, error) {
	return f.confirmEmailFn(ctx, hash)
}

func (f *fakeUserStore) ResetPassword(ctx context.Context, hash []byte, newPassword string) (rss.User, error) {
	return f.resetPasswordFn(ctx, hash, newPassword)
}

func (f *fakeUserStore) Delete(ctx context.Context, userID, password string) (rss.User, error) {
	return f.deleteFn(ctx, userID, password)
}

func (f *fakeUserStore) CheckPassword(ctx context.Context, userID, password string) error {
	return f.checkPasswordFn(ctx, userID, password)
}

func (f *fakeUserStore) ChangeEmail(ctx context.Context, hash []byte) (rss.User, string, error) {
	return f.changeEmailFn(ctx, hash)
}

// fakeTokenStore records the tokens created in it, unless createErr is set.
type fakeTokenStore struct {
	createErr error
	getFn     func(ctx context.Context, hash []byte, purpose string) (rss.Token, error)

	created []rss.Token
}

func (f *fakeTokenStore) Create(ctx context.Context, t rss.Token) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, t)
	return nil
}

func (f *fakeTokenStore) Get(ctx context.Context, hash []byte, purpose string) (rss.Token, error) {
	return f.getFn(ctx, hash, purpose)
}

type fakeSessionStore struct {
	createFn       func(ctx context.Context, userID string, idleBefore time.Time) (string, error)
	getFn          func(ctx context.Context, id string) (rss.Session, error)
	touchFn        func(ctx context.Context, id string) error
	deleteFn       func(ctx context.Context, id string) error
	deleteOthersFn func(ctx context.Context, userID, keepID string) (int64, error)
}

func (f *fakeSessionStore) Create(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
	return f.createFn(ctx, userID, idleBefore)
}

func (f *fakeSessionStore) Get(ctx context.Context, id string) (rss.Session, error) {
	return f.getFn(ctx, id)
}

func (f *fakeSessionStore) Touch(ctx context.Context, id string) error {
	return f.touchFn(ctx, id)
}

func (f *fakeSessionStore) Delete(ctx context.Context, id string) error {
	return f.deleteFn(ctx, id)
}

func (f *fakeSessionStore) DeleteOthers(ctx context.Context, userID, keepID string) (int64, error) {
	return f.deleteOthersFn(ctx, userID, keepID)
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

type fakeImportStore struct {
	createFn     func(ctx context.Context, userID, filename string, feeds []rss.ImportFeed) (string, error)
	getFn        func(ctx context.Context, id string) (rss.Import, []rss.ImportFeed, error)
	listByUserFn func(ctx context.Context, userID string, limit int) ([]rss.ImportSummary, error)
}

func (f *fakeImportStore) Create(ctx context.Context, userID, filename string, feeds []rss.ImportFeed) (string, error) {
	return f.createFn(ctx, userID, filename, feeds)
}

func (f *fakeImportStore) Get(ctx context.Context, id string) (rss.Import, []rss.ImportFeed, error) {
	return f.getFn(ctx, id)
}

// ListByUser returns no imports when listByUserFn is nil.
func (f *fakeImportStore) ListByUser(ctx context.Context, userID string, limit int) ([]rss.ImportSummary, error) {
	if f.listByUserFn == nil {
		return nil, nil
	}
	return f.listByUserFn(ctx, userID, limit)
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
	listTimelineFn func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error)
	listSharedFn   func(ctx context.Context, ownerID string, filter rss.TimelineFilter, limit int) ([]rss.SharedArticle, error)
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

func (f *fakeArticleStore) ListTimeline(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
	if f.listTimelineFn == nil {
		return nil, nil
	}
	return f.listTimelineFn(ctx, userID, filter, before, limit)
}

func (f *fakeArticleStore) ListShared(ctx context.Context, ownerID string, filter rss.TimelineFilter, limit int) ([]rss.SharedArticle, error) {
	if f.listSharedFn == nil {
		return nil, nil
	}
	return f.listSharedFn(ctx, ownerID, filter, limit)
}

type fakeReadStore struct {
	markReadFn       func(ctx context.Context, userID, articleID string) error
	readArticleIDsFn func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
	timelineMarkFn   func(ctx context.Context, userID string) (rss.TimelineMark, error)
	markSeenFn       func(ctx context.Context, userID string, upTo time.Time) error
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

func (f *fakeReadStore) TimelineMark(ctx context.Context, userID string) (rss.TimelineMark, error) {
	if f.timelineMarkFn == nil {
		return rss.TimelineMark{}, nil
	}
	return f.timelineMarkFn(ctx, userID)
}

func (f *fakeReadStore) MarkSeen(ctx context.Context, userID string, upTo time.Time) error {
	if f.markSeenFn == nil {
		return nil
	}
	return f.markSeenFn(ctx, userID, upTo)
}

type fakeSubscriptionStore struct {
	subscribeFn         func(ctx context.Context, userID, feedID string) error
	unsubscribeFn       func(ctx context.Context, userID, feedID string) error
	subscribedFeedIDsFn func(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error)
	listByUserFn        func(ctx context.Context, userID string) ([]rss.Subscription, error)
	getFn               func(ctx context.Context, userID, feedID string) (rss.Subscription, error)
	renameFn            func(ctx context.Context, userID, feedID, title string) error
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

func (f *fakeSubscriptionStore) Get(ctx context.Context, userID, feedID string) (rss.Subscription, error) {
	if f.getFn == nil {
		return rss.Subscription{}, rss.ErrNoRecord
	}
	return f.getFn(ctx, userID, feedID)
}

func (f *fakeSubscriptionStore) Rename(ctx context.Context, userID, feedID, title string) error {
	if f.renameFn == nil {
		return nil
	}
	return f.renameFn(ctx, userID, feedID, title)
}

// fakeEmailQueue records the messages queued to it, unless err is set.
type fakeEmailQueue struct {
	err error

	mu   sync.Mutex
	sent []mail.Message
}

func (f *fakeEmailQueue) Enqueue(ctx context.Context, m mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

// messages returns the messages queued so far.
func (f *fakeEmailQueue) messages() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

type fakeSaveStore struct {
	saveFn            func(ctx context.Context, userID, articleID string) error
	unsaveFn          func(ctx context.Context, userID, articleID string) error
	savedArticleIDsFn func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
	listSavedFn       func(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error)
}

func (f *fakeSaveStore) Save(ctx context.Context, userID, articleID string) error {
	if f.saveFn == nil {
		return nil
	}
	return f.saveFn(ctx, userID, articleID)
}

func (f *fakeSaveStore) Unsave(ctx context.Context, userID, articleID string) error {
	if f.unsaveFn == nil {
		return nil
	}
	return f.unsaveFn(ctx, userID, articleID)
}

func (f *fakeSaveStore) SavedArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	if f.savedArticleIDsFn == nil {
		return map[string]bool{}, nil
	}
	return f.savedArticleIDsFn(ctx, userID, articleIDs)
}

func (f *fakeSaveStore) ListSaved(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
	if f.listSavedFn == nil {
		return nil, nil
	}
	return f.listSavedFn(ctx, userID, q, before, limit)
}

type fakeStarStore struct {
	starFn              func(ctx context.Context, userID, articleID string) error
	unstarFn            func(ctx context.Context, userID, articleID string) error
	starredArticleIDsFn func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error)
	listStarredFn       func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.StarredArticle, error)
}

func (f *fakeStarStore) Star(ctx context.Context, userID, articleID string) error {
	if f.starFn == nil {
		return nil
	}
	return f.starFn(ctx, userID, articleID)
}

func (f *fakeStarStore) Unstar(ctx context.Context, userID, articleID string) error {
	if f.unstarFn == nil {
		return nil
	}
	return f.unstarFn(ctx, userID, articleID)
}

func (f *fakeStarStore) StarredArticleIDs(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error) {
	if f.starredArticleIDsFn == nil {
		return map[string]bool{}, nil
	}
	return f.starredArticleIDsFn(ctx, userID, articleIDs)
}

func (f *fakeStarStore) ListStarred(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.StarredArticle, error) {
	if f.listStarredFn == nil {
		return nil, nil
	}
	return f.listStarredFn(ctx, userID, filter, before, limit)
}

type fakeTagStore struct {
	createFn   func(ctx context.Context, userID, name string) (rss.Tag, error)
	listFn     func(ctx context.Context, userID string) ([]rss.Tag, error)
	getFn      func(ctx context.Context, userID, id string) (rss.Tag, error)
	renameFn   func(ctx context.Context, userID, id, name string) error
	deleteFn   func(ctx context.Context, userID, id string) error
	feedIDsFn  func(ctx context.Context, userID, id string) (map[string]bool, error)
	setFeedsFn func(ctx context.Context, userID, id string, feedIDs []string) error

	setForSubscriptionFn func(ctx context.Context, userID, feedID string, tagIDs, newNames []string) error
}

func (f *fakeTagStore) Create(ctx context.Context, userID, name string) (rss.Tag, error) {
	if f.createFn == nil {
		return rss.Tag{}, nil
	}
	return f.createFn(ctx, userID, name)
}

func (f *fakeTagStore) List(ctx context.Context, userID string) ([]rss.Tag, error) {
	if f.listFn == nil {
		return nil, nil
	}
	return f.listFn(ctx, userID)
}

func (f *fakeTagStore) Get(ctx context.Context, userID, id string) (rss.Tag, error) {
	if f.getFn == nil {
		return rss.Tag{}, rss.ErrNoRecord
	}
	return f.getFn(ctx, userID, id)
}

func (f *fakeTagStore) Rename(ctx context.Context, userID, id, name string) error {
	if f.renameFn == nil {
		return nil
	}
	return f.renameFn(ctx, userID, id, name)
}

func (f *fakeTagStore) Delete(ctx context.Context, userID, id string) error {
	if f.deleteFn == nil {
		return nil
	}
	return f.deleteFn(ctx, userID, id)
}

func (f *fakeTagStore) FeedIDs(ctx context.Context, userID, id string) (map[string]bool, error) {
	if f.feedIDsFn == nil {
		return map[string]bool{}, nil
	}
	return f.feedIDsFn(ctx, userID, id)
}

func (f *fakeTagStore) SetFeeds(ctx context.Context, userID, id string, feedIDs []string) error {
	if f.setFeedsFn == nil {
		return nil
	}
	return f.setFeedsFn(ctx, userID, id, feedIDs)
}

func (f *fakeTagStore) SetForSubscription(ctx context.Context, userID, feedID string, tagIDs, newNames []string) error {
	if f.setForSubscriptionFn == nil {
		return nil
	}
	return f.setForSubscriptionFn(ctx, userID, feedID, tagIDs, newNames)
}

type fakeDebugStore struct {
	siteDebugFn            func(ctx context.Context, siteID string) (rss.SiteDebug, error)
	feedDebugFn            func(ctx context.Context, feedIDs []string) (map[string]rss.FeedDebug, error)
	articleDebugFn         func(ctx context.Context, userID, articleID string) (rss.ArticleDebug, error)
	articlesByExternalIDFn func(ctx context.Context, feedID string, externalIDs []string) (map[string]rss.Article, error)
}

func (f *fakeDebugStore) SiteDebug(ctx context.Context, siteID string) (rss.SiteDebug, error) {
	return f.siteDebugFn(ctx, siteID)
}

func (f *fakeDebugStore) FeedDebug(ctx context.Context, feedIDs []string) (map[string]rss.FeedDebug, error) {
	return f.feedDebugFn(ctx, feedIDs)
}

func (f *fakeDebugStore) ArticleDebug(ctx context.Context, userID, articleID string) (rss.ArticleDebug, error) {
	return f.articleDebugFn(ctx, userID, articleID)
}

// ArticlesByExternalID reports no stored articles when articlesByExternalIDFn
// is nil.
func (f *fakeDebugStore) ArticlesByExternalID(ctx context.Context, feedID string, externalIDs []string) (map[string]rss.Article, error) {
	if f.articlesByExternalIDFn == nil {
		return map[string]rss.Article{}, nil
	}
	return f.articlesByExternalIDFn(ctx, feedID, externalIDs)
}

// fakeFetcher answers with resp or err, and records the URLs and headers
// it was asked for.
type fakeFetcher struct {
	resp *fetch.Response
	err  error

	urls    []string
	headers []http.Header
}

func (f *fakeFetcher) Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error) {
	f.urls = append(f.urls, rawURL)
	f.headers = append(f.headers, header.Clone())
	return f.resp, f.err
}

// fakeDiscoverer answers with res and err, and records the URLs it was asked
// to discover.
type fakeDiscoverer struct {
	res discovery.Result
	err error

	urls []string
}

func (f *fakeDiscoverer) Discover(ctx context.Context, u *url.URL) (discovery.Result, error) {
	f.urls = append(f.urls, u.String())
	return f.res, f.err
}

type fakeTimelineStore struct {
	createFn func(ctx context.Context, userID string, t rss.Timeline) (string, error)
	getFn    func(ctx context.Context, userID, id string) (rss.Timeline, error)
	listFn   func(ctx context.Context, userID string) ([]rss.Timeline, error)
	updateFn func(ctx context.Context, userID string, t rss.Timeline) error
	deleteFn func(ctx context.Context, userID, id string) error
	moveFn   func(ctx context.Context, userID, id string, up bool) error

	shareFn        func(ctx context.Context, userID, id, newToken string) (string, error)
	newShareLinkFn func(ctx context.Context, userID, id, newToken string) error
	unshareFn      func(ctx context.Context, userID, id string) error
	getSharedFn    func(ctx context.Context, token string) (rss.Timeline, string, error)
}

func (f *fakeTimelineStore) Create(ctx context.Context, userID string, t rss.Timeline) (string, error) {
	if f.createFn == nil {
		return "", nil
	}
	return f.createFn(ctx, userID, t)
}

func (f *fakeTimelineStore) Get(ctx context.Context, userID, id string) (rss.Timeline, error) {
	if f.getFn == nil {
		return rss.Timeline{}, rss.ErrNoRecord
	}
	return f.getFn(ctx, userID, id)
}

func (f *fakeTimelineStore) List(ctx context.Context, userID string) ([]rss.Timeline, error) {
	if f.listFn == nil {
		return nil, nil
	}
	return f.listFn(ctx, userID)
}

func (f *fakeTimelineStore) Update(ctx context.Context, userID string, t rss.Timeline) error {
	if f.updateFn == nil {
		return nil
	}
	return f.updateFn(ctx, userID, t)
}

func (f *fakeTimelineStore) Delete(ctx context.Context, userID, id string) error {
	if f.deleteFn == nil {
		return nil
	}
	return f.deleteFn(ctx, userID, id)
}

func (f *fakeTimelineStore) Move(ctx context.Context, userID, id string, up bool) error {
	if f.moveFn == nil {
		return nil
	}
	return f.moveFn(ctx, userID, id, up)
}

func (f *fakeTimelineStore) Share(ctx context.Context, userID, id, newToken string) (string, error) {
	if f.shareFn == nil {
		return newToken, nil
	}
	return f.shareFn(ctx, userID, id, newToken)
}

func (f *fakeTimelineStore) NewShareLink(ctx context.Context, userID, id, newToken string) error {
	if f.newShareLinkFn == nil {
		return nil
	}
	return f.newShareLinkFn(ctx, userID, id, newToken)
}

func (f *fakeTimelineStore) Unshare(ctx context.Context, userID, id string) error {
	if f.unshareFn == nil {
		return nil
	}
	return f.unshareFn(ctx, userID, id)
}

func (f *fakeTimelineStore) GetShared(ctx context.Context, token string) (rss.Timeline, string, error) {
	if f.getSharedFn == nil {
		return rss.Timeline{}, "", rss.ErrNoRecord
	}
	return f.getSharedFn(ctx, token)
}
