package pgsql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/grodier/rss/internal/auth"
	"github.com/grodier/rss/internal/domain"
)

type AuthRepository struct {
	db *sql.DB
}

func NewAuthRepository(db *sql.DB) *AuthRepository {
	return &AuthRepository{db: db}
}

func (r *AuthRepository) GetUserByID(ctx context.Context, db auth.DBTX, id uuid.UUID) (domain.User, error) {
	var u domain.User
	err := db.QueryRowContext(ctx,
		`SELECT id, display_name, created_at, deleted_at, purge_after
		FROM users WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &u.DisplayName, &u.CreatedAt, &u.DeletedAt, &u.PurgeAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, auth.ErrUserNotFound
	}
	return u, err
}

func (r *AuthRepository) UpdateUserDisplayName(ctx context.Context, db auth.DBTX, id uuid.UUID, displayName *string) (domain.User, error) {
	var u domain.User
	err := db.QueryRowContext(ctx,
		`UPDATE users SET display_name = $2
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, display_name, created_at, deleted_at, purge_after`, id, displayName,
	).Scan(&u.ID, &u.DisplayName, &u.CreatedAt, &u.DeletedAt, &u.PurgeAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, auth.ErrUserNotFound
	}
	return u, err
}

func (r *AuthRepository) SoftDeleteUser(ctx context.Context, db auth.DBTX, id uuid.UUID) error {
	result, err := db.ExecContext(ctx,
		`UPDATE users SET deleted_at = now(), purge_after = now() + interval '90 days'
		WHERE id = $1 AND deleted_at IS NULL`, id,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrUserNotFound
	}
	return nil
}

func (r *AuthRepository) SoftDeleteAccount(ctx context.Context, db auth.DBTX, id uuid.UUID) error {
	result, err := db.ExecContext(ctx,
		`UPDATE accounts SET deleted_at = now(), purge_after = now() + interval '90 days'
		WHERE id = $1 AND deleted_at IS NULL`, id,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrAccountNotFound
	}
	return nil
}

func (r *AuthRepository) ListMembershipsByUserID(ctx context.Context, db auth.DBTX, userID uuid.UUID) ([]domain.Membership, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT m.user_id, m.account_id, m.role, m.created_at, m.last_used_at
		FROM memberships m
		JOIN accounts a ON a.id = m.account_id AND a.deleted_at IS NULL
		WHERE m.user_id = $1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memberships []domain.Membership
	for rows.Next() {
		var m domain.Membership
		if err := rows.Scan(&m.UserID, &m.AccountID, &m.Role, &m.CreatedAt, &m.LastUsedAt); err != nil {
			return nil, err
		}
		memberships = append(memberships, m)
	}
	return memberships, rows.Err()
}

func (r *AuthRepository) GetPrimaryMembership(ctx context.Context, db auth.DBTX, userID uuid.UUID) (domain.Membership, error) {
	var m domain.Membership
	err := db.QueryRowContext(ctx,
		`SELECT m.user_id, m.account_id, m.role, m.created_at, m.last_used_at
		FROM memberships m
		JOIN accounts a ON a.id = m.account_id AND a.deleted_at IS NULL
		WHERE m.user_id = $1
		ORDER BY m.last_used_at DESC, CASE WHEN m.role = 'owner' THEN 0 ELSE 1 END
		LIMIT 1`, userID,
	).Scan(&m.UserID, &m.AccountID, &m.Role, &m.CreatedAt, &m.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Membership{}, auth.ErrNotAMember
	}
	return m, err
}

func (r *AuthRepository) UpdateMembershipLastUsedAt(ctx context.Context, db auth.DBTX, userID uuid.UUID, accountID uuid.UUID) error {
	result, err := db.ExecContext(ctx,
		`UPDATE memberships SET last_used_at = now()
		WHERE user_id = $1 AND account_id = $2`, userID, accountID,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrNotAMember
	}
	return nil
}
