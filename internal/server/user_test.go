package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/grodier/rss/internal/rss"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testInviteCode is the invite code signup tests run the server with.
const testInviteCode = "test-invite-code-0123"

// newSignupTestServer is newTestServerWith with signup open to testInviteCode.
// Signup sends a confirmation email, so a nil TokenService or EmailQueue gets
// a fake.
func newSignupTestServer(t *testing.T, svc Services) *Server {
	t.Helper()
	if svc.TokenService == nil {
		svc.TokenService = &fakeTokenStore{}
	}
	if svc.EmailQueue == nil {
		svc.EmailQueue = &fakeEmailQueue{}
	}
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
					return "user-1", time.Now(), nil
				},
			}, SessionService: &fakeSessionStore{
				createFn: func(context.Context, string, time.Time) (string, error) {
					return testSessionID, nil
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
				if loc := rr.Header().Get("Location"); loc != "/" {
					t.Errorf("Location = %q, want /", loc)
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

func TestSignupFormHandlerTrimsName(t *testing.T) {
	var gotName string
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(ctx context.Context, name, email, password string) (string, time.Time, error) {
			gotName = name
			return "user-1", time.Now(), nil
		},
	}, SessionService: &fakeSessionStore{
		createFn: func(context.Context, string, time.Time) (string, error) {
			return testSessionID, nil
		},
	}})
	h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)).ServeHTTP
	form := url.Values{
		"invite":   {testInviteCode},
		"name":     {"  Ann  "},
		"email":    {"user@example.com"},
		"password": {"correct-horse-battery"},
	}

	rr := postForm(t, h, "/signup", "192.0.2.1:1234", form)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if gotName != "Ann" {
		t.Errorf("Create got name %q, want %q", gotName, "Ann")
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

// testSessionID is the user_sessions ID fake session stores return.
const testSessionID = "8f14e45f-ceea-4e7a-9b1d-3c2f0a1b2c3d"

func TestSignupFormHandlerLogsIn(t *testing.T) {
	var calls []string
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(context.Context, string, string, string) (string, time.Time, error) {
			calls = append(calls, "user Create")
			return "user-1", time.Now(), nil
		},
	}, SessionService: &fakeSessionStore{
		createFn: func(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
			calls = append(calls, "session Create "+userID)
			return testSessionID, nil
		},
	}})

	var sessionID string
	rr := postSignup(t, s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.signupFormHandler(w, r)
		sessionID = s.sessionManager.GetString(r.Context(), "sessionID")
	})))

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	if want := []string{"user Create", "session Create user-1"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if sessionID != testSessionID {
		t.Errorf("sessionID = %q, want %q", sessionID, testSessionID)
	}
}

func TestLoginFormHandlerErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantError string
	}{
		{"no account", rss.ErrNoAccount, `<p class="error" id="email-error">There&#39;s no account with this email.</p>`},
		{"wrong password", rss.ErrWrongPassword, `<p class="error" id="password-error">That password isn&#39;t right.</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var creates int
			s := newTestServerWith(t, Services{UserService: &fakeUserStore{
				authenticateFn: func(ctx context.Context, email, password string) (string, error) {
					return "", tt.err
				},
			}, SessionService: &fakeSessionStore{
				createFn: func(context.Context, string, time.Time) (string, error) {
					creates++
					return testSessionID, nil
				},
			}})

			form := url.Values{"email": {"user@example.com"}, "password": {"wrong-password"}}
			rr := postForm(t, s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP, "/login", "192.0.2.1:1234", form)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			body := rr.Body.String()
			if !strings.Contains(body, tt.wantError) {
				t.Errorf("body does not contain %q: %s", tt.wantError, body)
			}
			if want := `name="email" value="user@example.com"`; !strings.Contains(body, want) {
				t.Errorf("body does not contain %q", want)
			}
			if creates != 0 {
				t.Errorf("session Create called %d times, want 0", creates)
			}
		})
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
			return "", rss.ErrWrongPassword
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
	assertBodyContains(t, rr.Body.String(), `Forgot your password? <a href="/password/forgot?email=user%40example.com">Reset it</a>.`)
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
				return "", rss.ErrWrongPassword
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
				case !strings.EqualFold(e, email):
					return "", rss.ErrNoAccount
				case p == password:
					return "user-1", nil
				}
				return "", rss.ErrWrongPassword
			},
		}, SessionService: &fakeSessionStore{
			createFn: func(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
				return "session-1", nil
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

	t.Run("emails with no account don't count", func(t *testing.T) {
		h, calls := newServer(t)
		for i := 1; i <= 15; i++ {
			rr := post(t, h, "nobody@example.com", "wrong-password")
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d: status = %d, want %d", i, rr.Code, http.StatusUnprocessableEntity)
			}
		}
		if *calls != 15 {
			t.Errorf("Authenticate called %d times, want 15", *calls)
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
			return "", rss.ErrWrongPassword
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
	var creates int
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(context.Context, string, string, string) (string, time.Time, error) {
			return "", time.Time{}, rss.ErrDuplicateEmail
		},
	}, SessionService: &fakeSessionStore{
		createFn: func(context.Context, string, time.Time) (string, error) {
			creates++
			return testSessionID, nil
		},
	}})

	rr := postSignup(t, s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<p class="error" id="email-error">There&#39;s already an account with this email.</p>`,
		`<a href="/login">Log in instead</a>, or <a href="/password/forgot?email=dup%40example.com">reset your password</a>.`,
		`name="name" value="Test User"`,
		`name="email" value="dup@example.com"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "correct-horse-battery") {
		t.Error("body echoes the password")
	}
	if creates != 0 {
		t.Errorf("session Create called %d times, want 0", creates)
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

func TestLoginFormHandlerSuccess(t *testing.T) {
	var creates int
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		authenticateFn: func(ctx context.Context, email, password string) (string, error) {
			return "user-id", nil
		},
	}, SessionService: &fakeSessionStore{
		createFn: func(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
			creates++
			if userID != "user-id" {
				t.Errorf("Create userID = %q, want user-id", userID)
			}
			if d := time.Since(idleBefore) - 90*24*time.Hour; d < 0 || d > time.Minute {
				t.Errorf("Create idleBefore = %v, want about 90 days ago", idleBefore)
			}
			return "8f14e45f-ceea-4e7a-9b1d-3c2f0a1b2c3d", nil
		},
	}})

	form := url.Values{"email": {"user@example.com"}, "password": {"correct-password"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}
	if creates != 1 {
		t.Errorf("Create called %d times, want 1", creates)
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Error("no session cookie in the response")
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	sess := rss.Session{ID: "8f14e45f-ceea-4e7a-9b1d-3c2f0a1b2c3d", User: rss.User{ID: "user-1"}, LastSeenAt: time.Now()}

	tests := []struct {
		name       string
		req        *http.Request
		wantDelete []string
	}{
		{"logged in", requestWithSession(sess), []string{sess.ID}},
		{"anonymous", httptest.NewRequest(http.MethodPost, "/logout", nil), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deleted []string
			s := newTestServerWith(t, Services{SessionService: &fakeSessionStore{
				deleteFn: func(ctx context.Context, id string) error {
					deleted = append(deleted, id)
					return nil
				},
			}})

			req := tt.req
			req.Method = http.MethodPost
			rr := httptest.NewRecorder()
			s.sessionManager.LoadAndSave(http.HandlerFunc(s.logoutHandler)).ServeHTTP(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
			}
			if loc := rr.Header().Get("Location"); loc != "/login" {
				t.Errorf("Location = %q, want /login", loc)
			}
			if !slices.Equal(deleted, tt.wantDelete) {
				t.Errorf("Delete called with %q, want %q", deleted, tt.wantDelete)
			}
		})
	}
}

func TestReturnTo(t *testing.T) {
	const feedPath = "/feeds/" + testFeedID
	s := newSignupTestServer(t, Services{UserService: &fakeUserStore{
		createFn: func(context.Context, string, string, string) (string, time.Time, error) {
			return "user-1", time.Now(), nil
		},
		authenticateFn: func(context.Context, string, string) (string, error) {
			return "user-1", nil
		},
	}, SessionService: &fakeSessionStore{
		createFn: func(context.Context, string, time.Time) (string, error) {
			return testSessionID, nil
		},
		getFn: func(context.Context, string) (rss.Session, error) {
			return rss.Session{ID: testSessionID, User: rss.User{ID: "user-1"}, LastSeenAt: time.Now()}, nil
		},
	}})
	ts := httptest.NewServer(s.router())
	t.Cleanup(ts.Close)

	// newClient returns a client with its own cookie jar that doesn't follow
	// redirects.
	newClient := func(t *testing.T) *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	// do sends a request and returns its Location header, checking for a 303.
	do := func(t *testing.T, c *http.Client, method, target string, form url.Values) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+target, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s %s: status = %d, want %d", method, target, resp.StatusCode, http.StatusSeeOther)
		}
		return resp.Header.Get("Location")
	}
	login := url.Values{"email": {"user@example.com"}, "password": {"correct-horse-battery"}}

	tests := []struct {
		name   string
		method string
		target string
		want   string
	}{
		{"GET", http.MethodGet, feedPath, feedPath},
		{"GET with a query", http.MethodGet, "/search?q=go", "/search?q=go"},
		{"HEAD", http.MethodHead, feedPath, feedPath},
		{"POST isn't remembered", http.MethodPost, feedPath + "/subscribe", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t)
			if loc := do(t, c, tt.method, tt.target, nil); loc != "/login" {
				t.Fatalf("%s %s: Location = %q, want /login", tt.method, tt.target, loc)
			}
			if loc := do(t, c, http.MethodPost, "/login", login); loc != tt.want {
				t.Errorf("login: Location = %q, want %q", loc, tt.want)
			}
			if loc := do(t, c, http.MethodPost, "/login", login); loc != "/" {
				t.Errorf("second login: Location = %q, want / (returnTo should be forgotten)", loc)
			}
		})
	}

	t.Run("signup", func(t *testing.T) {
		c := newClient(t)
		do(t, c, http.MethodGet, feedPath, nil)
		signup := url.Values{
			"invite":   {testInviteCode},
			"name":     {"Test User"},
			"email":    {"new@example.com"},
			"password": {"correct-horse-battery"},
		}
		if loc := do(t, c, http.MethodPost, "/signup", signup); loc != feedPath {
			t.Errorf("signup: Location = %q, want %q", loc, feedPath)
		}
	})

	t.Run("not a local path", func(t *testing.T) {
		h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "returnTo", "//evil.example")
			s.loginFormHandler(w, r)
		}))
		rr := postForm(t, h.ServeHTTP, "/login", "192.0.2.1:1234", login)
		if loc := rr.Header().Get("Location"); loc != "/" {
			t.Errorf("Location = %q, want /", loc)
		}
	})
}

func TestLoggedInRedirect(t *testing.T) {
	sess := rss.Session{ID: testSessionID, User: rss.User{ID: "user-1"}, LastSeenAt: time.Now()}
	s := newSignupTestServer(t, Services{})

	for _, tt := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/login", s.loginHandler},
		{"/signup", s.signupHandler},
	} {
		t.Run(tt.path, func(t *testing.T) {
			req := requestWithSession(sess)
			req.URL.Path = tt.path
			rr := httptest.NewRecorder()
			tt.handler(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
			}
			if loc := rr.Header().Get("Location"); loc != "/" {
				t.Errorf("Location = %q, want /", loc)
			}
		})
	}
}
