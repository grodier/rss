package mail

import (
	"context"
	"errors"
	"testing"
)

type fakeOutbox struct {
	err    error
	queued []Message
}

func (f *fakeOutbox) Enqueue(ctx context.Context, m Message) error {
	if f.err != nil {
		return f.err
	}
	f.queued = append(f.queued, m)
	return nil
}

// woken reports whether r has a pending Wake, consuming it.
func woken(r *Runner) bool {
	select {
	case <-r.wakeCh():
		return true
	default:
		return false
	}
}

func TestQueueEnqueue(t *testing.T) {
	msg := Message{To: "someone@example.com", Subject: "Hi"}

	t.Run("queues the message and wakes the runner", func(t *testing.T) {
		store := &fakeOutbox{}
		r := &Runner{}
		q := &Queue{Store: store, Runner: r}

		if err := q.Enqueue(t.Context(), msg); err != nil {
			t.Fatalf("Enqueue() error = %v, want nil", err)
		}
		if len(store.queued) != 1 || store.queued[0] != msg {
			t.Errorf("queued %+v, want %+v", store.queued, msg)
		}
		if !woken(r) {
			t.Error("the runner wasn't woken")
		}
	})

	t.Run("returns the store's error without waking the runner", func(t *testing.T) {
		boom := errors.New("boom")
		r := &Runner{}
		q := &Queue{Store: &fakeOutbox{err: boom}, Runner: r}

		if err := q.Enqueue(t.Context(), msg); !errors.Is(err, boom) {
			t.Fatalf("Enqueue() error = %v, want %v", err, boom)
		}
		if woken(r) {
			t.Error("the runner was woken")
		}
	})

	t.Run("a nil runner is fine", func(t *testing.T) {
		store := &fakeOutbox{}
		q := &Queue{Store: store}

		if err := q.Enqueue(t.Context(), msg); err != nil {
			t.Fatalf("Enqueue() error = %v, want nil", err)
		}
		if len(store.queued) != 1 {
			t.Errorf("queued %d messages, want 1", len(store.queued))
		}
	})
}
