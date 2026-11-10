package admin

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
)

func TestLogin(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	b := newBrowser(s)

	// An anonymous page sends the browser to /login, and remembers it.
	rr := b.get(t, "/?from=test")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous GET / = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
	before := b.cookie
	if before == nil {
		t.Fatal("no session cookie after an anonymous page")
	}

	rr = b.get(t, "/login")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `<form class="admin-form" action="/login" method="post"`) {
		t.Fatalf("GET /login = %d:\n%s", rr.Code, rr.Body)
	}
	// No sections or Log out before logging in.
	if strings.Contains(rr.Body.String(), "admin-links") || strings.Contains(rr.Body.String(), `action="/logout"`) {
		t.Errorf("the login page shows the nav's links:\n%s", rr.Body)
	}

	rr = b.post(t, "/login", url.Values{"email": {"ADMIN@example.com"}, "password": {testPassword}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login/code" {
		t.Fatalf("POST /login = %d to %q, want 303 to /login/code:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body)
	}
	afterPassword := b.cookie
	if afterPassword.Value == before.Value {
		t.Error("the password step kept the session token, want it renewed")
	}

	// The password alone doesn't log in.
	if rr := b.get(t, "/?from=test"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / after the password only = %d, want 303 to /login", rr.Code)
	}

	rr = b.get(t, "/login/code")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `autocomplete="one-time-code"`) {
		t.Fatalf("GET /login/code = %d:\n%s", rr.Code, rr.Body)
	}

	rr = b.post(t, "/login/code", url.Values{"code": {currentCode(s)}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/?from=test" {
		t.Fatalf("POST /login/code = %d to %q, want 303 to the remembered page:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body)
	}
	if b.cookie.Value == afterPassword.Value {
		t.Error("the code step kept the session token, want it renewed")
	}

	rr = b.get(t, "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / after logging in = %d, want 200", rr.Code)
	}
	for _, want := range []string{`<p>admin@example.com</p>`, `action="/logout"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("GET / lacks %s", want)
		}
	}

	// A logged-in admin has no use for the login page.
	if rr := b.get(t, "/login"); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Errorf("GET /login when logged in = %d to %q, want 303 to /", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLoginPasswordErrors(t *testing.T) {
	tests := []struct {
		name, email, password string
		want                  string
	}{
		{"wrong password", testEmail, "wrong password", "That email and password don&#39;t match an admin."},
		// The same error, so the page doesn't say which emails are admins'.
		{"unknown email", "reader@example.com", testPassword, "That email and password don&#39;t match an admin."},
		{"blank email", "", testPassword, "Email is required"},
		{"invalid email", "admin", testPassword, "Email must be a valid email address"},
		{"blank password", testEmail, "", "Password is required"},
		{"long password", testEmail, strings.Repeat("a", 257), "Password is too long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestServerWithAdmin(t)
			b := newBrowser(s)
			rr := b.post(t, "/login", url.Values{"email": {tt.email}, "password": {tt.password}})
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("body lacks %q:\n%s", tt.want, rr.Body)
			}
			// Nothing is pending: the code page sends the browser back.
			if rr := b.get(t, "/login/code"); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
				t.Errorf("GET /login/code after a failed password = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
			}
		})
	}
}

// passwordStep returns a browser past the password step.
func passwordStep(t *testing.T, s *Server) *browser {
	t.Helper()
	b := newBrowser(s)
	if rr := b.post(t, "/login", url.Values{"email": {testEmail}, "password": {testPassword}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303:\n%s", rr.Code, rr.Body)
	}
	return b
}

func TestLoginCodes(t *testing.T) {
	step := testNow.Unix() / totpStepSeconds
	tests := []struct {
		name   string
		code   string
		wantOK bool
	}{
		{"current code", Code(testSecret, step), true},
		{"with spaces", " " + Code(testSecret, step)[:3] + " " + Code(testSecret, step)[3:] + " ", true},
		{"previous step", Code(testSecret, step-1), true},
		{"next step", Code(testSecret, step+1), true},
		{"two steps old", Code(testSecret, step-2), false},
		{"another secret's", Code([]byte("another secret 12345"), step), false},
		{"recovery code", testRecovery, true},
		{"recovery code, typed loosely", "ABCD EFGH", true},
		{"unknown recovery code", "aaaa-aaaa", false},
		{"nonsense", "hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestServerWithAdmin(t)
			b := passwordStep(t, s)
			rr := b.post(t, "/login/code", url.Values{"code": {tt.code}})
			if tt.wantOK {
				if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
					t.Fatalf("POST /login/code = %d to %q, want 303 to /:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body)
				}
				return
			}
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), `<p class="error" id="code-error">That code didn&#39;t work.</p>`) {
				t.Errorf("body lacks the code's error:\n%s", rr.Body)
			}
			if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
				t.Errorf("GET / after a wrong code = %d, want 303 to /login", rr.Code)
			}
		})
	}
}

func TestLoginCodeWorksOnce(t *testing.T) {
	for _, tt := range []struct{ name, code string }{
		{"TOTP code", Code(testSecret, testNow.Unix()/totpStepSeconds)},
		{"recovery code", testRecovery},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestServerWithAdmin(t)
			if rr := passwordStep(t, s).post(t, "/login/code", url.Values{"code": {tt.code}}); rr.Code != http.StatusSeeOther {
				t.Fatalf("first use = %d, want 303", rr.Code)
			}
			if rr := passwordStep(t, s).post(t, "/login/code", url.Values{"code": {tt.code}}); rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("second use = %d, want 422", rr.Code)
			}
		})
	}
}

func TestLoginCodeStepExpires(t *testing.T) {
	s, now := newTestServerWithAdmin(t)
	b := passwordStep(t, s)
	*now = now.Add(pendingLoginWindow + time.Second)

	rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("POST /login/code after the window = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
	if body := b.get(t, "/login").Body.String(); !strings.Contains(body, "That took too long. Log in again.") {
		t.Errorf("GET /login lacks the flash:\n%s", body)
	}
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / = %d, want 303 to /login", rr.Code)
	}
}

func TestLoginLimiters(t *testing.T) {
	t.Run("per client IP, across both steps", func(t *testing.T) {
		s, _ := newTestServerWithAdmin(t)
		b := newBrowser(s)
		wrong := url.Values{"email": {testEmail}, "password": {"wrong"}}
		for i := range 10 {
			if rr := b.post(t, "/login", wrong); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d = %d, want 422", i+1, rr.Code)
			}
		}
		rr := b.post(t, "/login", url.Values{"email": {testEmail}, "password": {testPassword}})
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt 11 = %d, want 429", rr.Code)
		}
		if rr.Header().Get("Retry-After") == "" {
			t.Error("no Retry-After")
		}
		if !strings.Contains(rr.Body.String(), "Too many attempts. Try again in 15 minutes.") {
			t.Errorf("body lacks the limit's message:\n%s", rr.Body)
		}
	})

	t.Run("wrong codes per admin", func(t *testing.T) {
		s, _ := newTestServerWithAdmin(t)
		b := passwordStep(t, s)
		for i := range 5 {
			if rr := b.post(t, "/login/code", url.Values{"code": {"000000"}}); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("wrong code %d = %d, want 422", i+1, rr.Code)
			}
		}
		// The 7th attempt from this IP: under its limit, so this is the
		// admin's.
		rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}})
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("a right code after 5 wrong ones = %d, want 429", rr.Code)
		}
	})

	t.Run("right codes aren't counted", func(t *testing.T) {
		s, now := newTestServerWithAdmin(t)
		s.loginLimiter = ratelimit.New(100, time.Minute) // only the code limiter is under test
		for range 6 {
			*now = now.Add(totpStepSeconds * time.Second)
			b := passwordStep(t, s)
			if rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}}); rr.Code != http.StatusSeeOther {
				t.Fatalf("POST /login/code = %d, want 303", rr.Code)
			}
		}
	})
}

func TestRequireRecentCode(t *testing.T) {
	s, now := newTestServerWithAdmin(t)
	danger := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("done")) })
	mux := http.NewServeMux()
	mux.Handle("/", s.router())
	mux.Handle("/danger", s.withSession(s.requireAuthentication(s.requireRecentCode(danger))))

	b := loggedIn(t, s)
	b.handler = mux

	if rr := b.get(t, "/danger"); rr.Code != http.StatusOK {
		t.Fatalf("GET /danger right after logging in = %d, want 200", rr.Code)
	}

	*now = now.Add(recentCodeWindow + time.Second)
	rr := b.get(t, "/danger?id=1")
	wantLocation := "/login/code?next=%2Fdanger%3Fid%3D1"
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != wantLocation {
		t.Fatalf("GET /danger with an old code = %d to %q, want 303 to %q", rr.Code, rr.Header().Get("Location"), wantLocation)
	}
	// A form goes back to its page, at the same path.
	if rr := b.post(t, "/danger", nil); rr.Header().Get("Location") != "/login/code?next=%2Fdanger" {
		t.Errorf("POST /danger with an old code redirects to %q, want /login/code?next=%%2Fdanger", rr.Header().Get("Location"))
	}

	rr = b.get(t, wantLocation)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", wantLocation, rr.Code)
	}
	for _, want := range []string{"<h1>Confirm it's you</h1>", `<input type="hidden" name="next" value="/danger?id=1" />`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("the confirm page lacks %s:\n%s", want, rr.Body)
		}
	}

	if rr := b.post(t, "/login/code", url.Values{"code": {"000000"}, "next": {"/danger?id=1"}}); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong code = %d, want 422", rr.Code)
	}
	rr = b.post(t, "/login/code", url.Values{"code": {currentCode(s)}, "next": {"/danger?id=1"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/danger?id=1" {
		t.Fatalf("POST /login/code = %d to %q, want 303 back to /danger?id=1", rr.Code, rr.Header().Get("Location"))
	}
	if rr := b.get(t, "/danger"); rr.Code != http.StatusOK {
		t.Errorf("GET /danger after the code = %d, want 200", rr.Code)
	}
}

func TestLoginCodeNextIsLocal(t *testing.T) {
	for _, next := range []string{"//evil.example", "https://evil.example/", `/\evil.example`} {
		t.Run(next, func(t *testing.T) {
			s, _ := newTestServerWithAdmin(t)
			b := passwordStep(t, s)
			rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}, "next": {next}})
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
				t.Errorf("POST /login/code with next %q = %d to %q, want 303 to /", next, rr.Code, rr.Header().Get("Location"))
			}
		})
	}
}

func TestLogout(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	b := loggedIn(t, s)
	old := *b.cookie

	rr := b.post(t, "/logout", nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("POST /logout = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
	if b.cookie.Value == old.Value {
		t.Error("logging out kept the session token, want it renewed")
	}
	if body := b.get(t, "/login").Body.String(); !strings.Contains(body, "You&#39;ve logged out.") {
		t.Errorf("GET /login lacks the flash:\n%s", body)
	}
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / after logging out = %d, want 303", rr.Code)
	}
	// The old token no longer works.
	b.cookie = &old
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / with the token from before logging out = %d, want 303", rr.Code)
	}
}

func TestDeletedAdminIsLoggedOut(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	b := loggedIn(t, s)
	admins := s.services.Admins.(*fakeAdmins)
	for id := range admins.admins {
		admins.delete(id)
	}
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Errorf("GET / after the admin was deleted = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSessionCookie(t *testing.T) {
	for _, tt := range []struct {
		env, name string
		secure    bool
	}{
		{"development", "admin_session", false},
		{"production", "__Host-admin_session", true},
	} {
		t.Run(tt.env, func(t *testing.T) {
			sm := NewSessionManager(nil, tt.env)
			c := sm.Cookie
			if c.Name != tt.name || c.Secure != tt.secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
				t.Errorf("cookie = %+v; want %s, Secure %v, HttpOnly, SameSite=Strict, Path=/, no Domain", c, tt.name, tt.secure)
			}
			if sm.IdleTimeout != 30*time.Minute || sm.Lifetime != 12*time.Hour {
				t.Errorf("idle timeout, lifetime = %v, %v; want 30m, 12h", sm.IdleTimeout, sm.Lifetime)
			}
		})
	}
}

// publicRoutes are the routes an anonymous request may reach: the login's,
// and the healthcheck. Static files, under /static/, are public too.
var publicRoutes = map[string]bool{
	"GET /login":       true,
	"POST /login":      true,
	"GET /login/code":  true,
	"POST /login/code": true,
	"POST /logout":     true,
	"GET /healthcheck": true,
}

// TestRoutesRequireAuthentication checks that every route not in
// publicRoutes sends an anonymous request to /login.
func TestRoutesRequireAuthentication(t *testing.T) {
	s := newTestServer(t)
	routes, ok := s.router().(chi.Routes)
	if !ok {
		t.Fatal("router isn't a chi.Routes")
	}
	n := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if publicRoutes[key] || strings.HasPrefix(route, "/static/") {
			return nil
		}
		n++
		path := strings.NewReplacer("{id}", "1", "*", "x").Replace(route)
		rr := newBrowser(s).do(t, method, path, url.Values{})
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Errorf("anonymous %s = %d to %q, want 303 to /login", key, rr.Code, rr.Header().Get("Location"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("found no routes that need a login")
	}
}

func TestLoginIsAudited(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	audit := s.services.Audit.(*fakeAudit)
	adminID := s.services.Admins.(*fakeAdmins).byEmail(testEmail).ID

	b := newBrowser(s)
	b.post(t, "/login", url.Values{"email": {"nobody@example.com"}, "password": {testPassword}})
	b.post(t, "/login", url.Values{"email": {testEmail}, "password": {"wrong password"}})
	b = passwordStep(t, s)
	b.post(t, "/login/code", url.Values{"code": {"000000"}})
	if rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /login/code = %d, want 303", rr.Code)
	}
	b.post(t, "/logout", nil)
	// An anonymous logout records nothing.
	newBrowser(s).post(t, "/logout", nil)

	web := func(id, email, action string) rss.AuditEntry {
		return rss.AuditEntry{AuditActor: rss.AuditActor{AdminID: id, AdminEmail: email, Via: "web", IP: "192.0.2.1"}, Action: action}
	}
	want := []rss.AuditEntry{
		web("", "nobody@example.com", "login_failed"),
		web("", testEmail, "login_failed"),
		web(adminID, "", "login_failed"),
		web(adminID, "", "login"),
		web(adminID, testEmail, "logout"),
	}
	var got []rss.AuditEntry
	for _, e := range audit.events {
		got = append(got, e.AuditEntry)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit log:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoginNeedsItsRecord(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	s.services.Audit.(*fakeAudit).err = errors.New("audit log down")
	b := passwordStep(t, s)
	if rr := b.post(t, "/login/code", url.Values{"code": {currentCode(s)}}); rr.Code != http.StatusInternalServerError {
		t.Fatalf("POST /login/code when it can't be recorded = %d, want 500", rr.Code)
	}
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / after an unrecorded login = %d, want 303 to /login", rr.Code)
	}
}

func TestLogoutWithoutItsRecord(t *testing.T) {
	s, _ := newTestServerWithAdmin(t)
	b := loggedIn(t, s)
	s.services.Audit.(*fakeAudit).err = errors.New("audit log down")
	if rr := b.post(t, "/logout", nil); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("POST /logout when it can't be recorded = %d to %q, want 303 to /login", rr.Code, rr.Header().Get("Location"))
	}
	if rr := b.get(t, "/"); rr.Code != http.StatusSeeOther {
		t.Errorf("GET / after logging out = %d, want 303", rr.Code)
	}
}
