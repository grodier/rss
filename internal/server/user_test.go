package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/psql/psqltest"
)

// testInviteCode is the invite code signup tests run the server with.
const testInviteCode = "test-invite-code-0123"

// newSignupTestServer is newTestServerWith with signup open to testInviteCode.
func newSignupTestServer(t *testing.T, svc Services) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development", InviteCode: testInviteCode}, svc)
}

func TestSignupHandlerInvitePrefill(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"?invite=abc", `name="invite" value="abc"`},
		{"?invite=%3Cscript%3E", `name="invite" value="&lt;script&gt;"`},
		{"", `name="invite" value=""`},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			s := newSignupTestServer(t, Services{})
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signup"+tt.query, nil))

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("body does not contain %q", tt.want)
			}
			if strings.Contains(body, "<script>") {
				t.Error("body contains an unescaped <script>")
			}
		})
	}
}

func TestSignupFormHandlerInviteCode(t *testing.T) {
	tests := []struct {
		name       string
		invite     string
		wantStatus int
		wantError  string
	}{
		{"right code", testInviteCode, http.StatusSeeOther, ""},
		{"wrong code", "wrong-invite-code-0123", http.StatusUnprocessableEntity, "This invite code isn&#39;t valid"},
		{"blank code", "", http.StatusUnprocessableEntity, "Invite code is required"},
		{"prefix of the code", testInviteCode[:len(testInviteCode)-1], http.StatusUnprocessableEntity, "This invite code isn&#39;t valid"},
		{"code plus one character", testInviteCode + "x", http.StatusUnprocessableEntity, "This invite code isn&#39;t valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
				createFn: func(context.Context, string, string, string) (string, time.Time, error) {
					calls++
					return "id", time.Now(), nil
				},
			}})
			h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)).ServeHTTP
			form := url.Values{
				"invite":   {tt.invite},
				"name":     {"Test User"},
				"email":    {"user@example.com"},
				"password": {"correct-horse-battery"},
			}

			rr := postForm(t, h, "/signup", "192.0.2.1:1234", form)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusSeeOther {
				if loc := rr.Header().Get("Location"); loc != "/login" {
					t.Errorf("Location = %q, want /login", loc)
				}
				if calls != 1 {
					t.Errorf("Create called %d times, want 1", calls)
				}
				return
			}
			if !strings.Contains(rr.Body.String(), tt.wantError) {
				t.Errorf("body does not contain %q", tt.wantError)
			}
			if calls != 0 {
				t.Errorf("Create called %d times, want 0", calls)
			}
		})
	}
}

func TestSignupFormHandlerPasswordTooLong(t *testing.T) {
	s := newSignupTestServer(t, Services{})

	form := url.Values{
		"invite":   {testInviteCode},
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
	s, err := NewServer(logger, Config{Port: 4000, Env: "development", InviteCode: testInviteCode},
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
		"invite":   {testInviteCode},
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

	// A different email each time, so the per-email failure limit doesn't trip.
	for i := 1; i <= 20; i++ {
		form := url.Values{"email": {fmt.Sprintf("user%d@example.com", i)}, "password": {"wrong-password"}}
		if rr := postForm(t, h, "/login", "192.0.2.1:1234", form); rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
		}
	}

	form := url.Values{"email": {"user@example.com"}, "password": {"wrong-password"}}
	rr := postForm(t, h, "/login", "192.0.2.1:1234", form)
	assertRateLimitedForm(t, rr, "user@example.com")
	if calls != 20 {
		t.Errorf("Authenticate called %d times, want 20", calls)
	}

	if rr := postForm(t, h, "/login", "192.0.2.2:1234", form); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("other IP: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}

func TestLoginLimitPerForwardedClient(t *testing.T) {
	s := newTestServerConfig(t,
		Config{Port: 4000, Env: "development", ProxyHops: 1},
		Services{UserService: &fakeUserStore{
			authenticateFn: func(ctx context.Context, email, password string) (string, error) {
				return "", rss.ErrInvalidCredentials
			},
		}})
	h := s.router()

	post := func(xff string, i int) *httptest.ResponseRecorder {
		form := url.Values{"email": {fmt.Sprintf("user%d@example.com", i)}, "password": {"wrong-password"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", xff)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	for i := 1; i <= 20; i++ {
		post("1.2.3.4", i)
	}
	if rr := post("1.2.3.4", 21); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("1.2.3.4: status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	if rr := post("1.2.3.5", 22); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("1.2.3.5: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}

func TestLoginFormHandlerFailureLimit(t *testing.T) {
	const (
		email    = "user@example.com"
		password = "correct-horse-battery"
	)
	// newServer returns a login handler whose fake store accepts only email
	// with password, and a count of the calls to Authenticate.
	newServer := func(t *testing.T) (http.HandlerFunc, *int) {
		calls := 0
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			authenticateFn: func(ctx context.Context, e, p string) (string, error) {
				calls++
				switch {
				case p == "db-down":
					return "", errors.New("db down")
				case strings.EqualFold(e, email) && p == password:
					return "user-1", nil
				}
				return "", rss.ErrInvalidCredentials
			},
		}})
		return s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP, &calls
	}
	// Each request comes from a new IP, so the per-IP limit doesn't trip.
	ip := 0
	post := func(t *testing.T, h http.HandlerFunc, e, p string) *httptest.ResponseRecorder {
		t.Helper()
		ip++
		form := url.Values{"email": {e}, "password": {p}}
		return postForm(t, h, "/login", fmt.Sprintf("10.0.%d.%d:1234", ip/256, ip%256), form)
	}
	fail10 := func(t *testing.T, h http.HandlerFunc, e string) {
		t.Helper()
		for i := 1; i <= 10; i++ {
			if rr := post(t, h, e, "wrong-password"); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("failure %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
			}
		}
	}

	t.Run("limits after 10 failures", func(t *testing.T) {
		h, calls := newServer(t)
		fail10(t, h, email)

		rr := post(t, h, email, password)
		assertRateLimitedForm(t, rr, email)
		if *calls != 10 {
			t.Errorf("Authenticate called %d times, want 10", *calls)
		}

		rr = post(t, h, "USER@example.com", password)
		assertRateLimitedForm(t, rr, "USER@example.com")

		if rr := post(t, h, "other@example.com", "wrong-password"); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("other email: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
	})

	t.Run("successes don't count", func(t *testing.T) {
		h, _ := newServer(t)
		for i := 1; i <= 15; i++ {
			rr := post(t, h, email, password)
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("login %d: status = %d, want %d", i, rr.Code, http.StatusSeeOther)
			}
		}
	})

	t.Run("server errors don't count", func(t *testing.T) {
		h, _ := newServer(t)
		for i := 1; i <= 15; i++ {
			rr := post(t, h, email, "db-down")
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("login %d: status = %d, want %d", i, rr.Code, http.StatusInternalServerError)
			}
		}
		if rr := post(t, h, email, "wrong-password"); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("wrong password: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
	})

	t.Run("email with no account", func(t *testing.T) {
		h, calls := newServer(t)
		fail10(t, h, "nobody@example.com")

		rr := post(t, h, "nobody@example.com", "wrong-password")
		assertRateLimitedForm(t, rr, "nobody@example.com")
		if *calls != 10 {
			t.Errorf("Authenticate called %d times, want 10", *calls)
		}
	})
}

func TestLoginFormHandlerFailureLimitConcurrent(t *testing.T) {
	const email = "user@example.com"
	var calls atomic.Int32
	release := make(chan struct{})
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		authenticateFn: func(ctx context.Context, e, p string) (string, error) {
			calls.Add(1)
			<-release
			return "", rss.ErrInvalidCredentials
		},
	}})
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP

	// Each request comes from its own IP, so only the per-email limit applies.
	codes := make(chan int)
	form := url.Values{"email": {email}, "password": {"wrong-password"}}
	for i := range 20 {
		go func() {
			codes <- postForm(t, h, "/login", fmt.Sprintf("10.0.0.%d:1234", i+1), form).Code
		}()
	}

	// Requests over the limit return without reaching Authenticate.
	timeout := time.After(5 * time.Second)
	for i := 1; i <= 10; i++ {
		select {
		case code := <-codes:
			if code != http.StatusTooManyRequests {
				t.Errorf("early response %d: status = %d, want %d", i, code, http.StatusTooManyRequests)
			}
		case <-timeout:
			close(release)
			t.Fatalf("timed out waiting for 429s: got %d of 10", i-1)
		}
	}

	close(release)
	for i := 1; i <= 10; i++ {
		if code := <-codes; code != http.StatusUnprocessableEntity {
			t.Errorf("released response %d: status = %d, want %d", i, code, http.StatusUnprocessableEntity)
		}
	}
	if n := calls.Load(); n != 10 {
		t.Errorf("Authenticate called %d times, want 10", n)
	}
}

func TestSignupFormHandlerRateLimited(t *testing.T) {
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(ctx context.Context, name, email, password string) (string, time.Time, error) {
			t.Error("Create must not be called")
			return "", time.Time{}, nil
		},
	}})
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)).ServeHTTP
	// A wrong invite code fails validation, so no attempt reaches the store,
	// and the limit counts guesses at the code.
	form := url.Values{"invite": {"wrong-invite-code-0123"}, "name": {"Test User"}, "email": {"user@example.com"}, "password": {"correct-horse-battery"}}

	for i := 1; i <= 10; i++ {
		if rr := postForm(t, h, "/signup", "192.0.2.1:1234", form); rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
		}
	}

	rr := postForm(t, h, "/signup", "192.0.2.1:1234", form)
	assertRateLimitedForm(t, rr, "user@example.com")
	if strings.Contains(rr.Body.String(), "invite code isn") {
		t.Error("the rate-limited response checked the invite code")
	}

	if rr := postForm(t, h, "/signup", "192.0.2.2:1234", form); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("other IP: status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}

func TestSignupFormHandlerDuplicateEmail(t *testing.T) {
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(context.Context, string, string, string) (string, time.Time, error) {
			return "", time.Time{}, rss.ErrDuplicateEmail
		},
	}})

	var flash string
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.signupFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	}))

	rr := postSignup(t, h)

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
	if strings.Contains(rr.Body.String(), "already in use") {
		t.Error("body reveals that the email is in use")
	}
}

func TestSignupFormHandlerCreateError(t *testing.T) {
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(context.Context, string, string, string) (string, time.Time, error) {
			return "", time.Time{}, io.ErrUnexpectedEOF
		},
	}})

	rr := postSignup(t, s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func postSignup(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"invite":   {testInviteCode},
		"name":     {"Test User"},
		"email":    {"dup@example.com"},
		"password": {"correct-horse-battery"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
