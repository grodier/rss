package psql

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// newTokenUser creates a uniquely named user, deleted (with its tokens) when
// the test ends, and returns its ID and email.
func newTokenUser(t *testing.T, users *UserRepository) (string, string) {
	t.Helper()
	email := fmt.Sprintf("test-token-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := users.DB.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	id, _, err := users.Create(t.Context(), "Token User", email, "correct-horse-battery")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id, email
}

// createToken stores a new token for userID and returns its hash.
func createToken(t *testing.T, repo *TokenRepository, userID, purpose, email string, expiresAt time.Time) []byte {
	t.Helper()
	_, hash, err := token.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(t.Context(), rss.Token{Hash: hash, UserID: userID, Purpose: purpose, Email: email, ExpiresAt: expiresAt}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return hash
}

func tokenExists(t *testing.T, repo *TokenRepository, hash []byte) bool {
	t.Helper()
	var ok bool
	if err := repo.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_tokens WHERE hash = $1)`, hash).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestTokenRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	repo := NewTokenRepository(db)
	week := time.Now().Add(7 * 24 * time.Hour)

	t.Run("Get returns a valid token", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		hash := createToken(t, repo, userID, rss.TokenConfirmEmail, email, week)

		got, err := repo.Get(t.Context(), hash, rss.TokenConfirmEmail)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !bytes.Equal(got.Hash, hash) || got.UserID != userID || got.Purpose != rss.TokenConfirmEmail || got.Email != email {
			t.Errorf("Get = %+v, want hash %x, user %s, purpose %s, email %s", got, hash, userID, rss.TokenConfirmEmail, email)
		}
		if !got.ExpiresAt.Round(time.Second).Equal(week.Round(time.Second)) {
			t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, week)
		}
	})

	t.Run("not valid", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		expired := createToken(t, repo, userID, rss.TokenConfirmEmail, email, time.Now().Add(-time.Minute))
		valid := createToken(t, repo, userID, rss.TokenConfirmEmail, email, week)
		otherAddress := createToken(t, repo, userID, rss.TokenResetPassword, "old-"+email, week)

		tests := []struct {
			name    string
			hash    []byte
			purpose string
		}{
			{"unknown", token.Hash("unknown"), rss.TokenConfirmEmail},
			{"expired", expired, rss.TokenConfirmEmail},
			{"wrong purpose", valid, rss.TokenResetPassword},
			{"the account's email changed since", otherAddress, rss.TokenResetPassword},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := repo.Get(t.Context(), tt.hash, tt.purpose); !errors.Is(err, rss.ErrNoRecord) {
					t.Errorf("Get error = %v, want ErrNoRecord", err)
				}
			})
		}
	})

	t.Run("change_email ignores the address check", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		hash := createToken(t, repo, userID, rss.TokenChangeEmail, "new-"+email, week)

		got, err := repo.Get(t.Context(), hash, rss.TokenChangeEmail)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Email != "new-"+email {
			t.Errorf("Email = %q, want %q", got.Email, "new-"+email)
		}
	})

	t.Run("Create deletes the user's expired tokens and nothing else", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		otherID, otherEmail := newTokenUser(t, users)
		expired := createToken(t, repo, userID, rss.TokenResetPassword, email, time.Now().Add(-time.Minute))
		valid := createToken(t, repo, userID, rss.TokenConfirmEmail, email, week)
		othersExpired := createToken(t, repo, otherID, rss.TokenConfirmEmail, otherEmail, time.Now().Add(-time.Minute))

		createToken(t, repo, userID, rss.TokenConfirmEmail, email, week)

		if tokenExists(t, repo, expired) {
			t.Error("the user's expired token is still there")
		}
		if !tokenExists(t, repo, valid) {
			t.Error("the user's unexpired token was deleted")
		}
		if !tokenExists(t, repo, othersExpired) {
			t.Error("another user's expired token was deleted")
		}
	})
}

func TestConfirmEmail(t *testing.T) {
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	tokens := NewTokenRepository(db)
	week := time.Now().Add(7 * 24 * time.Hour)

	confirmedAt := func(t *testing.T, userID string) time.Time {
		t.Helper()
		var at *time.Time
		if err := db.QueryRow(`SELECT email_confirmed_at FROM users WHERE id = $1`, userID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if at == nil {
			return time.Time{}
		}
		return *at
	}

	t.Run("confirms the email once", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		hash := createToken(t, tokens, userID, rss.TokenConfirmEmail, email, week)
		other := createToken(t, tokens, userID, rss.TokenConfirmEmail, email, week)
		reset := createToken(t, tokens, userID, rss.TokenResetPassword, email, week)

		got, err := users.ConfirmEmail(t.Context(), hash)
		if err != nil {
			t.Fatalf("ConfirmEmail: %v", err)
		}
		if got != userID {
			t.Errorf("ConfirmEmail = %q, want %q", got, userID)
		}
		if confirmedAt(t, userID).IsZero() {
			t.Error("email_confirmed_at is NULL")
		}

		if _, err := users.ConfirmEmail(t.Context(), hash); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("ConfirmEmail again: error = %v, want ErrNoRecord", err)
		}
		if tokenExists(t, tokens, other) {
			t.Error("the user's other confirm token is still there")
		}
		if !tokenExists(t, tokens, reset) {
			t.Error("the user's reset token was deleted")
		}
	})

	t.Run("keeps the original confirmation time", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		if _, err := db.Exec(`UPDATE users SET email_confirmed_at = '2026-01-02T03:04:05Z' WHERE id = $1`, userID); err != nil {
			t.Fatal(err)
		}
		hash := createToken(t, tokens, userID, rss.TokenConfirmEmail, email, week)

		if _, err := users.ConfirmEmail(t.Context(), hash); err != nil {
			t.Fatalf("ConfirmEmail: %v", err)
		}
		if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !confirmedAt(t, userID).Equal(want) {
			t.Errorf("email_confirmed_at = %v, want %v", confirmedAt(t, userID), want)
		}
	})

	t.Run("not valid", func(t *testing.T) {
		userID, email := newTokenUser(t, users)
		expired := createToken(t, tokens, userID, rss.TokenConfirmEmail, email, time.Now().Add(-time.Minute))
		otherAddress := createToken(t, tokens, userID, rss.TokenConfirmEmail, "old-"+email, week)
		reset := createToken(t, tokens, userID, rss.TokenResetPassword, email, week)

		for name, hash := range map[string][]byte{
			"unknown":                           token.Hash("unknown"),
			"expired":                           expired,
			"the account's email changed since": otherAddress,
			"wrong purpose":                     reset,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := users.ConfirmEmail(t.Context(), hash); !errors.Is(err, rss.ErrNoRecord) {
					t.Errorf("ConfirmEmail error = %v, want ErrNoRecord", err)
				}
			})
		}
		if !confirmedAt(t, userID).IsZero() {
			t.Error("email_confirmed_at was set")
		}
	})
}
