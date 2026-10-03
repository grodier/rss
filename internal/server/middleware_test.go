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
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = s.isAuthenticated(r)
		})
		h := s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "authenticatedUserID", "user-1")
			s.authenticate(next).ServeHTTP(w, r)
		}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		if got != exists {
			t.Errorf("exists=%v: isAuthenticated = %v", exists, got)
		}
	}
}
