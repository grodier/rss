package server

import (
	"context"
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
