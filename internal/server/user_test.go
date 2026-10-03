package server

import (
	"context"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
)

func TestSignupFormHandlerPasswordTooLong(t *testing.T) {
	s := newTestServer(t)

	form := url.Values{
		"name":     {"a"},
		"email":    {"a@b.co"},
		"password": {strings.Repeat("a", 257)},
	}
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.signupFormHandler(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rr.Body.String(), "256 characters") {
		t.Errorf("body does not contain %q", "256 characters")
	}
}

func TestLoginFormHandlerPasswordTooLong(t *testing.T) {
	s := newTestServer(t)

	form := url.Values{
		"email":    {"a@b.co"},
		"password": {strings.Repeat("a", 257)},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.loginFormHandler(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rr.Body.String(), "Password is too long") {
		t.Errorf("body does not contain %q", "Password is too long")
	}
}

func TestSignupFormHandlerSuccessFlash(t *testing.T) {
	db := psqltest.NewDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := NewServer(logger, Config{Port: 4000, Env: "development"},
		Services{UserService: psql.NewUserRepository(db)}, scs.New())
	if err != nil {
		t.Fatal(err)
	}

	email := fmt.Sprintf("signup-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	var flash string
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.signupFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	}))

	form := url.Values{
		"name":     {"Test User"},
		"email":    {email},
		"password": {"correct-horse-battery"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	const want = "Your signup was successful. Please log in."
	if flash != want {
		t.Errorf("flash = %q, want %q", flash, want)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(flash, id) {
		t.Errorf("flash %q contains user ID %s", flash, id)
	}
}

func TestLoginFormHandlerInvalidCredentials(t *testing.T) {
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		authenticateFn: func(ctx context.Context, email, password string) (string, error) {
			return "", rss.ErrInvalidCredentials
		},
	}})

	form := url.Values{"email": {"user@example.com"}, "password": {"wrong-password"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rr.Body.String(), "Email or password is incorrect") {
		t.Errorf("body does not contain credentials error: %s", rr.Body.String())
	}
}
