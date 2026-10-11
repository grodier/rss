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
	"strconv"
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

func postForm(t *testing.T, h http.HandlerFunc, target, remoteAddr string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertRateLimitedForm(t *testing.T, rr *httptest.ResponseRecorder, email string) {
	t.Helper()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	if secs, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", rr.Header().Get("Retry-After"))
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Too many attempts. Try again in") {
		t.Errorf("body does not contain the rate-limit message: %s", body)
	}
	wantInput := `name="email" value="` + email + `"`
	if !strings.Contains(body, wantInput) {
		t.Errorf("body does not contain %q: %s", wantInput, body)
	}
}

func TestLoginFormHandlerRateLimited(t *testing.T) {
	calls := 0
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		authenticateFn: func(ctx context.Context, email, password string) (string, error) {
			calls++
			return "", rss.ErrInvalidCredentials
		},
	}})
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP
	form := url.Values{"email": {"user@example.com"}, "password": {"wrong-password"}}

	for i := 1; i <= 20; i++ {
		if rr := postForm(t, h, "/login", "192.0.2.1:1234", form); rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
		}
	}

	rr := postForm(t, h, "/login", "192.0.2.1:1234", form)
	assertRateLimitedForm(t, rr, "user@example.com")
	if calls != 20 {
		t.Errorf("Authenticate called %d times, want 20", calls)
	}

	if rr := postForm(t, h, "/login", "192.0.2.2:1234", form); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("other IP: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}

func TestSignupFormHandlerRateLimited(t *testing.T) {
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		createFn: func(ctx context.Context, name, email, password string) (string, time.Time, error) {
			t.Error("Create must not be called")
			return "", time.Time{}, nil
		},
	}})
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)).ServeHTTP
	// A blank name fails validation, so no attempt reaches the store.
	form := url.Values{"name": {""}, "email": {"user@example.com"}, "password": {"correct-horse-battery"}}

	for i := 1; i <= 10; i++ {
		if rr := postForm(t, h, "/signup", "192.0.2.1:1234", form); rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
		}
	}

	rr := postForm(t, h, "/signup", "192.0.2.1:1234", form)
	assertRateLimitedForm(t, rr, "user@example.com")

	if rr := postForm(t, h, "/signup", "192.0.2.2:1234", form); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("other IP: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}
