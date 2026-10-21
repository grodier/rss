package psql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// UserSessionRepository stores logged-in devices (user_sessions rows).
type UserSessionRepository struct {
	DB *sql.DB
}

func NewUserSessionRepository(db *sql.DB) *UserSessionRepository {
	return &UserSessionRepository{DB: db}
}

// Create logs userID in on a new device and returns the session's ID. It
// first deletes the user's sessions last seen before idleBefore, so idle
// rows don't pile up without a sweeper.
func (r *UserSessionRepository) Create(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	stmt := `DELETE FROM user_sessions WHERE user_id = $1 AND last_seen_at < $2`
	if _, err := tx.ExecContext(ctx, stmt, userID, idleBefore); err != nil {
		return "", err
	}

	var id string
	stmt = `INSERT INTO user_sessions (user_id) VALUES ($1) RETURNING id`
	if err := tx.QueryRowContext(ctx, stmt, userID).Scan(&id); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// Get returns the session with its user. rss.ErrNoRecord if there's no
// session with id.
func (r *UserSessionRepository) Get(ctx context.Context, id string) (rss.Session, error) {
	stmt := `SELECT s.id, s.last_seen_at, u.id, u.name, u.email, u.email_confirmed_at, u.created_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1`

	var s rss.Session
	var emailConfirmedAt sql.NullTime
	err := r.DB.QueryRowContext(ctx, stmt, id).Scan(
		&s.ID, &s.LastSeenAt, &s.User.ID, &s.User.Name, &s.User.Email, &emailConfirmedAt, &s.User.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Session{}, rss.ErrNoRecord
		}
		return rss.Session{}, err
	}
	s.User.EmailConfirmedAt = emailConfirmedAt.Time
	return s, nil
}

// Touch records that the session was used now.
func (r *UserSessionRepository) Touch(ctx context.Context, id string) error {
	stmt := `UPDATE user_sessions SET last_seen_at = now() WHERE id = $1`
	_, err := r.DB.ExecContext(ctx, stmt, id)
	return err
}

// Delete ends the session. Deleting one that's already gone is not an error.
func (r *UserSessionRepository) Delete(ctx context.Context, id string) error {
	stmt := `DELETE FROM user_sessions WHERE id = $1`
	_, err := r.DB.ExecContext(ctx, stmt, id)
	return err
}

// DeleteOthers logs userID out of every device except keepID and returns how
// many sessions it deleted.
func (r *UserSessionRepository) DeleteOthers(ctx context.Context, userID, keepID string) (int64, error) {
	stmt := `DELETE FROM user_sessions WHERE user_id = $1 AND id <> $2`
	res, err := r.DB.ExecContext(ctx, stmt, userID, keepID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
