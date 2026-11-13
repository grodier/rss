package psql

import (
	"context"
	"database/sql"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
)

// AvailabilityRepository stores the admin's checks of www's /healthcheck
// (availability_checks).
type AvailabilityRepository struct {
	DB *sql.DB
}

func NewAvailabilityRepository(db *sql.DB) *AvailabilityRepository {
	return &AvailabilityRepository{DB: db}
}

// MaxCheckText is the most bytes of a check's status and version that Record
// keeps: the answer is www's, but a misconfigured URL could reach anything.
const MaxCheckText = 100

// Record stores c, with its status and version cut to MaxCheckText bytes.
func (r *AvailabilityRepository) Record(ctx context.Context, c rss.AvailabilityCheck) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO availability_checks (checked_at, ok, status, latency_ms, version)
		VALUES ($1, $2, $3, $4, $5)`,
		c.CheckedAt, c.OK, textcut.Bytes(c.Status, MaxCheckText), c.LatencyMs, textcut.Bytes(c.Version, MaxCheckText))
	return err
}

// DeleteBefore deletes the checks made before before.
func (r *AvailabilityRepository) DeleteBefore(ctx context.Context, before time.Time) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM availability_checks WHERE checked_at < $1`, before)
	return err
}
