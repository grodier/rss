package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/grodier/rss/internal/rss"
)

const (
	// ErrorLogCapacity is how many server errors can wait to be written;
	// more are dropped.
	ErrorLogCapacity = 100
	// ErrorsKept is how many server errors the housekeeping keeps.
	ErrorsKept        = 1000
	errorWriteTimeout = 2 * time.Second
)

// ErrorStore stores server errors; satisfied by *psql.ServerErrorRepository.
type ErrorStore interface {
	Record(ctx context.Context, e rss.ServerError) error
}

// ErrorTrimmer deletes all but the newest server errors; satisfied by
// *psql.ServerErrorRepository.
type ErrorTrimmer interface {
	Trim(ctx context.Context, keep int) error
}

// ErrorLog keeps www's recent server errors for the admin. Record never
// blocks: errors wait in a buffer that Run writes from, and are dropped when
// it's full or their write fails, since the database may be why the request
// failed.
type ErrorLog struct {
	store    ErrorStore
	instance string
	logger   *slog.Logger
	queue    chan rss.ServerError
}

// NewErrorLog returns an ErrorLog that writes to store, naming its errors'
// instance (os.Hostname()).
func NewErrorLog(store ErrorStore, instance string, logger *slog.Logger) *ErrorLog {
	return &ErrorLog{
		store:    store,
		instance: instance,
		logger:   logger,
		queue:    make(chan rss.ServerError, ErrorLogCapacity),
	}
}

// Record queues e to be written, with this instance's name and, if it has
// none, the current time. It drops e, with a warning, when the buffer is full.
func (l *ErrorLog) Record(e rss.ServerError) {
	e.Instance = l.instance
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	select {
	case l.queue <- e:
	default:
		logger(l.logger).Warn("metrics: server error log full; error not recorded", "route", e.Route, "method", e.Method)
	}
}

// Run writes queued errors, one at a time, until ctx is canceled.
func (l *ErrorLog) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-l.queue:
			l.write(ctx, e)
		}
	}
}

func (l *ErrorLog) write(ctx context.Context, e rss.ServerError) {
	ctx, cancel := context.WithTimeout(ctx, errorWriteTimeout)
	defer cancel()
	if err := l.store.Record(ctx, e); err != nil {
		logger(l.logger).Error("metrics: record server error", "route", e.Route, "method", e.Method, "error", err)
	}
}
