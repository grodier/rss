package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	for _, exists := range []bool{false, true} {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			existsFn: func(ctx context.Context, id string) (bool, error) {
				if id != "user-1" {
					t.Errorf("Exists id = %q, want user-1", id)
				}
				return exists, nil
			},
		}})

		var got bool
		var gotID string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = s.isAuthenticated(r)
			gotID, _ = s.authenticatedUserID(r)
		})
		h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "authenticatedUserID", "user-1")
			s.authenticate(next).ServeHTTP(w, r)
		}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		if got != exists {
			t.Errorf("exists=%v: isAuthenticated = %v", exists, got)
		}
		wantID := ""
		if exists {
			wantID = "user-1"
		}
		if gotID != wantID {
			t.Errorf("exists=%v: authenticatedUserID = %q, want %q", exists, gotID, wantID)
		}
	}
}

func requestWithUserID(id string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	return r.WithContext(context.WithValue(r.Context(), authenticatedUserIDContextKey, id))
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
	h := s.requireAuthentication(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

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

	want := "default-src 'self'; img-src 'self' https:"
	if got := rr.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("Content-Security-Policy = %q, want %q", got, want)
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
