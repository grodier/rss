package psql

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
)

func TestUserRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewUserRepository(db)

	email := fmt.Sprintf("test-%d@example.com", time.Now().UnixNano())
	const password = "correct-horse-battery"

	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	id, createdAt, err := repo.Create("Test User", email, password)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Create returns id and createdAt", func(t *testing.T) {
		if id == "" {
			t.Error("id is empty")
		}
		if createdAt.IsZero() {
			t.Error("createdAt is zero")
		}
	})

	t.Run("Create with duplicate email in different case", func(t *testing.T) {
		_, _, err := repo.Create("Other User", strings.ToUpper(email), password)
		if !errors.Is(err, ErrDuplicateEmail) {
			t.Errorf("err = %v; want %v", err, ErrDuplicateEmail)
		}
	})

	t.Run("Authenticate with correct password", func(t *testing.T) {
		got, err := repo.Authenticate(email, password)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got != id {
			t.Errorf("id = %q; want %q", got, id)
		}
	})

	t.Run("Authenticate with wrong password", func(t *testing.T) {
		_, err := repo.Authenticate(email, "wrong-password")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("err = %v; want %v", err, ErrInvalidCredentials)
		}
	})

	t.Run("Authenticate with unknown email", func(t *testing.T) {
		_, err := repo.Authenticate("unknown-"+email, password)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("err = %v; want %v", err, ErrInvalidCredentials)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		exists, err := repo.Exists(id)
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Error("Exists = false; want true")
		}
	})
}
