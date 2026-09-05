package psql

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
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

func (r *UserRepository) Create(name, email, password string) (string, time.Time, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", time.Time{}, err
	}

	stmt := `INSERT INTO users (name, email, hashed_password)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`

	var id string
	var createdAt time.Time
	err = r.DB.QueryRow(stmt, name, email, hashedPassword).Scan(&id, &createdAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", time.Time{}, ErrDuplicateEmail
		}
		return "", time.Time{}, err
	}

	return id, createdAt, nil
}

func (r *UserRepository) Authenticate(email, password string) (string, error) {
	return "", nil
}

func (r *UserRepository) Exists(email string) (bool, error) {
	return false, nil
}
