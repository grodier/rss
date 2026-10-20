package mail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeStore is an outbox that hands out queued emails in order, then
// rss.ErrNoRecord, and reports what the runner recorded on its channels.
type fakeStore struct {
	mu     sync.Mutex
	queued []Queued
	claims int

	sent    chan string
	failed  chan failure
	gaveUp  chan failure
	deletes chan time.Time
}

type failure struct {
	id, msg string
	retryAt time.Time
}

func newFakeStore(queued ...Queued) *fakeStore {
	return &fakeStore{
		queued:  queued,
		sent:    make(chan string, 10),
		failed:  make(chan failure, 10),
		gaveUp:  make(chan failure, 10),
		deletes: make(chan time.Time, 10),
	}
}

func (s *fakeStore) ClaimNext(ctx context.Context, lease time.Duration) (Queued, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if len(s.queued) == 0 {
		return Queued{}, rss.ErrNoRecord
	}
	q := s.queued[0]
	s.queued = s.queued[1:]
	return q, nil
}

// add queues q for the next ClaimNext.
func (s *fakeStore) add(q Queued) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queued = append(s.queued, q)
}

func (s *fakeStore) claimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *fakeStore) MarkSent(ctx context.Context, id string) error {
	s.sent <- id
	return nil
}

func (s *fakeStore) MarkFailed(ctx context.Context, id, msg string, retryAt time.Time) error {
	s.failed <- failure{id: id, msg: msg, retryAt: retryAt}
	return nil
}

func (s *fakeStore) GiveUp(ctx context.Context, id, msg string) error {
	s.gaveUp <- failure{id: id, msg: msg}
	return nil
}

func (s *fakeStore) DeleteFinished(ctx context.Context, before time.Time) (int64, error) {
	select {
	case s.deletes <- before:
	default:
	}
	return 0, nil
}

type fakeSender struct {
	send func(ctx context.Context, m Message) error
}

func (f *fakeSender) Send(ctx context.Context, m Message) error {
	return f.send(ctx, m)
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

// receive fails the test if ch doesn't receive within a second.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestRunSendsQueuedEmail(t *testing.T) {
	msg := Message{To: "someone@example.com", Subject: "Hi", Text: "text", HTML: "html"}
	store := newFakeStore(Queued{ID: "a", Message: msg, Attempts: 1})
	got := make(chan Message, 1)
	r := &Runner{
		Store: store,
		Sender: &fakeSender{send: func(ctx context.Context, m Message) error {
			got <- m
			return nil
		}},
		PollInterval: 10 * time.Millisecond,
	}
	stop := start(t, r)
	defer stop()

	if m := receive(t, got, "the send"); m != msg {
		t.Errorf("sent %+v, want %+v", m, msg)
	}
	if id := receive(t, store.sent, "MarkSent"); id != "a" {
		t.Errorf("MarkSent(%q), want a", id)
	}
}

func TestRunSendTimeout(t *testing.T) {
	const timeout = 3 * time.Second
	deadlines := make(chan time.Time, 1)
	before := time.Now()
	r := &Runner{
		Store: newFakeStore(Queued{ID: "a", Attempts: 1}),
		Sender: &fakeSender{send: func(ctx context.Context, m Message) error {
			d, ok := ctx.Deadline()
			if !ok {
				t.Error("send ctx has no deadline")
			}
			deadlines <- d
			return nil
		}},
		PollInterval: 10 * time.Millisecond,
		SendTimeout:  timeout,
	}
	stop := start(t, r)
	defer stop()

	if d := receive(t, deadlines, "the send"); d.After(time.Now().Add(timeout)) || d.Before(before.Add(timeout)) {
		t.Errorf("deadline %s after the start, want SendTimeout (%s)", d.Sub(before), timeout)
	}
}

func TestRunRetriesFailedSends(t *testing.T) {
	tests := []struct {
		attempts  int
		wantDelay time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{7, time.Hour},
	}
	for _, tt := range tests {
		store := newFakeStore(Queued{ID: "a", Attempts: tt.attempts})
		r := &Runner{
			Store: store,
			Sender: &fakeSender{send: func(ctx context.Context, m Message) error {
				return errors.New("421 try again later")
			}},
			PollInterval: 10 * time.Millisecond,
		}
		before := time.Now()
		stop := start(t, r)

		f := receive(t, store.failed, "MarkFailed")
		after := time.Now()
		stop()

		if f.id != "a" || f.msg != "421 try again later" {
			t.Errorf("attempt %d: MarkFailed(%q, %q), want a and the send error", tt.attempts, f.id, f.msg)
		}
		if f.retryAt.Before(before.Add(tt.wantDelay)) || f.retryAt.After(after.Add(tt.wantDelay)) {
			t.Errorf("attempt %d: retryAt is %s from now, want %s", tt.attempts, f.retryAt.Sub(before), tt.wantDelay)
		}
		select {
		case g := <-store.gaveUp:
			t.Errorf("attempt %d: GiveUp(%q), want MarkFailed only", tt.attempts, g.id)
		default:
		}
	}
}

func TestRunGivesUpAfterMaxAttempts(t *testing.T) {
	tests := []struct {
		name string
		send func() error
		want string
	}{
		{"error", func() error { return errors.New("550 no such user") }, "550 no such user"},
		{"panic", func() error { panic("boom") }, "panic: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore(Queued{ID: "a", Attempts: 8})
			r := &Runner{
				Store:        store,
				Sender:       &fakeSender{send: func(ctx context.Context, m Message) error { return tt.send() }},
				PollInterval: 10 * time.Millisecond,
			}
			stop := start(t, r)
			defer stop()

			g := receive(t, store.gaveUp, "GiveUp")
			if g.id != "a" || g.msg != tt.want {
				t.Errorf("GiveUp(%q, %q), want a and %q", g.id, g.msg, tt.want)
			}
			select {
			case f := <-store.failed:
				t.Errorf("MarkFailed(%q), want GiveUp only", f.id)
			default:
			}
		})
	}
}

func TestRunKeepsWorkingAfterFailure(t *testing.T) {
	store := newFakeStore(
		Queued{ID: "bad", Attempts: 1},
		Queued{ID: "good", Message: Message{To: "good@example.com"}, Attempts: 1},
	)
	r := &Runner{
		Store: store,
		Sender: &fakeSender{send: func(ctx context.Context, m Message) error {
			if m.To == "" {
				panic("boom")
			}
			return nil
		}},
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
	}
	stop := start(t, r)
	defer stop()

	if id := receive(t, store.sent, "the next email's MarkSent"); id != "good" {
		t.Errorf("MarkSent(%q), want good", id)
	}
}

func TestWakeClaimsWithinASecond(t *testing.T) {
	store := newFakeStore()
	r := &Runner{
		Store: store,
		Sender: &fakeSender{send: func(ctx context.Context, m Message) error {
			return nil
		}},
		Workers:      1,
		PollInterval: time.Hour,
	}
	stop := start(t, r)
	defer stop()

	// Wait for the worker to find nothing due and go idle.
	deadline := time.Now().Add(time.Second)
	for store.claimCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the worker never claimed")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)

	store.add(Queued{ID: "a", Attempts: 1})
	r.Wake()

	if id := receive(t, store.sent, "MarkSent after Wake"); id != "a" {
		t.Errorf("MarkSent(%q), want a", id)
	}
}

func TestWakeNilRunner(t *testing.T) {
	var r *Runner
	r.Wake() // must not panic
}

func TestWakeNeverBlocks(t *testing.T) {
	r := &Runner{}
	done := make(chan struct{})
	go func() {
		// Nothing is running to take these.
		r.Wake()
		r.Wake()
		r.Wake()
		close(done)
	}()
	receive(t, done, "Wake to return")
}

func TestRunReturnsWhenCanceled(t *testing.T) {
	r := &Runner{
		Store:        newFakeStore(),
		Sender:       &fakeSender{},
		Workers:      2,
		PollInterval: time.Hour,
	}
	stop := start(t, r)
	if err := stop(); err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
}

func TestRunLeaseMustExceedSendTimeout(t *testing.T) {
	for _, lease := range []time.Duration{10 * time.Second, 30 * time.Second} {
		r := &Runner{
			Store:       newFakeStore(),
			Sender:      &fakeSender{},
			SendTimeout: 30 * time.Second,
			Lease:       lease,
		}
		err := r.Run(t.Context())
		if err == nil || !strings.Contains(err.Error(), "must exceed SendTimeout") {
			t.Errorf("Lease %s: Run() error = %v, want it to say Lease must exceed SendTimeout", lease, err)
		}
	}
}

func TestRunSweepsFinishedEmails(t *testing.T) {
	store := newFakeStore()
	r := &Runner{
		Store:         store,
		Sender:        &fakeSender{},
		PollInterval:  time.Hour,
		SweepInterval: 10 * time.Millisecond,
		KeepFor:       48 * time.Hour,
	}
	before := time.Now()
	stop := start(t, r)
	defer stop()

	// Once at the start, then every SweepInterval.
	for i := range 2 {
		cutoff := receive(t, store.deletes, "DeleteFinished")
		after := time.Now()
		if cutoff.Before(before.Add(-r.KeepFor)) || cutoff.After(after.Add(-r.KeepFor)) {
			t.Errorf("sweep %d: DeleteFinished(now - %s), want now - KeepFor (%s)", i, time.Since(cutoff), r.KeepFor)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{6, 32 * time.Minute},
		{7, time.Hour},
		{8, time.Hour},
		{100, time.Hour},
	}
	for _, tt := range tests {
		if got := retryDelay(tt.attempts); got != tt.want {
			t.Errorf("retryDelay(%d) = %s, want %s", tt.attempts, got, tt.want)
		}
	}
}
