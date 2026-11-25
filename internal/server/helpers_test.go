package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/httpserve"
)

func TestDecodePostFormErrors(t *testing.T) {
	s := newSignupTestServer(t, Services{})
	handlers := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"login", s.loginFormHandler},
		{"signup", s.signupFormHandler},
		{"lookups", s.lookupCreateHandler},
		{"subscribe", s.feedSubscribeHandler},
		{"unsubscribe", s.feedUnsubscribeHandler},
	}
	bodies := []struct {
		name       string
		body       string
		wantStatus int
		wantTitle  string
	}{
		{"malformed", "q=%zz", http.StatusBadRequest, "Bad request"},
		{"oversized", "email=" + strings.Repeat("a", httpserve.MaxFormBytes), http.StatusRequestEntityTooLarge, "Request too large"},
	}
	for _, hh := range handlers {
		for _, b := range bodies {
			t.Run(hh.name+"/"+b.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(b.body))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("id", testFeedID)
				ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
				req = req.WithContext(context.WithValue(ctx, authenticatedUserIDContextKey, "user-1"))
				rr := httptest.NewRecorder()

				s.sessionManager.LoadAndSave(hh.h).ServeHTTP(rr, req)

				if rr.Code != b.wantStatus {
					t.Fatalf("status = %d, want %d", rr.Code, b.wantStatus)
				}
				if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("Content-Type = %q, want text/html", ct)
				}
				if !strings.Contains(rr.Body.String(), b.wantTitle) {
					t.Errorf("body does not contain %q", b.wantTitle)
				}
			})
		}
	}
}

func TestUUIDParam(t *testing.T) {
	const valid = "123e4567-e89b-12d3-a456-426614174000"
	tests := []struct {
		name   string
		param  string
		wantOK bool
	}{
		{"uuid", valid, true},
		{"not a uuid", "abc", false},
		{"trailing character", valid + "a", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.param)
			req := httptest.NewRequest(http.MethodGet, "/feeds/x", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rr := httptest.NewRecorder()

			id, ok := s.uuidParam(rr, req, "id")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if id != tt.param {
					t.Errorf("id = %q, want %q", id, tt.param)
				}
				if rr.Body.Len() != 0 || len(rr.Header()) != 0 {
					t.Errorf("wrote a response: headers %v, body %q", rr.Header(), rr.Body.String())
				}
				return
			}
			if id != "" {
				t.Errorf("id = %q, want empty", id)
			}
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}
			if body := rr.Body.String(); !strings.Contains(body, "Page not found") {
				t.Errorf("body isn't the 404 page: %q", body)
			}
		})
	}
}
