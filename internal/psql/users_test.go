package psql

import (
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
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

	id, createdAt, err := repo.Create(t.Context(), "Test User", email, password)
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
		_, _, err := repo.Create(t.Context(), "Other User", strings.ToUpper(email), password)
		if !errors.Is(err, rss.ErrDuplicateEmail) {
			t.Errorf("err = %v; want %v", err, rss.ErrDuplicateEmail)
		}
	})

	t.Run("Stored hash is Argon2id", func(t *testing.T) {
		var stored []byte
		err := db.QueryRow(`SELECT hashed_password FROM users WHERE id = $1`, id).Scan(&stored)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(stored), "$argon2id$") {
			t.Errorf("stored hash = %q; want $argon2id$ prefix", stored)
		}
	})

	t.Run("Authenticate with correct password", func(t *testing.T) {
		got, err := repo.Authenticate(t.Context(), email, password)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got != id {
			t.Errorf("id = %q; want %q", got, id)
		}
	})

	t.Run("Authenticate with wrong password", func(t *testing.T) {
		_, err := repo.Authenticate(t.Context(), email, "wrong-password")
		if !errors.Is(err, rss.ErrWrongPassword) {
			t.Errorf("err = %v; want %v", err, rss.ErrWrongPassword)
		}
	})

	t.Run("Authenticate with unknown email", func(t *testing.T) {
		_, err := repo.Authenticate(t.Context(), "unknown-"+email, password)
		if !errors.Is(err, rss.ErrNoAccount) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoAccount)
		}
	})

	t.Run("UpdateName changes the name", func(t *testing.T) {
		if err := repo.UpdateName(t.Context(), id, "Renamed User"); err != nil {
			t.Fatalf("UpdateName: %v", err)
		}
		var got string
		if err := db.QueryRow(`SELECT name FROM users WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "Renamed User" {
			t.Errorf("name = %q; want %q", got, "Renamed User")
		}
	})

	t.Run("UpdateName with unknown id", func(t *testing.T) {
		err := repo.UpdateName(t.Context(), "00000000-0000-4000-8000-000000000000", "Nobody")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoRecord)
		}
	})
}

func TestUserRepositoryChangePassword(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewUserRepository(db)
	sessions := NewUserSessionRepository(db)

	const oldPassword, newPassword = "correct-horse-battery", "staple-tuna-lamp"

	// newUser creates a user with oldPassword and two sessions, and returns
	// the user's email and the sessions' IDs.
	newUser := func(t *testing.T) (email, userID, keep, other string) {
		t.Helper()
		email = fmt.Sprintf("test-password-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		userID, _, err := repo.Create(t.Context(), "Password User", email, oldPassword)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		idleBefore := time.Now().Add(-90 * 24 * time.Hour)
		if keep, err = sessions.Create(t.Context(), userID, idleBefore); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if other, err = sessions.Create(t.Context(), userID, idleBefore); err != nil {
			t.Fatalf("create session: %v", err)
		}
		return email, userID, keep, other
	}

	t.Run("wrong current password", func(t *testing.T) {
		email, userID, keep, other := newUser(t)

		err := repo.ChangePassword(t.Context(), userID, keep, "wrong-password", newPassword)
		if !errors.Is(err, rss.ErrWrongPassword) {
			t.Fatalf("err = %v; want %v", err, rss.ErrWrongPassword)
		}
		if _, err := repo.Authenticate(t.Context(), email, oldPassword); err != nil {
			t.Errorf("Authenticate with the old password: %v", err)
		}
		if _, err := sessions.Get(t.Context(), other); err != nil {
			t.Errorf("other session: %v; want it kept", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		email, userID, keep, other := newUser(t)

		if err := repo.ChangePassword(t.Context(), userID, keep, oldPassword, newPassword); err != nil {
			t.Fatalf("ChangePassword: %v", err)
		}
		if got, err := repo.Authenticate(t.Context(), email, newPassword); err != nil || got != userID {
			t.Errorf("Authenticate with the new password = %q, %v; want %q, nil", got, err, userID)
		}
		if _, err := repo.Authenticate(t.Context(), email, oldPassword); !errors.Is(err, rss.ErrWrongPassword) {
			t.Errorf("Authenticate with the old password: err = %v; want %v", err, rss.ErrWrongPassword)
		}
		if _, err := sessions.Get(t.Context(), keep); err != nil {
			t.Errorf("kept session: %v", err)
		}
		if _, err := sessions.Get(t.Context(), other); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("other session: err = %v; want %v", err, rss.ErrNoRecord)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		err := repo.ChangePassword(t.Context(), "00000000-0000-4000-8000-000000000000",
			"00000000-0000-4000-8000-000000000001", oldPassword, newPassword)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoRecord)
		}
	})
}
