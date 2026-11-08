package metrics

import (
	"context"
	"log/slog"
	"time"
)

// Retention: minute rows are rolled into hourly rows once they're RollUpAfter
// old, and each kind of row is deleted after its age below (see "Retention"
// under "Observability" in docs/decisions.md).
const (
	RollUpAfter         = 2 * time.Hour
	MinuteRetention     = 7 * 24 * time.Hour
	HourRetention       = 90 * 24 * time.Hour
	RuntimeRetention    = 7 * 24 * time.Hour
	housekeepingPeriod  = time.Hour
	housekeepingTimeout = time.Minute
)

// HousekeepingStore rolls up and deletes old metrics; satisfied by
// *psql.MetricsRepository.
type HousekeepingStore interface {
	// RollUp sums the minute rows of the whole hours before before into
	// hourly rows, leaving hours that already have them alone.
	RollUp(ctx context.Context, before time.Time) error
	// DeleteBefore deletes minute rows older than minutes, hourly rows older
	// than hours, and runtime rows older than runtime.
	DeleteBefore(ctx context.Context, minutes, hours, runtime time.Time) error
}

// Housekeeper rolls up and deletes old metrics once an hour. Every instance
// runs one; the work is idempotent, so they don't need to coordinate.
type Housekeeper struct {
	Store  HousekeepingStore
	Logger *slog.Logger
}

// Run does the housekeeping now and then every hour until ctx is canceled.
func (h *Housekeeper) Run(ctx context.Context) error {
	t := time.NewTicker(housekeepingPeriod)
	defer t.Stop()
	for {
		h.Tidy(ctx, time.Now())
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Tidy rolls up and deletes the metrics that are old at now.
func (h *Housekeeper) Tidy(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, housekeepingTimeout)
	defer cancel()
	if err := h.Store.RollUp(ctx, now.Add(-RollUpAfter)); err != nil {
		if ctx.Err() == nil {
			logger(h.Logger).Error("metrics: roll up request metrics", "error", err)
		}
		// Deleting minute rows that weren't rolled up would lose them.
		return
	}
	err := h.Store.DeleteBefore(ctx, now.Add(-MinuteRetention), now.Add(-HourRetention), now.Add(-RuntimeRetention))
	if err != nil && ctx.Err() == nil {
		logger(h.Logger).Error("metrics: delete old metrics", "error", err)
	}
}
