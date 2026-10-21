package psql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func TestUserSessionRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	repo := NewUserSessionRepository(db)

	newUser := func(t *testing.T) (string, string) {
		t.Helper()
		email := fmt.Sprintf("test-session-%d@example.com", time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		id, _, err := users.Create(t.Context(), "Session User", email, "correct-horse-battery")
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return id, email
	}

	// setLastSeen moves a session's last_seen_at back by ago.
	setLastSeen := func(t *testing.T, id string, ago time.Duration) {
		t.Helper()
		_, err := db.Exec(`UPDATE user_sessions SET last_seen_at = now() - $2 * interval '1 second' WHERE id = $1`,
			id, ago.Seconds())
		if err != nil {
			t.Fatalf("set last_seen_at: %v", err)
		}
	}

	exists := func(t *testing.T, id string) bool {
		t.Helper()
		var ok bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_sessions WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	idleBefore := func() time.Time { return time.Now().Add(-90 * 24 * time.Hour) }

	t.Run("Create and Get", func(t *testing.T) {
		userID, email := newUser(t)
		id, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if id == "" {
			t.Fatal("id is empty")
		}

		got, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != id {
			t.Errorf("ID = %q, want %q", got.ID, id)
		}
		if time.Since(got.LastSeenAt) > time.Minute {
			t.Errorf("LastSeenAt = %v, want about now", got.LastSeenAt)
		}
		u := got.User
		if u.ID != userID || u.Name != "Session User" || u.Email != email {
			t.Errorf("User = %+v, want ID %q, name Session User, email %q", u, userID, email)
		}
		if !u.EmailConfirmedAt.IsZero() {
			t.Errorf("EmailConfirmedAt = %v, want zero", u.EmailConfirmedAt)
		}
		if u.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero")
		}
	})

	t.Run("Touch moves last_seen_at forward", func(t *testing.T) {
		userID, _ := newUser(t)
		id, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		setLastSeen(t, id, 2*24*time.Hour)

		if err := repo.Touch(t.Context(), id); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		got, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if time.Since(got.LastSeenAt) > time.Minute {
			t.Errorf("LastSeenAt = %v, want about now", got.LastSeenAt)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		userID, _ := newUser(t)
		id, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if err := repo.Delete(t.Context(), id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repo.Get(t.Context(), id); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Get after Delete: err = %v, want %v", err, rss.ErrNoRecord)
		}
		if err := repo.Delete(t.Context(), id); err != nil {
			t.Errorf("second Delete: %v, want nil", err)
		}
	})

	t.Run("Create deletes the user's idle sessions", func(t *testing.T) {
		userID, _ := newUser(t)
		otherID, _ := newUser(t)

		idle, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		recent, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		otherIdle, err := repo.Create(t.Context(), otherID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		setLastSeen(t, idle, 91*24*time.Hour)
		setLastSeen(t, recent, 89*24*time.Hour)
		setLastSeen(t, otherIdle, 91*24*time.Hour)

		if _, err := repo.Create(t.Context(), userID, idleBefore()); err != nil {
			t.Fatalf("Create: %v", err)
		}

		if exists(t, idle) {
			t.Error("idle session still exists")
		}
		if !exists(t, recent) {
			t.Error("recent session was deleted")
		}
		if !exists(t, otherIdle) {
			t.Error("another user's idle session was deleted")
		}
	})

	t.Run("DeleteOthers", func(t *testing.T) {
		userID, _ := newUser(t)
		otherUserID, _ := newUser(t)

		create := func(userID string) string {
			t.Helper()
			id, err := repo.Create(t.Context(), userID, idleBefore())
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			return id
		}
		keep, other1, other2 := create(userID), create(userID), create(userID)
		otherUsers := create(otherUserID)

		n, err := repo.DeleteOthers(t.Context(), userID, keep)
		if err != nil {
			t.Fatalf("DeleteOthers: %v", err)
		}
		if n != 2 {
			t.Errorf("deleted = %d, want 2", n)
		}
		if !exists(t, keep) {
			t.Error("kept session was deleted")
		}
		if exists(t, other1) || exists(t, other2) {
			t.Error("another session of the user still exists")
		}
		if !exists(t, otherUsers) {
			t.Error("another user's session was deleted")
		}

		n, err = repo.DeleteOthers(t.Context(), userID, keep)
		if err != nil {
			t.Fatalf("second DeleteOthers: %v", err)
		}
		if n != 0 {
			t.Errorf("second deleted = %d, want 0", n)
		}
	})

	t.Run("deleting the user cascades", func(t *testing.T) {
		userID, _ := newUser(t)
		id, err := repo.Create(t.Context(), userID, idleBefore())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		if exists(t, id) {
			t.Error("session still exists after its user was deleted")
		}
	})

	t.Run("Get unknown ID", func(t *testing.T) {
		_, err := repo.Get(t.Context(), "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}
