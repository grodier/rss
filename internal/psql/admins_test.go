package psql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grodier/rss/internal/password"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newAdminCredentials returns credentials with pw and the recovery codes'
// hashes, whose secret is just a marker: the repository stores it as is.
func newAdminCredentials(t *testing.T, pw, secret string, codeHashes ...string) rss.AdminCredentials {
	t.Helper()
	hash, err := password.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	c := rss.AdminCredentials{HashedPassword: []byte(hash), SealedTOTPSecret: []byte(secret)}
	for _, h := range codeHashes {
		c.RecoveryCodeHashes = append(c.RecoveryCodeHashes, []byte(h))
	}
	return c
}

func TestAdminRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewAdminRepository(db)

	email := fmt.Sprintf("admin-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM admins WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup admin: %v", err)
		}
	})

	id, err := repo.Create(t.Context(), email, newAdminCredentials(t, "first password", "secret-1", "code-a", "code-b"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Create refuses a duplicate email", func(t *testing.T) {
		_, err := repo.Create(t.Context(), email, newAdminCredentials(t, "pw", "s"))
		if !errors.Is(err, rss.ErrDuplicateEmail) {
			t.Errorf("Create duplicate error = %v, want rss.ErrDuplicateEmail", err)
		}
	})

	t.Run("Get and GetByEmail", func(t *testing.T) {
		a, err := repo.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if a.ID != id || a.Email != email || string(a.TOTPSecret) != "secret-1" || a.TOTPLastStep != 0 || a.CreatedAt.IsZero() {
			t.Errorf("Get = %+v", a)
		}
		// email is citext.
		b, err := repo.GetByEmail(t.Context(), "ADMIN"+email[len("admin"):])
		if err != nil || b.ID != id {
			t.Errorf("GetByEmail = %+v, %v; want the admin", b, err)
		}
		if _, err := repo.Get(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Get unknown error = %v, want rss.ErrNoRecord", err)
		}
		if _, err := repo.GetByEmail(t.Context(), "nobody-"+email); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByEmail unknown error = %v, want rss.ErrNoRecord", err)
		}
	})

	t.Run("Authenticate", func(t *testing.T) {
		a, err := repo.Authenticate(t.Context(), email, "first password")
		if err != nil || a.ID != id {
			t.Errorf("Authenticate = %+v, %v; want the admin", a, err)
		}
		if _, err := repo.Authenticate(t.Context(), email, "wrong"); !errors.Is(err, rss.ErrWrongPassword) {
			t.Errorf("wrong password error = %v, want rss.ErrWrongPassword", err)
		}
		if _, err := repo.Authenticate(t.Context(), "nobody-"+email, "first password"); !errors.Is(err, rss.ErrNoAccount) {
			t.Errorf("unknown email error = %v, want rss.ErrNoAccount", err)
		}
	})

	t.Run("AcceptTOTPStep accepts each step once, in order", func(t *testing.T) {
		for _, tt := range []struct {
			step int64
			want bool
		}{{100, true}, {100, false}, {99, false}, {101, true}} {
			ok, err := repo.AcceptTOTPStep(t.Context(), id, tt.step)
			if err != nil || ok != tt.want {
				t.Errorf("AcceptTOTPStep(%d) = %v, %v; want %v", tt.step, ok, err, tt.want)
			}
		}
	})

	t.Run("UseRecoveryCode works once", func(t *testing.T) {
		for _, tt := range []struct {
			hash string
			want bool
		}{{"code-a", true}, {"code-a", false}, {"code-z", false}} {
			ok, err := repo.UseRecoveryCode(t.Context(), id, []byte(tt.hash))
			if err != nil || ok != tt.want {
				t.Errorf("UseRecoveryCode(%s) = %v, %v; want %v", tt.hash, ok, err, tt.want)
			}
		}
	})

	t.Run("Reset replaces the credentials", func(t *testing.T) {
		if err := repo.Reset(t.Context(), email, newAdminCredentials(t, "second password", "secret-2", "code-c")); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		a, err := repo.Authenticate(t.Context(), email, "second password")
		if err != nil {
			t.Fatalf("Authenticate with the new password: %v", err)
		}
		if a.ID != id || string(a.TOTPSecret) != "secret-2" || a.TOTPLastStep != 0 {
			t.Errorf("after Reset = %+v; want the same id, the new secret and step 0", a)
		}
		if _, err := repo.Authenticate(t.Context(), email, "first password"); !errors.Is(err, rss.ErrWrongPassword) {
			t.Errorf("old password error = %v, want rss.ErrWrongPassword", err)
		}
		if ok, _ := repo.UseRecoveryCode(t.Context(), id, []byte("code-b")); ok {
			t.Error("an old recovery code still works")
		}
		if ok, _ := repo.UseRecoveryCode(t.Context(), id, []byte("code-c")); !ok {
			t.Error("the new recovery code doesn't work")
		}
		if err := repo.Reset(t.Context(), "nobody-"+email, newAdminCredentials(t, "pw", "s")); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Reset unknown error = %v, want rss.ErrNoRecord", err)
		}
	})
}
