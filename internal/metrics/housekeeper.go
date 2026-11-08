package metrics

import (
	"context"
	"log/slog"
	"time"
)

// Retention: minute rows are rolled into hourly rows once they're RollUpAfter
// old, and real-user views into daily rows once their UTC day has been over
// for RUMRollUpAfter, and each kind of row is deleted after its age below
// (see "Retention" under "Observability" in docs/decisions.md).
const (
	RollUpAfter      = 2 * time.Hour
	MinuteRetention  = 7 * 24 * time.Hour
	HourRetention    = 90 * 24 * time.Hour
	RuntimeRetention = 7 * 24 * time.Hour
	// RUMRollUpAfter leaves a day's last beacons time to be stored.
	RUMRollUpAfter   = time.Hour
	RUMViewRetention = 30 * 24 * time.Hour
	RUMDailyMonths   = 13
	// RUMViewsKept caps the real-user views however recent, so a flood of
	// beacons can't fill the disk.
	RUMViewsKept        = 500_000
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

// RUMHousekeepingStore rolls up and deletes old real-user views; satisfied
// by *psql.RUMRepository.
type RUMHousekeepingStore interface {
	// RollUp counts the views of each whole UTC day before before into daily
	// rows, leaving days that already have them alone.
	RollUp(ctx context.Context, before time.Time) error
	// DeleteBefore deletes views received before views, and the daily rows
	// of the UTC days before daily's.
	DeleteBefore(ctx context.Context, views, daily time.Time) error
	// Trim deletes every view but the newest keep.
	Trim(ctx context.Context, keep int) error
}

// Housekeeper rolls up and deletes old metrics and real-user views, and
// deletes all but the newest ErrorsKept server errors and RUMViewsKept
// views, once an hour. Every
// instance runs one; the work is idempotent, so they don't need to
// coordinate.
type Housekeeper struct {
	Store  HousekeepingStore
	RUM    RUMHousekeepingStore
	Errors ErrorTrimmer
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

// Tidy rolls up and deletes the metrics and real-user views that are old at
// now, and trims the server errors.
func (h *Housekeeper) Tidy(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, housekeepingTimeout)
	defer cancel()
	if err := h.Errors.Trim(ctx, ErrorsKept); err != nil && ctx.Err() == nil {
		logger(h.Logger).Error("metrics: trim server errors", "error", err)
	}
	h.tidyRequests(ctx, now)
	h.tidyRUM(ctx, now)
}

func (h *Housekeeper) tidyRequests(ctx context.Context, now time.Time) {
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

func (h *Housekeeper) tidyRUM(ctx context.Context, now time.Time) {
	// The cap holds even when the rollup fails, as a flood that makes it
	// fail is what the cap is for.
	defer func() {
		if err := h.RUM.Trim(ctx, RUMViewsKept); err != nil && ctx.Err() == nil {
			logger(h.Logger).Error("metrics: trim real-user views", "error", err)
		}
	}()
	if err := h.RUM.RollUp(ctx, now.Add(-RUMRollUpAfter)); err != nil {
		if ctx.Err() == nil {
			logger(h.Logger).Error("metrics: roll up real-user views", "error", err)
		}
		// Deleting views by age that weren't rolled up would lose them.
		return
	}
	err := h.RUM.DeleteBefore(ctx, now.Add(-RUMViewRetention), now.AddDate(0, -RUMDailyMonths, 0))
	if err != nil && ctx.Err() == nil {
		logger(h.Logger).Error("metrics: delete old real-user views", "error", err)
	}
}
