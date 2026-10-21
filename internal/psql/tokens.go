package psql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/grodier/rss/internal/rss"
)

// TokenRepository stores the one-time tokens sent in email links
// (user_tokens rows).
type TokenRepository struct {
	DB *sql.DB
}

func NewTokenRepository(db *sql.DB) *TokenRepository {
	return &TokenRepository{DB: db}
}

// Create stores t. It first deletes the user's expired tokens, so the table
// stays bounded without a sweeper.
func (r *TokenRepository) Create(ctx context.Context, t rss.Token) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt := `DELETE FROM user_tokens WHERE user_id = $1 AND expires_at <= now()`
	if _, err := tx.ExecContext(ctx, stmt, t.UserID); err != nil {
		return err
	}

	stmt = `INSERT INTO user_tokens (hash, user_id, purpose, email, expires_at)
		VALUES ($1, $2, $3, $4, $5)`
	if _, err := tx.ExecContext(ctx, stmt, t.Hash, t.UserID, t.Purpose, t.Email, t.ExpiresAt); err != nil {
		return err
	}

	return tx.Commit()
}

// Get returns the token with hash and purpose if it's unexpired and, except
// for change_email (whose address is the new one), its address is still the
// account's. Otherwise rss.ErrNoRecord.
func (r *TokenRepository) Get(ctx context.Context, hash []byte, purpose string) (rss.Token, error) {
	stmt := `SELECT t.hash, t.user_id, t.purpose, t.email, t.expires_at
		FROM user_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.hash = $1 AND t.purpose = $2 AND t.expires_at > now()
			AND (t.purpose = 'change_email' OR t.email = u.email)`

	var t rss.Token
	err := r.DB.QueryRowContext(ctx, stmt, hash, purpose).Scan(&t.Hash, &t.UserID, &t.Purpose, &t.Email, &t.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rss.Token{}, rss.ErrNoRecord
		}
		return rss.Token{}, err
	}
	return t, nil
}
