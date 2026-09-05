package psql

import (
	"database/sql"
	"time"
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

func (r *UserRepository) Create(name, email, password string) error {
	return nil
}

func (r *UserRepository) Authenticate(email, password string) (string, error) {
	return "", nil
}

func (r *UserRepository) Exists(email string) (bool, error) {
	return false, nil
}
