package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/rss"
)

// fakeStore hands out pending feeds in order and records outcomes.
type fakeStore struct {
	mu       sync.Mutex
	pending  []rss.ImportFeed
	finished []rss.ImportFeed
	done     chan struct{} // receives once per Finish
}

func newFakeStore(feeds ...rss.ImportFeed) *fakeStore {
	return &fakeStore{pending: feeds, done: make(chan struct{}, 100)}
}

func (s *fakeStore) ClaimNext(ctx context.Context) (rss.ImportFeed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return rss.ImportFeed{}, rss.ErrNoRecord
	}
	f := s.pending[0]
	s.pending = s.pending[1:]
	return f, nil
}

func (s *fakeStore) Finish(ctx context.Context, f rss.ImportFeed) error {
	s.mu.Lock()
	s.finished = append(s.finished, f)
	s.mu.Unlock()
	s.done <- struct{}{}
	return nil
}

func (s *fakeStore) ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error) {
	return 0, nil
}

// fakeFeeds holds feeds by URL.
type fakeFeeds struct {
	mu    sync.Mutex
	byURL map[string]rss.Feed
}

func (f *fakeFeeds) GetByURL(ctx context.Context, url string) (rss.Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	feed, ok := f.byURL[url]
	if !ok {
		return rss.Feed{}, rss.ErrNoRecord
	}
	return feed, nil
}

func (f *fakeFeeds) put(feed rss.Feed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byURL == nil {
		f.byURL = map[string]rss.Feed{}
	}
	f.byURL[feed.Url] = feed
}

// renameCall is one call to fakeSubscriptions.Rename.
type renameCall struct {
	userID, feedID, title string
}

// fakeSubscriptions holds subscriptions as "userID feedID", and records
// Rename calls, returning renameErr.
type fakeSubscriptions struct {
	subs      map[string]bool
	renames   []renameCall
	renameErr error
}

func (s *fakeSubscriptions) Rename(ctx context.Context, userID, feedID, title string) error {
	s.renames = append(s.renames, renameCall{userID, feedID, title})
	return s.renameErr
}

func (s *fakeSubscriptions) Subscribe(ctx context.Context, userID, feedID string) error {
	if s.subs == nil {
		s.subs = map[string]bool{}
	}
	s.subs[userID+" "+feedID] = true
	return nil
}

func (s *fakeSubscriptions) SubscribedFeedIDs(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
	got := map[string]bool{}
	for _, id := range feedIDs {
		if s.subs[userID+" "+id] {
			got[id] = true
		}
	}
	return got, nil
}

// addTagsCall is one call to fakeTags.AddToSubscription.
type addTagsCall struct {
	userID, feedID string
	names          []string
}

// fakeTags records AddToSubscription calls and returns err.
type fakeTags struct {
	calls []addTagsCall
	err   error
}

func (t *fakeTags) AddToSubscription(ctx context.Context, userID, feedID string, names []string) error {
	t.calls = append(t.calls, addTagsCall{userID, feedID, names})
	return t.err
}

// fakeSaver records saves and, like DiscoveryRepository.Save, makes the
// saved feeds known to feeds.
type fakeSaver struct {
	feeds *fakeFeeds
	calls int
	err   error
}

func (s *fakeSaver) Save(ctx context.Context, site rss.Site, feeds []rss.FeedWithArticles) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	for _, f := range feeds {
		f.Feed.ID = "id:" + f.Feed.Url
		f.Feed.SiteID = "site-1"
		s.feeds.put(f.Feed)
	}
	return "site-1", nil
}

type fakeDiscoverer struct {
	calls      int
	DiscoverFn func(ctx context.Context, u *url.URL) (discovery.Result, error)
}

func (d *fakeDiscoverer) Discover(ctx context.Context, u *url.URL) (discovery.Result, error) {
	d.calls++
	if d.DiscoverFn == nil {
		return discovery.Result{}, errors.New("unexpected Discover call")
	}
	return d.DiscoverFn(ctx, u)
}

// result returns a discovery result for a site with the given feed URLs.
func result(feedURLs ...string) discovery.Result {
	res := discovery.Result{Site: discovery.SiteInfo{Key: "example.com", URL: "https://example.com/", Title: "Example"}}
	for _, u := range feedURLs {
		res.Feeds = append(res.Feeds, discovery.FeedInfo{URL: u, Title: "Feed", Items: []feedparse.Item{{ID: "a", Title: "A"}}})
	}
	return res
}

type testRunner struct {
	*Runner
	store *fakeStore
	feeds *fakeFeeds
	subs  *fakeSubscriptions
	tags  *fakeTags
	saver *fakeSaver
	disc  *fakeDiscoverer
	logs  *bytes.Buffer
}

func newTestRunner() *testRunner {
	feeds := &fakeFeeds{}
	tr := &testRunner{
		store: newFakeStore(),
		feeds: feeds,
		subs:  &fakeSubscriptions{},
		tags:  &fakeTags{},
		saver: &fakeSaver{feeds: feeds},
		disc:  &fakeDiscoverer{},
		logs:  &bytes.Buffer{},
	}
	tr.Runner = &Runner{
		Store:         tr.store,
		Feeds:         tr.feeds,
		Subscriptions: tr.subs,
		Tags:          tr.tags,
		Saver:         tr.saver,
		Discoverer:    tr.disc,
		Logger:        slog.New(slog.NewTextHandler(tr.logs, nil)),
	}
	tr.setDefaults()
	return tr
}

// importOne processes one feed with url and returns what was recorded, or
// fails the test if nothing was.
func (tr *testRunner) importOne(t *testing.T, ctx context.Context, url string) rss.ImportFeed {
	t.Helper()
	return tr.importTagged(t, ctx, url, nil)
}

// importTagged is importOne for a feed with tags.
func (tr *testRunner) importTagged(t *testing.T, ctx context.Context, url string, tags []string) rss.ImportFeed {
	t.Helper()
	return tr.importFeed(t, ctx, rss.ImportFeed{URL: url, Tags: tags})
}

// importTitled is importOne for a feed with an outline title.
func (tr *testRunner) importTitled(t *testing.T, ctx context.Context, url, title string) rss.ImportFeed {
	t.Helper()
	return tr.importFeed(t, ctx, rss.ImportFeed{URL: url, Title: title})
}

// importFeed processes f as the third feed of import imp-1 by user-1.
func (tr *testRunner) importFeed(t *testing.T, ctx context.Context, f rss.ImportFeed) rss.ImportFeed {
	t.Helper()
	f.ImportID, f.UserID, f.Position, f.Status = "imp-1", "user-1", 3, rss.ImportRunning
	tr.process(ctx, f)
	if len(tr.store.finished) != 1 {
		t.Fatalf("Finish called %d times; want 1", len(tr.store.finished))
	}
	return tr.store.finished[0]
}

func checkOutcome(t *testing.T, got rss.ImportFeed, status rss.ImportStatus, failure rss.ImportFailure, feedID string) {
	t.Helper()
	if got.Status != status || got.Failure != failure || got.FeedID != feedID {
		t.Errorf("got status %q, failure %q, feed %q; want %q, %q, %q (error %q)",
			got.Status, got.Failure, got.FeedID, status, failure, feedID, got.Error)
	}
	if got.ImportID != "imp-1" || got.Position != 3 {
		t.Errorf("recorded %s/%d; want imp-1/3", got.ImportID, got.Position)
	}
}

func TestProcessInvalidURL(t *testing.T) {
	tr := newTestRunner()
	got := tr.importOne(t, t.Context(), "ftp://example.com/feed")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportInvalidURL, "")
	if tr.disc.calls != 0 {
		t.Errorf("Discover called %d times; want 0", tr.disc.calls)
	}
}

func TestProcessKnownFeed(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})

	// Canonicalized before the lookup: host lowercased, fragment dropped.
	got := tr.importOne(t, t.Context(), " https://EXAMPLE.com/feed#top ")
	checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
	if !tr.subs.subs["user-1 feed-1"] {
		t.Error("user not subscribed to feed-1")
	}
	if tr.disc.calls != 0 || tr.saver.calls != 0 {
		t.Errorf("Discover called %d times, Save %d; want 0", tr.disc.calls, tr.saver.calls)
	}
	if !strings.Contains(tr.logs.String(), "import feed finished") {
		t.Errorf("no finished log line in %q", tr.logs)
	}
}

func TestProcessKnownGoneFeedIsDiscovered(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "old", Url: "https://example.com/feed", GoneAt: time.Now()})
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return result("https://example.com/feed"), nil
	}

	got := tr.importOne(t, t.Context(), "https://example.com/feed")
	checkOutcome(t, got, rss.ImportSubscribed, "", "id:https://example.com/feed")
	if tr.disc.calls != 1 || tr.saver.calls != 1 {
		t.Errorf("Discover called %d times, Save %d; want 1 each", tr.disc.calls, tr.saver.calls)
	}
}

func TestProcessAlreadySubscribed(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})
	tr.subs.Subscribe(t.Context(), "user-1", "feed-1")

	got := tr.importOne(t, t.Context(), "https://example.com/feed")
	checkOutcome(t, got, rss.ImportAlreadySubscribed, "", "feed-1")
}

func TestProcessAddsTags(t *testing.T) {
	want := []addTagsCall{{"user-1", "feed-1", []string{"Tech", "AI"}}}

	t.Run("subscribed", func(t *testing.T) {
		tr := newTestRunner()
		tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})

		got := tr.importTagged(t, t.Context(), "https://example.com/feed", []string{"Tech", "AI"})
		checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
		if !reflect.DeepEqual(tr.tags.calls, want) {
			t.Errorf("AddToSubscription calls = %v; want %v", tr.tags.calls, want)
		}
	})

	t.Run("already subscribed", func(t *testing.T) {
		tr := newTestRunner()
		tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})
		tr.subs.Subscribe(t.Context(), "user-1", "feed-1")

		got := tr.importTagged(t, t.Context(), "https://example.com/feed", []string{"Tech", "AI"})
		checkOutcome(t, got, rss.ImportAlreadySubscribed, "", "feed-1")
		if !reflect.DeepEqual(tr.tags.calls, want) {
			t.Errorf("AddToSubscription calls = %v; want %v", tr.tags.calls, want)
		}
	})

	t.Run("an error is logged and the feed is still subscribed", func(t *testing.T) {
		tr := newTestRunner()
		tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})
		tr.tags.err = errors.New("db down")

		got := tr.importTagged(t, t.Context(), "https://example.com/feed", []string{"Tech"})
		checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
		if got.Error != "" {
			t.Errorf("Error = %q; want none", got.Error)
		}
		if logs := tr.logs.String(); !strings.Contains(logs, "importer: add tags") || !strings.Contains(logs, "db down") {
			t.Errorf("no add tags error in logs %q", logs)
		}
	})

	t.Run("no tags, no call", func(t *testing.T) {
		tr := newTestRunner()
		tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})

		got := tr.importOne(t, t.Context(), "https://example.com/feed")
		checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
		if len(tr.tags.calls) != 0 {
			t.Errorf("AddToSubscription calls = %v; want none", tr.tags.calls)
		}
	})

	t.Run("a failed feed isn't tagged", func(t *testing.T) {
		tr := newTestRunner()

		got := tr.importTagged(t, t.Context(), "ftp://example.com/feed", []string{"Tech"})
		checkOutcome(t, got, rss.ImportFailed, rss.ImportInvalidURL, "")
		if len(tr.tags.calls) != 0 {
			t.Errorf("AddToSubscription calls = %v; want none", tr.tags.calls)
		}
	})
}

func TestImportedName(t *testing.T) {
	const outlineURL = "https://example.com/feed"
	feed := rss.Feed{Title: "Example  Feed"}
	tests := []struct {
		name, title, want string
	}{
		{"different name", "My Feed", "My Feed"},
		{"whitespace collapsed", "  My \n Feed ", "My Feed"},
		{"same as the feed title", "Example Feed", ""},
		{"same ignoring case and spaces", " example   FEED ", ""},
		{"empty", "", ""},
		{"only spaces", "   ", ""},
		{"the outline URL", outlineURL, ""},
		{"101 characters", strings.Repeat("é", 101), ""},
		{"100 characters", strings.Repeat("é", 100), strings.Repeat("é", 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := importedName(tt.title, outlineURL, feed); got != tt.want {
				t.Errorf("importedName(%q) = %q; want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestProcessSetsName(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed", Title: "Feed"})

	got := tr.importTitled(t, t.Context(), "https://example.com/feed", "My Feed")
	checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
	want := []renameCall{{"user-1", "feed-1", "My Feed"}}
	if !reflect.DeepEqual(tr.subs.renames, want) {
		t.Errorf("Rename calls = %v; want %v", tr.subs.renames, want)
	}
}

func TestProcessNameMatchingFeedTitleIsNotSet(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed", Title: "Feed"})

	got := tr.importTitled(t, t.Context(), "https://example.com/feed", "feed")
	checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
	if len(tr.subs.renames) != 0 {
		t.Errorf("Rename calls = %v; want none", tr.subs.renames)
	}
}

func TestProcessAlreadySubscribedKeepsName(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed", Title: "Feed"})
	tr.subs.Subscribe(t.Context(), "user-1", "feed-1")

	got := tr.importTitled(t, t.Context(), "https://example.com/feed", "My Feed")
	checkOutcome(t, got, rss.ImportAlreadySubscribed, "", "feed-1")
	if len(tr.subs.renames) != 0 {
		t.Errorf("Rename calls = %v; want none", tr.subs.renames)
	}
}

func TestProcessRenameErrorStillSubscribes(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://example.com/feed", Title: "Feed"})
	tr.subs.renameErr = errors.New("db down")

	got := tr.importTitled(t, t.Context(), "https://example.com/feed", "My Feed")
	checkOutcome(t, got, rss.ImportSubscribed, "", "feed-1")
	if got.Error != "" {
		t.Errorf("Error = %q; want none", got.Error)
	}
	if logs := tr.logs.String(); !strings.Contains(logs, "importer: set name") || !strings.Contains(logs, "db down") {
		t.Errorf("no set name error in logs %q", logs)
	}
}

func TestProcessUnknownFeedIsDiscoveredAndSaved(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		if u.String() != "https://example.com/feed" {
			t.Errorf("Discover URL = %q", u)
		}
		// Discovery reports the feed's final URL, after redirects.
		return result("https://example.com/feed.xml"), nil
	}

	got := tr.importTitled(t, t.Context(), "example.com/feed", "My Feed")
	checkOutcome(t, got, rss.ImportSubscribed, "", "id:https://example.com/feed.xml")
	if tr.saver.calls != 1 {
		t.Errorf("Save called %d times; want 1", tr.saver.calls)
	}
	if !tr.subs.subs["user-1 id:https://example.com/feed.xml"] {
		t.Error("user not subscribed to the saved feed")
	}
	want := []renameCall{{"user-1", "id:https://example.com/feed.xml", "My Feed"}}
	if !reflect.DeepEqual(tr.subs.renames, want) {
		t.Errorf("Rename calls = %v; want %v", tr.subs.renames, want)
	}
}

func TestProcessWebPageUsesFirstFeed(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return result("https://example.com/posts.xml", "https://example.com/comments.xml"), nil
	}

	got := tr.importOne(t, t.Context(), "https://example.com/")
	checkOutcome(t, got, rss.ImportSubscribed, "", "id:https://example.com/posts.xml")
	if tr.subs.subs["user-1 id:https://example.com/comments.xml"] {
		t.Error("subscribed to the second feed too")
	}
	// Both are saved, as a lookup saves them.
	if _, err := tr.feeds.GetByURL(t.Context(), "https://example.com/comments.xml"); err != nil {
		t.Errorf("second feed not saved: %v", err)
	}
}

func TestProcessDiscoveryError(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return discovery.Result{}, errors.New("dial tcp: no such host")
	}

	got := tr.importOne(t, t.Context(), "https://example.com/feed")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportUnreachable, "")
	if !strings.Contains(got.Error, "no such host") {
		t.Errorf("Error = %q; want the discovery error", got.Error)
	}
}

func TestProcessAppLink(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return discovery.Result{}, fmt.Errorf("%w: %s", discovery.ErrAppLink, u)
	}

	got := tr.importOne(t, t.Context(), "https://paperround.app/shared/abc/feed.atom")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportAppLink, "")
	if tr.saver.calls != 0 {
		t.Errorf("Save called %d times; want 0", tr.saver.calls)
	}
}

func TestProcessNoFeeds(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return result(), nil
	}

	got := tr.importOne(t, t.Context(), "https://example.com/about")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportNotFeed, "")
	if tr.saver.calls != 0 {
		t.Errorf("Save called %d times; want 0", tr.saver.calls)
	}
}

func TestProcessSaveError(t *testing.T) {
	tr := newTestRunner()
	tr.saver.err = errors.New("connection refused")
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return result("https://example.com/feed"), nil
	}

	got := tr.importOne(t, t.Context(), "https://example.com/feed")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportUnreachable, "")
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("Error = %q; want the save error", got.Error)
	}
	if logs := tr.logs.String(); !strings.Contains(logs, "import feed failed") || !strings.Contains(logs, "connection refused") {
		t.Errorf("save error not logged: %q", logs)
	}
}

func TestProcessPanicFails(t *testing.T) {
	tr := newTestRunner()
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		panic("boom")
	}

	got := tr.importOne(t, t.Context(), "https://example.com/feed")
	checkOutcome(t, got, rss.ImportFailed, rss.ImportUnreachable, "")
	if !strings.Contains(got.Error, "panic: boom") {
		t.Errorf("Error = %q; want the panic", got.Error)
	}
}

func TestProcessCanceledRecordsNothing(t *testing.T) {
	tr := newTestRunner()
	ctx, cancel := context.WithCancel(t.Context())
	tr.disc.DiscoverFn = func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		cancel() // shutdown during the fetch
		return discovery.Result{}, ctx.Err()
	}

	tr.process(ctx, rss.ImportFeed{ImportID: "imp-1", UserID: "user-1", URL: "https://example.com/feed"})
	if len(tr.store.finished) != 0 {
		t.Errorf("recorded %+v; want nothing", tr.store.finished)
	}
}

func TestRunProcessesQueueAndStops(t *testing.T) {
	tr := newTestRunner()
	tr.feeds.put(rss.Feed{ID: "feed-1", Url: "https://a.example.com/feed"})
	tr.feeds.put(rss.Feed{ID: "feed-2", Url: "https://b.example.com/feed"})
	tr.store.pending = []rss.ImportFeed{
		{ImportID: "imp-1", UserID: "user-1", Position: 0, URL: "https://a.example.com/feed"},
		{ImportID: "imp-1", UserID: "user-1", Position: 1, URL: "https://b.example.com/feed"},
	}
	tr.PollInterval = time.Millisecond
	tr.Workers = 1 // the fakes' maps aren't safe for concurrent writes

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- tr.Run(ctx) }()

	for range 2 {
		select {
		case <-tr.store.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a feed outcome")
		}
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of cancel")
	}

	if !tr.subs.subs["user-1 feed-1"] || !tr.subs.subs["user-1 feed-2"] {
		t.Errorf("subscriptions = %v; want both feeds", tr.subs.subs)
	}
}

func TestRunRejectsStaleAfterNotAboveJobTimeout(t *testing.T) {
	r := &Runner{JobTimeout: time.Minute, StaleAfter: time.Minute}
	if err := r.Run(t.Context()); err == nil {
		t.Error("Run = nil; want an error")
	}
}
