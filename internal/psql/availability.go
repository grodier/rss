package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// AvailabilityRepository stores the admin's checks of www's /healthcheck
// (availability_checks).
type AvailabilityRepository struct {
	DB *sql.DB
}

func NewAvailabilityRepository(db *sql.DB) *AvailabilityRepository {
	return &AvailabilityRepository{DB: db}
}

// Record stores c.
func (r *AvailabilityRepository) Record(ctx context.Context, c rss.AvailabilityCheck) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO availability_checks (checked_at, ok, status, latency_ms, version)
		VALUES ($1, $2, $3, $4, $5)`,
		c.CheckedAt, c.OK, c.Status, c.LatencyMs, c.Version)
	return err
}

// DeleteBefore deletes the checks made before before.
func (r *AvailabilityRepository) DeleteBefore(ctx context.Context, before time.Time) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM availability_checks WHERE checked_at < $1`, before)
	return err
}
