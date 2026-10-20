package psql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"sync"
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

// dummyHash is verified against when no account has the email, so that login
// takes as long as it does for a wrong password and doesn't reveal which
// emails have accounts.
var dummyHash = sync.OnceValues(func() (string, error) {
	return password.Hash("dummy password for unknown emails")
})

func (r *UserRepository) Authenticate(ctx context.Context, email, pw string) (string, error) {
	var id string
	var hashedPassword []byte

	stmt := `SELECT id, hashed_password FROM users WHERE email = $1`

	err := r.DB.QueryRowContext(ctx, stmt, email).Scan(&id, &hashedPassword)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			h, err := dummyHash()
			if err != nil {
				return "", err
			}
			_, _ = password.Verify(pw, h)
			return "", rss.ErrInvalidCredentials
		default:
			return "", err
		}
	}

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return "", fmt.Errorf("verify password for user %s: %w", id, err)
	}
	if !match {
		return "", rss.ErrInvalidCredentials
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
