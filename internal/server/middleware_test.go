package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

func TestRecoverPanicRendersHTML(t *testing.T) {
	s := newTestServer(t)
	h := s.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if got := rr.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection = %q, want close", got)
	}
}

func TestRecordRequest(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantRoute  string
		wantStatus int
	}{
		// Anonymous, so the login redirect, but counted by its pattern.
		{"pattern, not path", http.MethodGet, "/feeds/0b6e1c5e-0000-4000-8000-000000000001", "/feeds/{id}", http.StatusSeeOther},
		{"public page", http.MethodGet, "/privacy", "/privacy", http.StatusOK},
		{"unknown path", http.MethodGet, "/no/such/page", "(unmatched)", http.StatusNotFound},
		// chi matches no pattern when the method is wrong.
		{"wrong method", http.MethodPut, "/privacy", "(unmatched)", http.StatusMethodNotAllowed},
	}
	// CrossOriginProtection answers before routing, so its 403s are counted
	// with no pattern.
	t.Run("cross-origin 403", func(t *testing.T) {
		rec := &fakeRecorder{}
		s := newTestServerWith(t, Services{Metrics: rec})
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
		seen := rec.observations()
		if len(seen) != 1 || seen[0].route != "(unmatched)" || seen[0].status != http.StatusForbidden {
			t.Errorf("observed %+v, want one (unmatched) 403", seen)
		}
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			s := newTestServerWith(t, Services{Metrics: rec})
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			seen := rec.observations()
			if len(seen) != 1 {
				t.Fatalf("observed %d requests, want 1: %+v", len(seen), seen)
			}
			o := seen[0]
			if o.route != tt.wantRoute || o.method != tt.method || o.status != tt.wantStatus {
				t.Errorf("observed %+v, want route %q, method %s, status %d", o, tt.wantRoute, tt.method, tt.wantStatus)
			}
			if o.bytes != rr.Body.Len() {
				t.Errorf("bytes = %d, want %d", o.bytes, rr.Body.Len())
			}
			if o.d <= 0 {
				t.Errorf("duration = %v, want > 0", o.d)
			}
		})
	}

	t.Run("recovered panic", func(t *testing.T) {
		rec := &fakeRecorder{}
		s := newTestServerWith(t, Services{Metrics: rec})
		// The real router's middleware order, around a route that panics.
		router := chi.NewRouter()
		router.Use(s.recordRequest, s.serverTiming, s.recoverPanic)
		router.Get("/boom/{id}", func(w http.ResponseWriter, r *http.Request) { panic("boom") })

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom/1", nil))

		seen := rec.observations()
		if len(seen) != 1 {
			t.Fatalf("observed %d requests, want 1", len(seen))
		}
		if seen[0].route != "/boom/{id}" || seen[0].status != http.StatusInternalServerError {
			t.Errorf("observed %+v, want /boom/{id} with 500", seen[0])
		}
	})
}

func TestServerTiming(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"HTML page", "/privacy", true},
		{"HTML error page", "/no/such/page", true},
		{"JSON", "/healthcheck", false},
		{"static file", "/static/img/logo.svg", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			newTestServer(t).router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))

			got := rr.Header().Get("Server-Timing")
			if !tt.want {
				if got != "" {
					t.Errorf("Server-Timing = %q, want none", got)
				}
				return
			}
			dur, ok := strings.CutPrefix(got, "app;dur=")
			if !ok {
				t.Fatalf("Server-Timing = %q, want app;dur=<ms>", got)
			}
			if _, frac, ok := strings.Cut(dur, "."); !ok || len(frac) != 1 {
				t.Errorf("Server-Timing duration %q, want one decimal", dur)
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	const sessID = "8f14e45f-ceea-4e7a-9b1d-3c2f0a1b2c3d"
	user := rss.User{ID: "user-1", Name: "Test User", Email: "user@example.com"}
	sessionLastSeen := func(ago time.Duration) func(ctx context.Context, id string) (rss.Session, error) {
		return func(ctx context.Context, id string) (rss.Session, error) {
			if id != sessID {
				t.Errorf("Get id = %q, want %q", id, sessID)
			}
			return rss.Session{ID: id, User: user, LastSeenAt: time.Now().Add(-ago)}, nil
		}
	}

	tests := []struct {
		name       string
		sessionID  string // put in the session before authenticate; "" puts nothing
		getFn      func(ctx context.Context, id string) (rss.Session, error)
		touchErr   error
		wantStatus int
		wantAuth   bool
		wantKey    bool // sessionID still in the session afterwards
		wantGet    bool
		wantTouch  bool
		wantDelete bool
	}{
		{name: "no sessionID", wantStatus: http.StatusOK},
		{name: "malformed ID", sessionID: "user-1", wantStatus: http.StatusOK},
		{
			name:      "no record",
			sessionID: sessID,
			getFn: func(ctx context.Context, id string) (rss.Session, error) {
				return rss.Session{}, rss.ErrNoRecord
			},
			wantStatus: http.StatusOK,
			wantGet:    true,
		},
		{
			name:       "last seen 91 days ago",
			sessionID:  sessID,
			getFn:      sessionLastSeen(91 * 24 * time.Hour),
			wantStatus: http.StatusOK,
			wantGet:    true,
			wantDelete: true,
		},
		{
			name:       "last seen 2 days ago",
			sessionID:  sessID,
			getFn:      sessionLastSeen(2 * 24 * time.Hour),
			wantStatus: http.StatusOK,
			wantAuth:   true,
			wantKey:    true,
			wantGet:    true,
			wantTouch:  true,
		},
		{
			name:       "last seen 1 hour ago",
			sessionID:  sessID,
			getFn:      sessionLastSeen(time.Hour),
			wantStatus: http.StatusOK,
			wantAuth:   true,
			wantKey:    true,
			wantGet:    true,
		},
		{
			name:      "Get error",
			sessionID: sessID,
			getFn: func(ctx context.Context, id string) (rss.Session, error) {
				return rss.Session{}, errors.New("db down")
			},
			wantStatus: http.StatusInternalServerError,
			wantKey:    true,
			wantGet:    true,
		},
		{
			name:       "Touch error",
			sessionID:  sessID,
			getFn:      sessionLastSeen(2 * 24 * time.Hour),
			touchErr:   errors.New("db down"),
			wantStatus: http.StatusOK,
			wantAuth:   true,
			wantKey:    true,
			wantGet:    true,
			wantTouch:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotGet, gotTouch, gotDelete bool
			store := &fakeSessionStore{
				getFn: func(ctx context.Context, id string) (rss.Session, error) {
					gotGet = true
					return tt.getFn(ctx, id)
				},
				touchFn: func(ctx context.Context, id string) error {
					gotTouch = true
					if id != sessID {
						t.Errorf("Touch id = %q, want %q", id, sessID)
					}
					return tt.touchErr
				},
				deleteFn: func(ctx context.Context, id string) error {
					gotDelete = true
					if id != sessID {
						t.Errorf("Delete id = %q, want %q", id, sessID)
					}
					return nil
				},
			}
			s := newTestServerWith(t, Services{SessionService: store})

			var called, gotAuth bool
			var gotID string
			var gotSess rss.Session
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotAuth = s.isAuthenticated(r)
				gotID, _ = s.authenticatedUserID(r)
				gotSess, _ = s.authenticatedSession(r)
			})
			var gotKey bool
			h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.sessionID != "" {
					s.sessionManager.Put(r.Context(), "sessionID", tt.sessionID)
				}
				s.authenticate(next).ServeHTTP(w, r)
				gotKey = s.sessionManager.Exists(r.Context(), "sessionID")
			}))

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusInternalServerError {
				if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("Content-Type = %q, want text/html", ct)
				}
				if called {
					t.Error("next handler called after a Get error")
				}
			}
			if gotAuth != tt.wantAuth {
				t.Errorf("isAuthenticated = %v, want %v", gotAuth, tt.wantAuth)
			}
			if tt.wantAuth {
				if gotID != user.ID {
					t.Errorf("authenticatedUserID = %q, want %q", gotID, user.ID)
				}
				if gotSess.ID != sessID || gotSess.User != user {
					t.Errorf("authenticatedSession = %+v, want ID %q and user %+v", gotSess, sessID, user)
				}
			}
			if gotKey != tt.wantKey {
				t.Errorf("sessionID in session = %v, want %v", gotKey, tt.wantKey)
			}
			if gotGet != tt.wantGet {
				t.Errorf("Get called = %v, want %v", gotGet, tt.wantGet)
			}
			if gotTouch != tt.wantTouch {
				t.Errorf("Touch called = %v, want %v", gotTouch, tt.wantTouch)
			}
			if gotDelete != tt.wantDelete {
				t.Errorf("Delete called = %v, want %v", gotDelete, tt.wantDelete)
			}
		})
	}
}

func requestWithUserID(id string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	return r.WithContext(context.WithValue(r.Context(), authenticatedUserIDContextKey, id))
}

// requestWithSession returns a request authenticated as sess, as authenticate
// leaves it.
func requestWithSession(sess rss.Session) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), authenticatedUserIDContextKey, sess.User.ID)
	ctx = context.WithValue(ctx, authenticatedSessionContextKey, sess)
	return r.WithContext(ctx)
}

func TestAuthenticatedUserID(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name   string
		req    *http.Request
		wantID string
		wantOK bool
	}{
		{"no context value", httptest.NewRequest(http.MethodGet, "/", nil), "", false},
		{"user ID set", requestWithUserID("abc"), "abc", true},
		{"empty user ID", requestWithUserID(""), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := s.authenticatedUserID(tt.req)
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("authenticatedUserID = (%q, %v), want (%q, %v)", id, ok, tt.wantID, tt.wantOK)
			}
			if got := s.isAuthenticated(tt.req); got != tt.wantOK {
				t.Errorf("isAuthenticated = %v, want %v", got, tt.wantOK)
			}
		})
	}
}

func TestRequireAuthentication(t *testing.T) {
	s := newTestServer(t)
	called := false
	h := s.sessionManager.LoadAndSave(s.requireAuthentication(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})))

	t.Run("anonymous", func(t *testing.T) {
		called = false
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusSeeOther {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if loc := rr.Header().Get("Location"); loc != "/login" {
			t.Errorf("Location = %q, want /login", loc)
		}
		if called {
			t.Error("next handler called for anonymous request")
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		called = false
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, requestWithUserID("abc"))
		if !called {
			t.Error("next handler not called")
		}
	})
}

// TestCacheControl checks that logged-in pages, and /, are kept out of every
// cache: no-store for a navigation, and private, no-cache for a script's
// request (a fetch or a beacon), which Chrome needs to keep the page that
// sent it in its back/forward cache. It goes through the router, so it also
// checks those routes have privateCache.
func TestCacheControl(t *testing.T) {
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{}})
	routes := []struct {
		name   string
		target string
		login  bool
	}{
		{"a route that needs login", "/timelines/all", true},
		{"/ logged in", "/", true},
		{"/ logged out", "/", false},
	}
	modes := []struct {
		mode string // Sec-Fetch-Mode; "" sends none
		want string
	}{
		{"", "no-store"},
		{"navigate", "no-store"},
		{"cors", "private, no-cache"},
		{"same-origin", "private, no-cache"},
		{"no-cors", "private, no-cache"},
	}
	for _, route := range routes {
		for _, m := range modes {
			t.Run(route.name+" "+cmp.Or(m.mode, "no header"), func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, route.target, nil)
				if route.login {
					r = r.WithContext(context.WithValue(r.Context(), authenticatedUserIDContextKey, "user-1"))
				}
				if m.mode != "" {
					r.Header.Set("Sec-Fetch-Mode", m.mode)
				}
				rr := httptest.NewRecorder()
				s.router().ServeHTTP(rr, r)
				if rr.Code != http.StatusOK {
					t.Fatalf("status = %d", rr.Code)
				}
				if got := rr.Header().Values("Cache-Control"); len(got) != 1 || got[0] != m.want {
					t.Errorf("Cache-Control = %q, want %q", got, m.want)
				}
			})
		}
	}
}

// TestEmailLinkHeaders checks that every response of the routes email links
// open, GET and POST, keeps their token out of Referer headers and caches,
// and that the routes around them don't get those headers. It goes through
// the router, so it checks those routes have emailLink. The fakes know no
// token: whatever each route answers, the headers are the point.
func TestEmailLinkHeaders(t *testing.T) {
	noRecord := func(context.Context, []byte) (string, error) { return "", rss.ErrNoRecord }
	s := newTestServerWith(t, Services{
		TokenService: &fakeTokenStore{getFn: func(context.Context, []byte, string) (rss.Token, error) {
			return rss.Token{}, rss.ErrNoRecord
		}},
		UserService: &fakeUserStore{
			confirmEmailFn: noRecord,
			changeEmailFn: func(context.Context, []byte) (rss.User, string, error) {
				return rss.User{}, "", rss.ErrNoRecord
			},
		},
		InviteService:   &fakeInviteStore{},
		WaitlistService: &fakeWaitlistStore{},
	})
	form := url.Values{"token": {"unknown"}, "invite": {"unknown"}, "password": {"correct-horse-battery"}}

	tests := []struct {
		name string
		req  *http.Request
	}{
		{"GET /signup", httptest.NewRequest(http.MethodGet, "/signup?invite=unknown", nil)},
		{"GET /signup with no invite", httptest.NewRequest(http.MethodGet, "/signup", nil)},
		{"POST /signup", postFormRequest("/signup", form)},
		{"GET /confirm-email", httptest.NewRequest(http.MethodGet, "/confirm-email?token=unknown", nil)},
		{"POST /confirm-email", postFormRequest("/confirm-email", form)},
		{"GET /email-change", httptest.NewRequest(http.MethodGet, "/email-change?token=unknown", nil)},
		{"POST /email-change", postFormRequest("/email-change", form)},
		{"GET /waitlist/confirm", httptest.NewRequest(http.MethodGet, "/waitlist/confirm?token=unknown", nil)},
		{"POST /waitlist/confirm", postFormRequest("/waitlist/confirm", form)},
		{"GET /waitlist/leave", httptest.NewRequest(http.MethodGet, "/waitlist/leave?token=unknown", nil)},
		{"POST /waitlist/leave", postFormRequest("/waitlist/leave", form)},
		{"GET /password/reset", httptest.NewRequest(http.MethodGet, "/password/reset?token=unknown", nil)},
		{"POST /password/reset", postFormRequest("/password/reset", form)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, tt.req)
			if rr.Code >= http.StatusInternalServerError {
				t.Fatalf("status = %d", rr.Code)
			}
			if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
			// scs adds no-cache="Set-Cookie" after it when the response
			// writes the session cookie.
			if got := rr.Header().Values("Cache-Control"); !slices.Contains(got, "no-store") {
				t.Errorf("Cache-Control = %q, want no-store among them", got)
			}
		})
	}

	t.Run("a route outside the group", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/password/forgot", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if got := rr.Header().Get("Referrer-Policy"); got != "origin-when-cross-origin" {
			t.Errorf("Referrer-Policy = %q, want origin-when-cross-origin", got)
		}
		if got := rr.Header().Values("Cache-Control"); slices.Contains(got, "no-store") {
			t.Errorf("Cache-Control = %q, want no no-store", got)
		}
	})
}

func TestCommonHeadersCSP(t *testing.T) {
	s := newTestServer(t)
	h := s.commonHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	want := "default-src 'self'; img-src 'self' https:; frame-src https://www.youtube-nocookie.com https://player.vimeo.com"
	if got := rr.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("Content-Security-Policy = %q, want %q", got, want)
	}
}

func TestRedirectToCanonicalHost(t *testing.T) {
	production := Config{Env: "production", BaseURL: "https://example.app"}
	tests := []struct {
		name         string
		cfg          Config
		method       string
		target       string
		host         string
		wantLocation string // "" means not redirected
	}{
		{name: "canonical host", cfg: production, method: http.MethodGet, target: "https://example.app/feeds"},
		{name: "old host", cfg: production, method: http.MethodGet, target: "/timelines/abc?before=x", host: "rss.georgerodier.com", wantLocation: "https://example.app/timelines/abc?before=x"},
		{name: "POST to another host", cfg: production, method: http.MethodPost, target: "/logout", host: "rss.georgerodier.com", wantLocation: "https://example.app/logout"},
		{name: "healthcheck on another host", cfg: production, method: http.MethodGet, target: "/healthcheck", host: "something.onrender.com"},
		{name: "case and port ignored", cfg: production, method: http.MethodGet, target: "/feeds", host: "EXAMPLE.APP:443"},
		{name: "development", cfg: Config{Env: "development", BaseURL: "http://localhost:4000"}, method: http.MethodGet, target: "/feeds", host: "127.0.0.1:4000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, tt.cfg, Services{})
			req := httptest.NewRequest(tt.method, tt.target, nil)
			if tt.host != "" {
				req.Host = tt.host
			}
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, req)

			if tt.wantLocation == "" {
				if rr.Code == http.StatusPermanentRedirect {
					t.Fatalf("status = 308 (Location %q), want not redirected", rr.Header().Get("Location"))
				}
				if tt.target == "/healthcheck" && rr.Code != http.StatusOK {
					t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
				}
				return
			}
			if rr.Code != http.StatusPermanentRedirect {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusPermanentRedirect)
			}
			if got := rr.Header().Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
		})
	}
}

func TestCommonHeadersHSTS(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		host string
		want string
	}{
		{name: "production", cfg: Config{Env: "production", BaseURL: "https://example.app"}, host: "example.app", want: "max-age=31536000"},
		{name: "development", cfg: Config{Env: "development", BaseURL: "http://localhost:4000"}, host: "localhost:4000"},
		{name: "redirect from another host", cfg: Config{Env: "production", BaseURL: "https://example.app"}, host: "rss.georgerodier.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, tt.cfg, Services{})
			req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
			req.Host = tt.host
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, req)

			if got := rr.Header().Get("Strict-Transport-Security"); got != tt.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tt.want)
			}
		})
	}
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad log line %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// TestLogRequestPanic checks a panic recoverPanic turns into the 500 page
// is logged as a 500 (httpserve's TestLogRequests covers the rest).
func TestLogRequestPanic(t *testing.T) {
	s := newTestServer(t)
	var buf bytes.Buffer
	s.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	req := httptest.NewRequest(http.MethodGet, "/some/path?q=1", nil)
	req.RemoteAddr = "192.0.2.7:5555"
	s.logRequest(s.recoverPanic(panicking)).ServeHTTP(httptest.NewRecorder(), req)

	var got map[string]any
	for _, l := range logLines(t, &buf) {
		if l["msg"] == "request" {
			got = l
		}
	}
	if got == nil {
		t.Fatalf("no request line in:\n%s", buf.String())
	}
	if got["status"] != float64(http.StatusInternalServerError) || got["ip"] != "192.0.2.7" || got["uri"] != "/some/path?q=1" {
		t.Errorf("request line = %v, want status 500 from 192.0.2.7 for /some/path?q=1", got)
	}
}

func TestLogRequestThroughRouter(t *testing.T) {
	tests := []struct {
		name   string
		req    *http.Request
		status float64
	}{
		{"cross-site POST", func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/login", nil)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			return r
		}(), 403},
		{"healthcheck", httptest.NewRequest(http.MethodGet, "/healthcheck", nil), 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			var buf bytes.Buffer
			s.logger = slog.New(slog.NewJSONHandler(&buf, nil))
			s.router().ServeHTTP(httptest.NewRecorder(), tt.req)

			lines := logLines(t, &buf)
			if len(lines) != 1 || lines[0]["msg"] != "request" || lines[0]["status"] != tt.status {
				t.Errorf("log lines = %v, want one request line with status %v", lines, tt.status)
			}
		})
	}
}
