package lookup

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

type resetCall struct {
	olderThan   time.Duration
	maxAttempts int
}

// fakeStore hands out pending jobs in order and records outcomes.
type fakeStore struct {
	mu       sync.Mutex
	pending  []rss.Lookup
	finished map[string]string // job ID → site ID
	failed   map[string]string // job ID → message
	resets   []resetCall
	done     chan struct{} // receives once per Finish/Fail
}

func newFakeStore(jobs ...rss.Lookup) *fakeStore {
	return &fakeStore{
		pending:  jobs,
		finished: map[string]string{},
		failed:   map[string]string{},
		done:     make(chan struct{}, 100),
	}
}

func (s *fakeStore) ClaimNext(ctx context.Context) (rss.Lookup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return rss.Lookup{}, rss.ErrNoRecord
	}
	job := s.pending[0]
	s.pending = s.pending[1:]
	return job, nil
}

func (s *fakeStore) Finish(ctx context.Context, id, siteID string) error {
	s.mu.Lock()
	s.finished[id] = siteID
	s.mu.Unlock()
	s.done <- struct{}{}
	return nil
}

func (s *fakeStore) Fail(ctx context.Context, id, msg string) error {
	s.mu.Lock()
	s.failed[id] = msg
	s.mu.Unlock()
	s.done <- struct{}{}
	return nil
}

func (s *fakeStore) ResetStale(ctx context.Context, olderThan time.Duration, maxAttempts int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets = append(s.resets, resetCall{olderThan, maxAttempts})
	return 0, nil
}

type saveCall struct {
	site  rss.Site
	feeds []rss.FeedWithArticles
}

type fakeSaver struct {
	mu     sync.Mutex
	calls  []saveCall
	siteID string
	err    error
}

func (s *fakeSaver) Save(ctx context.Context, site rss.Site, feeds []rss.FeedWithArticles) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, saveCall{site, feeds})
	return s.siteID, s.err
}

type fakeDiscoverer struct {
	DiscoverFn func(ctx context.Context, u *url.URL) (discovery.Result, error)
}

func (d *fakeDiscoverer) Discover(ctx context.Context, u *url.URL) (discovery.Result, error) {
	return d.DiscoverFn(ctx, u)
}

func job(id string) rss.Lookup {
	return rss.Lookup{ID: id, SiteKey: id + ".example.com", URL: "https://" + id + ".example.com/", Status: rss.LookupRunning}
}

// start runs r in the background and returns a function that cancels it and
// waits for Run to return.
func start(t *testing.T, r *Runner) (stop func()) {
	t.Helper()
	if r.PollInterval == 0 {
		r.PollInterval = time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- r.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-errc:
				if err != nil {
					t.Errorf("Run: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Run did not return within 1s of cancel")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// waitOutcomes waits for n Finish/Fail calls.
func waitOutcomes(t *testing.T, s *fakeStore, n int) {
	t.Helper()
	for range n {
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a job outcome")
		}
	}
}

func TestRunSavesDiscoveredFeeds(t *testing.T) {
	store := newFakeStore(job("a"))
	saver := &fakeSaver{siteID: "site-1"}
	disc := &fakeDiscoverer{DiscoverFn: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		if u.String() != "https://a.example.com/" {
			t.Errorf("Discover URL = %q", u)
		}
		return discovery.Result{
			Site: discovery.SiteInfo{Key: "a.example.com", URL: "https://a.example.com/", Title: "A", Description: "Site A"},
			Feeds: []discovery.FeedInfo{
				{URL: "https://a.example.com/feed.xml", Title: "Posts", Description: "All posts", SiteURL: "https://a.example.com/", Items: []feedparse.Item{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}},
				{URL: "https://a.example.com/comments.xml", Title: "Comments"},
			},
		}, nil
	}}

	stop := start(t, &Runner{Store: store, Saver: saver, Discoverer: disc})
	waitOutcomes(t, store, 1)
	stop()

	if got := store.finished["a"]; got != "site-1" {
		t.Errorf("Finish site ID = %q, want site-1 (finished=%v failed=%v)", got, store.finished, store.failed)
	}
	if len(saver.calls) != 1 {
		t.Fatalf("Save called %d times, want 1", len(saver.calls))
	}
	wantSite := rss.Site{Host: "a.example.com", URL: "https://a.example.com/", Title: "A", Description: "Site A"}
	if saver.calls[0].site != wantSite {
		t.Errorf("Save site = %+v, want %+v", saver.calls[0].site, wantSite)
	}
	wantFeeds := []rss.FeedWithArticles{
		{
			Feed:     rss.Feed{Url: "https://a.example.com/feed.xml", SiteUrl: "https://a.example.com/", Title: "Posts", Description: "All posts"},
			Articles: ingest.Articles([]feedparse.Item{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}),
		},
		{Feed: rss.Feed{Url: "https://a.example.com/comments.xml", Title: "Comments"}, Articles: []rss.Article{}},
	}
	if arts := saver.calls[0].feeds[0].Articles; len(arts) != 2 || arts[0].ExternalID != "a" || arts[1].ExternalID != "b" {
		t.Errorf("first feed articles = %+v, want ExternalIDs a, b", arts)
	}
	for i := range saver.calls[0].feeds {
		f := &saver.calls[0].feeds[i].Feed
		if f.NextFetch.IsZero() {
			t.Errorf("feed %q has zero NextFetch", f.Url)
		}
		f.NextFetch = time.Time{} // jittered; compared above
	}
	if !reflect.DeepEqual(saver.calls[0].feeds, wantFeeds) {
		t.Errorf("Save feeds = %+v, want %+v", saver.calls[0].feeds, wantFeeds)
	}
}

func TestRunNoFeedsFinishesWithoutSaving(t *testing.T) {
	store := newFakeStore(job("a"))
	saver := &fakeSaver{siteID: "site-1"}
	disc := &fakeDiscoverer{DiscoverFn: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return discovery.Result{Site: discovery.SiteInfo{Key: "a.example.com"}}, nil
	}}

	stop := start(t, &Runner{Store: store, Saver: saver, Discoverer: disc})
	waitOutcomes(t, store, 1)
	stop()

	if got, ok := store.finished["a"]; !ok || got != "" {
		t.Errorf("Finish = %q (called %v), want \"\" (failed=%v)", got, ok, store.failed)
	}
	if len(saver.calls) != 0 {
		t.Errorf("Save called %d times, want 0", len(saver.calls))
	}
}

func TestRunFailures(t *testing.T) {
	feeds := discovery.Result{
		Site:  discovery.SiteInfo{Key: "a.example.com"},
		Feeds: []discovery.FeedInfo{{URL: "https://a.example.com/feed.xml"}},
	}

	tests := []struct {
		name    string
		url     string
		disc    func(ctx context.Context, u *url.URL) (discovery.Result, error)
		saveErr error
		wantMsg string // substring; "" to only check Fail was called
	}{
		{
			name: "discover error",
			disc: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
				return discovery.Result{}, errors.New("connection refused")
			},
			wantMsg: "connection refused",
		},
		{
			name: "save error",
			disc: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
				return feeds, nil
			},
			saveErr: errors.New("db down"),
			wantMsg: "db down",
		},
		{
			name: "bad url",
			url:  "http://a b.example.com/%zz",
			disc: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
				t.Error("Discover called for an unparsable URL")
				return feeds, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := job("a")
			if tt.url != "" {
				j.URL = tt.url
			}
			store := newFakeStore(j)
			saver := &fakeSaver{siteID: "site-1", err: tt.saveErr}

			stop := start(t, &Runner{Store: store, Saver: saver, Discoverer: &fakeDiscoverer{DiscoverFn: tt.disc}})
			waitOutcomes(t, store, 1)
			stop()

			msg, ok := store.failed["a"]
			if !ok {
				t.Fatalf("Fail not called (finished=%v)", store.finished)
			}
			if tt.wantMsg != "" && msg != tt.wantMsg {
				t.Errorf("Fail message = %q, want %q", msg, tt.wantMsg)
			}
			if len(store.finished) != 0 {
				t.Errorf("Finish called: %v", store.finished)
			}
		})
	}
}

func TestRunPanicFailsJobAndKeepsWorking(t *testing.T) {
	store := newFakeStore(job("a"), job("b"))
	saver := &fakeSaver{}
	disc := &fakeDiscoverer{DiscoverFn: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		if u.Host == "a.example.com" {
			panic("boom")
		}
		return discovery.Result{}, nil
	}}

	// One worker, so job b is processed by the worker that panicked.
	stop := start(t, &Runner{Store: store, Saver: saver, Discoverer: disc, Workers: 1})
	waitOutcomes(t, store, 2)
	stop()

	if _, ok := store.failed["a"]; !ok {
		t.Errorf("job a not failed after panic (finished=%v)", store.finished)
	}
	if _, ok := store.finished["b"]; !ok {
		t.Errorf("job b not finished after job a panicked (failed=%v)", store.failed)
	}
}

func TestRunResetsStaleOnceAtStart(t *testing.T) {
	store := newFakeStore()
	disc := &fakeDiscoverer{DiscoverFn: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		return discovery.Result{}, nil
	}}

	stop := start(t, &Runner{
		Store: store, Saver: &fakeSaver{}, Discoverer: disc,
		JobTimeout: time.Second, StaleAfter: 5 * time.Minute, MaxAttempts: 7,
	})
	time.Sleep(20 * time.Millisecond) // let workers poll a few times
	stop()

	want := []resetCall{{5 * time.Minute, 7}}
	if !reflect.DeepEqual(store.resets, want) {
		t.Errorf("ResetStale calls = %v, want %v", store.resets, want)
	}
}

func TestRunCancelLeavesInterruptedJobRunning(t *testing.T) {
	store := newFakeStore(job("a"))
	started := make(chan struct{})
	returned := make(chan struct{})
	disc := &fakeDiscoverer{DiscoverFn: func(ctx context.Context, u *url.URL) (discovery.Result, error) {
		defer close(returned)
		close(started)
		<-ctx.Done()
		return discovery.Result{}, ctx.Err()
	}}

	stop := start(t, &Runner{Store: store, Saver: &fakeSaver{}, Discoverer: disc})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job was not started")
	}
	stop() // fails the test if Run doesn't return within 1s

	select {
	case <-returned:
	default:
		t.Error("Run returned before the in-flight Discover did")
	}
	if len(store.finished) != 0 || len(store.failed) != 0 {
		t.Errorf("interrupted job recorded: finished=%v failed=%v", store.finished, store.failed)
	}
}
