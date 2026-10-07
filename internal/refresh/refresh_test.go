package refresh

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

type fakeStore struct {
	claimDue func(ctx context.Context, lease time.Duration) (rss.Feed, error)
}

func (s *fakeStore) ClaimDue(ctx context.Context, lease time.Duration) (rss.Feed, error) {
	return s.claimDue(ctx, lease)
}

type fakeRefresher struct {
	refresh func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error)
}

func (f *fakeRefresher) Refresh(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
	return f.refresh(ctx, feed)
}

// queue returns a ClaimDue that hands out feeds in order, then
// rss.ErrNoRecord.
func queue(feeds ...rss.Feed) func(context.Context, time.Duration) (rss.Feed, error) {
	var mu sync.Mutex
	return func(ctx context.Context, lease time.Duration) (rss.Feed, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(feeds) == 0 {
			return rss.Feed{}, rss.ErrNoRecord
		}
		f := feeds[0]
		feeds = feeds[1:]
		return f, nil
	}
}

// start runs r in the background and returns a func that cancels it and
// returns Run's error, failing the test if Run doesn't return promptly.
func start(t *testing.T, r *Runner) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- r.Run(ctx) }()
	return func() error {
		t.Helper()
		cancel()
		select {
		case err := <-errc:
			return err
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after cancel")
			return nil
		}
	}
}

// waitFor fails the test if ch doesn't receive within a second.
func waitFor(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

func TestRunRefreshesEachClaimedFeedOnce(t *testing.T) {
	refreshed := make(chan string, 10)
	r := &Runner{
		Store: &fakeStore{claimDue: queue(rss.Feed{ID: "a"}, rss.Feed{ID: "b"})},
		Refresher: &fakeRefresher{refresh: func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
			refreshed <- feed.ID
			return rss.FetchResult{New: 1}, nil
		}},
		PollInterval: 10 * time.Millisecond,
	}
	stop := start(t, r)

	got := map[string]int{}
	got[waitFor(t, refreshed, "first refresh")]++
	got[waitFor(t, refreshed, "second refresh")]++
	if err := stop(); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	close(refreshed)
	for id := range refreshed {
		got[id]++
	}
	if len(got) != 2 || got["a"] != 1 || got["b"] != 1 {
		t.Errorf("refreshed = %v, want a and b once each", got)
	}
}

func TestRunPassesLeaseToClaimDue(t *testing.T) {
	leases := make(chan time.Duration, 100)
	r := &Runner{
		Store: &fakeStore{claimDue: func(ctx context.Context, lease time.Duration) (rss.Feed, error) {
			select {
			case leases <- lease:
			default:
			}
			return rss.Feed{}, rss.ErrNoRecord
		}},
		Refresher:    &fakeRefresher{},
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
		Lease:        7 * time.Minute,
	}
	stop := start(t, r)
	defer stop()

	select {
	case got := <-leases:
		if got != 7*time.Minute {
			t.Errorf("lease = %s, want 7m", got)
		}
	case <-time.After(time.Second):
		t.Fatal("ClaimDue was not called")
	}
}

func TestRunRefreshDeadline(t *testing.T) {
	const timeout = 3 * time.Second
	deadlines := make(chan time.Time, 1)
	before := time.Now()
	r := &Runner{
		Store: &fakeStore{claimDue: queue(rss.Feed{ID: "a"})},
		Refresher: &fakeRefresher{refresh: func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
			d, ok := ctx.Deadline()
			if !ok {
				t.Error("refresh ctx has no deadline")
			}
			deadlines <- d
			return rss.FetchResult{}, nil
		}},
		PollInterval: 10 * time.Millisecond,
		JobTimeout:   timeout,
	}
	stop := start(t, r)
	defer stop()

	select {
	case d := <-deadlines:
		if after := time.Now(); d.After(after.Add(timeout)) {
			t.Errorf("deadline %s is later than JobTimeout from the start", d.Sub(before))
		}
	case <-time.After(time.Second):
		t.Fatal("feed was not refreshed")
	}
}

func TestRunKeepsWorkingAfterRefreshFailure(t *testing.T) {
	tests := []struct {
		name string
		fail func() (rss.FetchResult, error)
	}{
		{"error", func() (rss.FetchResult, error) { return rss.FetchResult{}, errors.New("boom") }},
		{"panic", func() (rss.FetchResult, error) { panic("boom") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refreshed := make(chan string, 10)
			r := &Runner{
				Store: &fakeStore{claimDue: queue(rss.Feed{ID: "bad"}, rss.Feed{ID: "good"})},
				Refresher: &fakeRefresher{refresh: func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
					if feed.ID == "bad" {
						return tt.fail()
					}
					refreshed <- feed.ID
					return rss.FetchResult{}, nil
				}},
				Workers:      1,
				PollInterval: 10 * time.Millisecond,
			}
			stop := start(t, r)
			defer stop()

			if got := waitFor(t, refreshed, "the next feed's refresh"); got != "good" {
				t.Errorf("refreshed %q, want good", got)
			}
		})
	}
}

func TestRunKeepsClaimingAfterClaimError(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	refreshed := make(chan string, 10)
	r := &Runner{
		Store: &fakeStore{claimDue: func(ctx context.Context, lease time.Duration) (rss.Feed, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			switch calls {
			case 1:
				return rss.Feed{}, errors.New("db down")
			case 2:
				return rss.Feed{ID: "a"}, nil
			}
			return rss.Feed{}, rss.ErrNoRecord
		}},
		Refresher: &fakeRefresher{refresh: func(ctx context.Context, feed rss.Feed) (rss.FetchResult, error) {
			refreshed <- feed.ID
			return rss.FetchResult{}, nil
		}},
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
	}
	stop := start(t, r)
	defer stop()

	if got := waitFor(t, refreshed, "refresh after a claim error"); got != "a" {
		t.Errorf("refreshed %q, want a", got)
	}
}

func TestRunReturnsPromptlyWhenCanceledWhileWaiting(t *testing.T) {
	claimed := make(chan string, 100)
	r := &Runner{
		Store: &fakeStore{claimDue: func(ctx context.Context, lease time.Duration) (rss.Feed, error) {
			select {
			case claimed <- "":
			default:
			}
			return rss.Feed{}, rss.ErrNoRecord
		}},
		Refresher:    &fakeRefresher{},
		PollInterval: time.Hour,
	}
	stop := start(t, r)

	waitFor(t, claimed, "a claim")
	if err := stop(); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestRunRejectsLeaseNotExceedingJobTimeout(t *testing.T) {
	tests := []struct {
		name       string
		lease      time.Duration
		jobTimeout time.Duration
	}{
		{"equal", 30 * time.Second, 30 * time.Second},
		{"shorter", 10 * time.Second, 30 * time.Second},
		{"default timeout", 20 * time.Second, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Runner{
				Store: &fakeStore{claimDue: func(ctx context.Context, lease time.Duration) (rss.Feed, error) {
					t.Error("ClaimDue called despite invalid config")
					return rss.Feed{}, rss.ErrNoRecord
				}},
				Refresher:  &fakeRefresher{},
				Lease:      tt.lease,
				JobTimeout: tt.jobTimeout,
			}
			if err := r.Run(context.Background()); err == nil {
				t.Fatal("Run() error = nil, want an error")
			}
		})
	}
}
