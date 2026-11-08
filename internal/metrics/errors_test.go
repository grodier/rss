package metrics

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// fakeErrorStore records the errors it's given, failing with err.
type fakeErrorStore struct {
	mu       sync.Mutex
	err      error
	recorded []rss.ServerError
	written  chan struct{}
}

func (f *fakeErrorStore) Record(ctx context.Context, e rss.ServerError) error {
	f.mu.Lock()
	f.recorded = append(f.recorded, e)
	f.mu.Unlock()
	if f.written != nil {
		f.written <- struct{}{}
	}
	return f.err
}

func (f *fakeErrorStore) errors() []rss.ServerError {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rss.ServerError(nil), f.recorded...)
}

func TestErrorLogWrites(t *testing.T) {
	store := &fakeErrorStore{written: make(chan struct{}, 1)}
	l := NewErrorLog(store, "web-1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Run(ctx)
	}()

	l.Record(rss.ServerError{Route: "/feeds/{id}", Method: "GET", Message: "boom", Panic: true})
	select {
	case <-store.written:
	case <-time.After(5 * time.Second):
		t.Fatal("error not written")
	}
	cancel()
	<-done

	got := store.errors()
	if len(got) != 1 {
		t.Fatalf("wrote %d errors, want 1", len(got))
	}
	e := got[0]
	if e.Instance != "web-1" || e.Route != "/feeds/{id}" || e.Method != "GET" || e.Message != "boom" || !e.Panic {
		t.Errorf("wrote %+v", e)
	}
	if time.Since(e.OccurredAt) > time.Minute {
		t.Errorf("OccurredAt = %v, want about now", e.OccurredAt)
	}
}

func TestErrorLogFailedWriteLogged(t *testing.T) {
	var logs bytes.Buffer
	store := &fakeErrorStore{err: errors.New("database down")}
	l := NewErrorLog(store, "web-1", slog.New(slog.NewTextHandler(&logs, nil)))
	l.write(context.Background(), rss.ServerError{Route: "/", Method: "GET", Message: "boom"})
	if !strings.Contains(logs.String(), "database down") {
		t.Errorf("logs = %q, want the failed write", logs.String())
	}
}

func TestErrorLogFullDrops(t *testing.T) {
	var logs bytes.Buffer
	l := NewErrorLog(&fakeErrorStore{}, "web-1", slog.New(slog.NewTextHandler(&logs, nil)))
	// Nothing drains the buffer: the one after capacity is dropped.
	for range ErrorLogCapacity + 1 {
		l.Record(rss.ServerError{Route: "/", Method: "GET", Message: "boom"})
	}
	if n := len(l.queue); n != ErrorLogCapacity {
		t.Errorf("%d queued, want %d", n, ErrorLogCapacity)
	}
	if strings.Count(logs.String(), "error log full") != 1 {
		t.Errorf("logs = %q, want one warning", logs.String())
	}
}
