package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grodier/rss/internal/rss"
)

const (
	defaultWorkers       = 1
	defaultPollInterval  = 10 * time.Second
	defaultSendTimeout   = 30 * time.Second
	defaultLease         = 2 * time.Minute
	defaultMaxAttempts   = 8
	defaultSweepInterval = time.Hour
	defaultKeepFor       = 7 * 24 * time.Hour

	// maxRetryDelay caps the wait between attempts (see retryDelay).
	maxRetryDelay = time.Hour

	// recordTimeout bounds writing a send's outcome, which uses a fresh
	// context so shutting down doesn't prevent it.
	recordTimeout = 5 * time.Second
)

// Queued is a claimed outbox row.
type Queued struct {
	ID       string
	Message  Message
	Attempts int // including this one
}

// Store is the outbox; satisfied by *psql.EmailRepository.
type Store interface {
	ClaimNext(ctx context.Context, lease time.Duration) (Queued, error) // rss.ErrNoRecord when nothing is due
	MarkSent(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id, msg string, retryAt time.Time) error
	GiveUp(ctx context.Context, id, msg string) error
	DeleteFinished(ctx context.Context, before time.Time) (int64, error)
}

// Runner sends the emails queued in the outbox with a pool of workers,
// retrying failures, and deletes finished emails. Zero values use the
// defaults.
type Runner struct {
	Store         Store
	Sender        Sender
	Logger        *slog.Logger
	Workers       int           // default 1
	PollInterval  time.Duration // default 10s; wait after finding nothing due, unless woken
	SendTimeout   time.Duration // default 30s
	Lease         time.Duration // default 2m (must exceed SendTimeout)
	MaxAttempts   int           // default 8; the email is given up on after this many
	SweepInterval time.Duration // default 1h; how often finished emails are deleted
	KeepFor       time.Duration // default 7 days; how long finished emails are kept

	wakeOnce sync.Once
	wake     chan struct{}
}

// Run deletes finished emails, then runs Workers goroutines and deletes
// finished emails again every SweepInterval until ctx is canceled, and
// returns after they have all stopped.
func (r *Runner) Run(ctx context.Context) error {
	r.setDefaults()
	if r.Lease <= r.SendTimeout {
		return fmt.Errorf("mail: Lease (%s) must exceed SendTimeout (%s)", r.Lease, r.SendTimeout)
	}

	r.Logger.Info("mail runner started", "workers", r.Workers)
	r.sweep(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(r.SweepInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.sweep(ctx)
			}
		}
	}()
	for range r.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.work(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// Wake makes an idle worker check the outbox now instead of at its next
// poll. It never blocks, and does nothing on a nil *Runner.
func (r *Runner) Wake() {
	if r == nil {
		return
	}
	select {
	case r.wakeCh() <- struct{}{}:
	default:
	}
}

// wakeCh returns the one-slot channel Wake signals idle workers on.
func (r *Runner) wakeCh() chan struct{} {
	r.wakeOnce.Do(func() { r.wake = make(chan struct{}, 1) })
	return r.wake
}

func (r *Runner) setDefaults() {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Workers <= 0 {
		r.Workers = defaultWorkers
	}
	if r.PollInterval <= 0 {
		r.PollInterval = defaultPollInterval
	}
	if r.SendTimeout <= 0 {
		r.SendTimeout = defaultSendTimeout
	}
	if r.Lease <= 0 {
		r.Lease = defaultLease
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = defaultMaxAttempts
	}
	if r.SweepInterval <= 0 {
		r.SweepInterval = defaultSweepInterval
	}
	if r.KeepFor <= 0 {
		r.KeepFor = defaultKeepFor
	}
}

// sweep deletes emails sent or given up on more than KeepFor ago.
func (r *Runner) sweep(ctx context.Context) {
	n, err := r.Store.DeleteFinished(ctx, time.Now().Add(-r.KeepFor))
	if err != nil {
		if ctx.Err() == nil {
			r.Logger.Error("mail: delete finished emails", "error", err)
		}
		return
	}
	if n > 0 {
		r.Logger.Info("mail: deleted finished emails", "count", n)
	}
}

func (r *Runner) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		q, err := r.Store.ClaimNext(ctx, r.Lease)
		if err != nil {
			if !errors.Is(err, rss.ErrNoRecord) && ctx.Err() == nil {
				r.Logger.Error("mail: claim next email", "error", err)
			}
			if !r.wait(ctx) {
				return
			}
			continue
		}

		r.send(ctx, q)
	}
}

// wait sleeps for PollInterval or until Wake is called, and reports whether
// ctx is still live.
func (r *Runner) wait(ctx context.Context) bool {
	t := time.NewTimer(r.PollInterval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	case <-r.wakeCh():
		return true
	}
}

// send sends one email and records the outcome: sent, failed with a retry
// time, or given up on after MaxAttempts. If ctx is canceled during a failed
// send, nothing is recorded and the lease retries the email.
func (r *Runner) send(ctx context.Context, q Queued) {
	err := r.deliver(ctx, q.Message)
	if err != nil && ctx.Err() != nil {
		r.Logger.Info("email interrupted by shutdown", "id", q.ID)
		return
	}

	recCtx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()

	switch {
	case err == nil:
		r.Logger.Info("email sent", "id", q.ID, "attempts", q.Attempts)
		if rerr := r.Store.MarkSent(recCtx, q.ID); rerr != nil {
			r.Logger.Error("mail: record sent email", "id", q.ID, "error", rerr)
		}
	case q.Attempts >= r.MaxAttempts:
		r.Logger.Error("email failed", "id", q.ID, "attempts", q.Attempts, "error", err, "gave_up", true)
		if rerr := r.Store.GiveUp(recCtx, q.ID, err.Error()); rerr != nil {
			r.Logger.Error("mail: record failed email", "id", q.ID, "error", rerr)
		}
	default:
		retryAt := time.Now().Add(retryDelay(q.Attempts))
		r.Logger.Warn("email failed", "id", q.ID, "attempts", q.Attempts, "error", err, "retry_at", retryAt)
		if rerr := r.Store.MarkFailed(recCtx, q.ID, err.Error(), retryAt); rerr != nil {
			r.Logger.Error("mail: record failed email", "id", q.ID, "error", rerr)
		}
	}
}

// deliver sends m within SendTimeout. A panic is returned as an error.
func (r *Runner) deliver(ctx context.Context, m Message) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()

	sendCtx, cancel := context.WithTimeout(ctx, r.SendTimeout)
	defer cancel()
	return r.Sender.Send(sendCtx, m)
}

// retryDelay is how long to wait after a failed attempt (attempts counts it)
// before the next one: 1 minute, doubling each time, capped at an hour.
func retryDelay(attempts int) time.Duration {
	d := time.Minute
	for i := 1; i < attempts && d < maxRetryDelay; i++ {
		d *= 2
	}
	return min(d, maxRetryDelay)
}
