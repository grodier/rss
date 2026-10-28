package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
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

func TestLogRequest(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	tests := []struct {
		name       string
		wrap       func(s *Server, h http.Handler) http.Handler
		h          http.HandlerFunc
		wantStatus float64
		wantSize   float64
	}{
		{"explicit status and body", nil, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("hello"))
		}, 201, 5},
		{"nothing written", nil, func(w http.ResponseWriter, r *http.Request) {}, 200, 0},
		{"body without WriteHeader", nil, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hi"))
		}, 200, 2},
		{"panic", func(s *Server, h http.Handler) http.Handler { return s.recoverPanic(h) },
			func(w http.ResponseWriter, r *http.Request) { panicking.ServeHTTP(w, r) }, 500, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			var buf bytes.Buffer
			s.logger = slog.New(slog.NewJSONHandler(&buf, nil))
			var h http.Handler = tt.h
			if tt.wrap != nil {
				h = tt.wrap(s, h)
			}
			req := httptest.NewRequest(http.MethodGet, "/some/path?q=1", nil)
			req.RemoteAddr = "192.0.2.7:5555"
			s.logRequest(h).ServeHTTP(httptest.NewRecorder(), req)

			lines := logLines(t, &buf)
			var got map[string]any
			for _, l := range lines {
				if l["msg"] == "request" {
					got = l
				}
			}
			if got == nil {
				t.Fatalf("no request line in %v", lines)
			}
			if got["status"] != tt.wantStatus {
				t.Errorf("status = %v, want %v", got["status"], tt.wantStatus)
			}
			if tt.wantSize >= 0 && got["size"] != tt.wantSize {
				t.Errorf("size = %v, want %v", got["size"], tt.wantSize)
			}
			if got["method"] != "GET" || got["uri"] != "/some/path?q=1" || got["ip"] != "192.0.2.7" || got["proto"] != "HTTP/1.1" {
				t.Errorf("unexpected fields: %v", got)
			}
			if _, ok := got["duration"]; !ok {
				t.Error("missing duration")
			}
		})
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
