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

	t.Run("GetByEmail ignores case", func(t *testing.T) {
		u, err := repo.GetByEmail(t.Context(), strings.ToUpper(email))
		if err != nil {
			t.Fatalf("GetByEmail: %v", err)
		}
		if u.ID != id || u.Name != "Test User" || u.Email != email {
			t.Errorf("GetByEmail = %+v; want %s, Test User, %s", u, id, email)
		}
	})

	t.Run("GetByEmail with unknown email", func(t *testing.T) {
		_, err := repo.GetByEmail(t.Context(), "unknown-"+email)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoRecord)
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

	t.Run("UpdateTheme saves each theme", func(t *testing.T) {
		for _, theme := range []string{rss.ThemeDark, rss.ThemeLight, rss.ThemeSystem} {
			if err := repo.UpdateTheme(t.Context(), id, theme); err != nil {
				t.Fatalf("UpdateTheme(%q): %v", theme, err)
			}
			var got string
			if err := db.QueryRow(`SELECT theme FROM users WHERE id = $1`, id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != theme {
				t.Errorf("theme = %q; want %q", got, theme)
			}
		}
	})

	t.Run("UpdateTheme rejects another value", func(t *testing.T) {
		if err := repo.UpdateTheme(t.Context(), id, "sepia"); err == nil {
			t.Error("UpdateTheme(sepia) succeeded; want the CHECK to reject it")
		}
	})

	t.Run("UpdateTheme with unknown id", func(t *testing.T) {
		err := repo.UpdateTheme(t.Context(), "00000000-0000-4000-8000-000000000000", rss.ThemeDark)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoRecord)
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

func TestUserRepositoryCheckPassword(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewUserRepository(db)
	userID, _ := newTokenUser(t, repo)

	tests := []struct {
		name     string
		userID   string
		password string
		want     error
	}{
		{"right password", userID, "correct-horse-battery", nil},
		{"wrong password", userID, "wrong-password", rss.ErrWrongPassword},
		{"unknown user", "00000000-0000-4000-8000-000000000000", "correct-horse-battery", rss.ErrNoRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := repo.CheckPassword(t.Context(), tt.userID, tt.password); !errors.Is(err, tt.want) {
				t.Errorf("CheckPassword error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDeleteUser(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewUserRepository(db)
	sessions := NewUserSessionRepository(db)
	tokens := NewTokenRepository(db)
	subs := NewSubscriptionRepository(db)
	reads := NewReadRepository(db)

	const password = "correct-horse-battery"

	count := func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}

	// newUser creates a user with password who subscribes to the feed of
	// articleID, has read it, and has a session and a token. It returns the
	// user's ID and email.
	newUser := func(t *testing.T, articleID string) (string, string) {
		t.Helper()
		email := fmt.Sprintf("test-delete-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		userID, _, err := repo.Create(t.Context(), "Delete User", email, password)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		var feedID string
		if err := db.QueryRow(`SELECT feed_id FROM articles WHERE id = $1`, articleID).Scan(&feedID); err != nil {
			t.Fatal(err)
		}
		if err := subs.Subscribe(t.Context(), userID, feedID); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := reads.MarkRead(t.Context(), userID, articleID); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
		if _, err := sessions.Create(t.Context(), userID, time.Now().Add(-90*24*time.Hour)); err != nil {
			t.Fatalf("create session: %v", err)
		}
		createToken(t, tokens, userID, rss.TokenConfirmEmail, email, time.Now().Add(time.Hour))
		return userID, email
	}

	// rows counts the user's rows in each table the delete cascades to.
	rows := func(t *testing.T, userID string) map[string]int {
		t.Helper()
		got := map[string]int{}
		for _, table := range []string{"users", "subscriptions", "reads", "user_sessions", "user_tokens"} {
			col := "user_id"
			if table == "users" {
				col = "id"
			}
			got[table] = count(t, `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, userID)
		}
		return got
	}

	t.Run("wrong password", func(t *testing.T) {
		articleID := newTestArticles(t, db, 1)[0]
		userID, _ := newUser(t, articleID)

		_, err := repo.Delete(t.Context(), userID, "wrong-password")
		if !errors.Is(err, rss.ErrWrongPassword) {
			t.Fatalf("err = %v; want %v", err, rss.ErrWrongPassword)
		}
		for table, n := range rows(t, userID) {
			if n != 1 {
				t.Errorf("%s: %d rows; want 1", table, n)
			}
		}
	})

	t.Run("right password", func(t *testing.T) {
		articleID := newTestArticles(t, db, 1)[0]
		userID, email := newUser(t, articleID)
		otherID, _ := newUser(t, articleID)

		got, err := repo.Delete(t.Context(), userID, password)
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got.ID != userID || got.Name != "Delete User" || got.Email != email || got.CreatedAt.IsZero() {
			t.Errorf("Delete returned %+v; want the user as they were", got)
		}
		for table, n := range rows(t, userID) {
			if n != 0 {
				t.Errorf("%s: %d rows left; want 0", table, n)
			}
		}
		if n := count(t, `SELECT count(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.id = $1`, articleID); n != 1 {
			t.Errorf("the article and its feed: %d rows; want 1", n)
		}
		for table, n := range rows(t, otherID) {
			if n != 1 {
				t.Errorf("other user's %s: %d rows; want 1", table, n)
			}
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		_, err := repo.Delete(t.Context(), "00000000-0000-4000-8000-000000000000", password)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v; want %v", err, rss.ErrNoRecord)
		}
	})
}
