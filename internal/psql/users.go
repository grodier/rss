package psql

import (
	"database/sql"
	"errors"
	"time"

	"github.com/grodier/rss/internal/password"
	"github.com/lib/pq"
)

type User struct {
	ID             string
	Name           string
	Email          string
	HashedPassword []byte
	CreatedAt      time.Time
}

type UserRepository struct {
	DB *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{DB: db}
}

func (r *UserRepository) Create(name, email, pw string) (string, time.Time, error) {
	hashedPassword, err := password.Hash(pw)
	if err != nil {
		return "", time.Time{}, err
	}

	stmt := `INSERT INTO users (name, email, hashed_password)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`

	var id string
	var createdAt time.Time
	err = r.DB.QueryRow(stmt, name, email, []byte(hashedPassword)).Scan(&id, &createdAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", time.Time{}, ErrDuplicateEmail
		}
		return "", time.Time{}, err
	}

	return id, createdAt, nil
}

func (r *UserRepository) Authenticate(email, pw string) (string, error) {
	var id string
	var hashedPassword []byte

	stmt := `SELECT id, hashed_password FROM users WHERE email = $1`

	err := r.DB.QueryRow(stmt, email).Scan(&id, &hashedPassword)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return "", ErrInvalidCredentials
		default:
			return "", err
		}
	}

	match, err := password.Verify(pw, string(hashedPassword))
	if err != nil {
		return "", err
	}
	if !match {
		return "", ErrInvalidCredentials
	}

	return id, nil
}

func (r *UserRepository) Exists(id string) (bool, error) {
	stmt := `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`

	var exists bool
	err := r.DB.QueryRow(stmt, id).Scan(&exists)

	return exists, err
}
