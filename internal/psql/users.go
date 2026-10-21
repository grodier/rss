package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"time"

	"github.com/grodier/rss/internal/password"
	"github.com/lib/pq"
)

type UserRepository struct {
	DB *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{DB: db}
}

func (r *UserRepository) Create(ctx context.Context, name, email, pw string) (string, time.Time, error) {
	hashedPassword, err := password.Hash(pw)
	if err != nil {
		return "", time.Time{}, err
	}

	stmt := `INSERT INTO users (name, email, hashed_password)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`

	var id string
	var createdAt time.Time
	err = r.DB.QueryRowContext(ctx, stmt, name, email, []byte(hashedPassword)).Scan(&id, &createdAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", time.Time{}, rss.ErrDuplicateEmail
		}
		return "", time.Time{}, err
	}

	return id, createdAt, nil
}

func (r *UserRepository) Authenticate(ctx context.Context, email, pw string) (string, error) {
	var id string
	var hashedPassword []byte

	stmt := `SELECT id, hashed_password FROM users WHERE email = $1`

	err := r.DB.QueryRowContext(ctx, stmt, email).Scan(&id, &hashedPassword)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return "", rss.ErrNoAccount
		default:
			return "", err
		}
	}

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return "", fmt.Errorf("verify password for user %s: %w", id, err)
	}
	if !match {
		return "", rss.ErrWrongPassword
	}

	return id, nil
}

// UpdateName sets the name of the user with id. It returns rss.ErrNoRecord if
// there is no such user.
func (r *UserRepository) UpdateName(ctx context.Context, id, name string) error {
	stmt := `UPDATE users SET name = $2 WHERE id = $1`

	res, err := r.DB.ExecContext(ctx, stmt, id, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return rss.ErrNoRecord
	}
	return nil
}

// ChangePassword replaces the password of the user with userID, if current
// is right, and logs out every other device: it deletes the user's sessions
// except keepSessionID. A wrong current password returns
// rss.ErrWrongPassword with nothing changed; an unknown user,
// rss.ErrNoRecord.
func (r *UserRepository) ChangePassword(ctx context.Context, userID, keepSessionID, current, next string) error {
	// Verified before the transaction starts, since Argon2 is slow.
	var hashedPassword []byte
	stmt := `SELECT hashed_password FROM users WHERE id = $1`
	err := r.DB.QueryRowContext(ctx, stmt, userID).Scan(&hashedPassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.ErrNoRecord
		}
		return err
	}

	match, err := password.Verify(current, string(hashedPassword))
	if err != nil {
		return fmt.Errorf("verify password for user %s: %w", userID, err)
	}
	if !match {
		return rss.ErrWrongPassword
	}

	newHash, err := password.Hash(next)
	if err != nil {
		return err
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt = `UPDATE users SET hashed_password = $2 WHERE id = $1`
	if _, err := tx.ExecContext(ctx, stmt, userID, []byte(newHash)); err != nil {
		return err
	}

	stmt = `DELETE FROM user_sessions WHERE user_id = $1 AND id <> $2`
	if _, err := tx.ExecContext(ctx, stmt, userID, keepSessionID); err != nil {
		return err
	}

	return tx.Commit()
}

// ConfirmEmail uses a confirm_email token: it marks the account's email
// confirmed and deletes the user's other confirm_email tokens. ErrNoRecord if
// the token is unknown, expired, or for an address the account no longer has.
func (r *UserRepository) ConfirmEmail(ctx context.Context, hash []byte) (string, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	stmt := `DELETE FROM user_tokens t USING users u
		WHERE t.hash = $1 AND t.purpose = 'confirm_email' AND t.expires_at > now()
			AND u.id = t.user_id AND u.email = t.email
		RETURNING t.user_id`
	var userID string
	if err := tx.QueryRowContext(ctx, stmt, hash).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", rss.ErrNoRecord
		}
		return "", err
	}

	stmt = `UPDATE users SET email_confirmed_at = COALESCE(email_confirmed_at, now()) WHERE id = $1`
	if _, err := tx.ExecContext(ctx, stmt, userID); err != nil {
		return "", err
	}

	stmt = `DELETE FROM user_tokens WHERE user_id = $1 AND purpose = 'confirm_email'`
	if _, err := tx.ExecContext(ctx, stmt, userID); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return userID, nil
}
