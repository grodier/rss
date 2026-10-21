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

// GetByEmail returns the user with email, ignoring case. rss.ErrNoRecord if
// there is none.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (rss.User, error) {
	stmt := `SELECT id, name, email, email_confirmed_at, created_at FROM users WHERE email = $1`

	var u rss.User
	var confirmedAt sql.NullTime
	err := r.DB.QueryRowContext(ctx, stmt, email).Scan(&u.ID, &u.Name, &u.Email, &confirmedAt, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.User{}, rss.ErrNoRecord
		}
		return rss.User{}, err
	}
	u.EmailConfirmedAt = confirmedAt.Time
	return u, nil
}

// ResetPassword uses a reset_password token: it sets the new password,
// confirms the email, and deletes the user's reset tokens and every session.
// ErrNoRecord if the token is unknown, expired, or for an address the
// account no longer has.
func (r *UserRepository) ResetPassword(ctx context.Context, hash []byte, newPassword string) (rss.User, error) {
	// Hashed before the transaction starts, since Argon2 is slow.
	newHash, err := password.Hash(newPassword)
	if err != nil {
		return rss.User{}, err
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.User{}, err
	}
	defer tx.Rollback()

	stmt := `DELETE FROM user_tokens t USING users u
		WHERE t.hash = $1 AND t.purpose = 'reset_password' AND t.expires_at > now()
			AND u.id = t.user_id AND u.email = t.email
		RETURNING u.id, u.name, u.email, u.created_at`
	var u rss.User
	if err := tx.QueryRowContext(ctx, stmt, hash).Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.User{}, rss.ErrNoRecord
		}
		return rss.User{}, err
	}

	stmt = `UPDATE users
		SET hashed_password = $2, email_confirmed_at = COALESCE(email_confirmed_at, now())
		WHERE id = $1
		RETURNING email_confirmed_at`
	if err := tx.QueryRowContext(ctx, stmt, u.ID, []byte(newHash)).Scan(&u.EmailConfirmedAt); err != nil {
		return rss.User{}, err
	}

	stmt = `DELETE FROM user_tokens WHERE user_id = $1 AND purpose = 'reset_password'`
	if _, err := tx.ExecContext(ctx, stmt, u.ID); err != nil {
		return rss.User{}, err
	}

	stmt = `DELETE FROM user_sessions WHERE user_id = $1`
	if _, err := tx.ExecContext(ctx, stmt, u.ID); err != nil {
		return rss.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return rss.User{}, err
	}
	return u, nil
}

// Delete verifies password and deletes the user, which cascades to their
// subscriptions, reads, user_sessions and user_tokens. It returns the user as
// they were, for the confirmation email. ErrWrongPassword if the password
// doesn't match; ErrNoRecord if there is no such user.
func (r *UserRepository) Delete(ctx context.Context, userID, pw string) (rss.User, error) {
	// Verified before deleting, outside any transaction, since Argon2 is slow.
	var u rss.User
	var confirmedAt sql.NullTime
	var hashedPassword []byte
	stmt := `SELECT id, name, email, email_confirmed_at, created_at, hashed_password
		FROM users WHERE id = $1`
	err := r.DB.QueryRowContext(ctx, stmt, userID).Scan(&u.ID, &u.Name, &u.Email, &confirmedAt, &u.CreatedAt, &hashedPassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.User{}, rss.ErrNoRecord
		}
		return rss.User{}, err
	}
	u.EmailConfirmedAt = confirmedAt.Time

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return rss.User{}, fmt.Errorf("verify password for user %s: %w", userID, err)
	}
	if !match {
		return rss.User{}, rss.ErrWrongPassword
	}

	res, err := r.DB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return rss.User{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return rss.User{}, err
	}
	if n == 0 {
		return rss.User{}, rss.ErrNoRecord
	}
	return u, nil
}

// CheckPassword returns nil if password is the user's. ErrWrongPassword if it
// doesn't match; ErrNoRecord if there is no such user.
func (r *UserRepository) CheckPassword(ctx context.Context, userID, pw string) error {
	var hashedPassword []byte
	stmt := `SELECT hashed_password FROM users WHERE id = $1`
	err := r.DB.QueryRowContext(ctx, stmt, userID).Scan(&hashedPassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.ErrNoRecord
		}
		return err
	}

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return fmt.Errorf("verify password for user %s: %w", userID, err)
	}
	if !match {
		return rss.ErrWrongPassword
	}
	return nil
}

// ChangeEmail uses a change_email token: it sets the account's email to the
// token's address, marks it confirmed, and deletes the user's other
// change_email tokens. It returns the user as they were before. ErrNoRecord
// if the token is unknown or expired; ErrDuplicateEmail, with old and
// newEmail set and nothing changed, if another account took the address
// meanwhile.
func (r *UserRepository) ChangeEmail(ctx context.Context, hash []byte) (old rss.User, newEmail string, err error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return rss.User{}, "", err
	}
	defer tx.Rollback()

	stmt := `DELETE FROM user_tokens
		WHERE hash = $1 AND purpose = 'change_email' AND expires_at > now()
		RETURNING user_id, email`
	var userID string
	if err := tx.QueryRowContext(ctx, stmt, hash).Scan(&userID, &newEmail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.User{}, "", rss.ErrNoRecord
		}
		return rss.User{}, "", err
	}

	stmt = `SELECT id, name, email, email_confirmed_at, created_at FROM users WHERE id = $1 FOR UPDATE`
	var confirmedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, stmt, userID).Scan(&old.ID, &old.Name, &old.Email, &confirmedAt, &old.CreatedAt); err != nil {
		return rss.User{}, "", err
	}
	old.EmailConfirmedAt = confirmedAt.Time

	stmt = `UPDATE users SET email = $2, email_confirmed_at = now() WHERE id = $1`
	if _, err := tx.ExecContext(ctx, stmt, userID, newEmail); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return old, newEmail, rss.ErrDuplicateEmail
		}
		return rss.User{}, "", err
	}

	stmt = `DELETE FROM user_tokens WHERE user_id = $1 AND purpose = 'change_email'`
	if _, err := tx.ExecContext(ctx, stmt, userID); err != nil {
		return rss.User{}, "", err
	}

	if err := tx.Commit(); err != nil {
		return rss.User{}, "", err
	}
	return old, newEmail, nil
}
