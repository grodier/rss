package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSafeReturnPath(t *testing.T) {
	const fallback = "/feeds/abc"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"local path", "/sites/abc", "/sites/abc"},
		{"query string", "/search?q=x", "/search?q=x"},
		{"fragment", "/sites/abc#feed-x", "/sites/abc#feed-x"},
		{"empty", "", fallback},
		{"relative path", "feeds/x", fallback},
		{"protocol-relative", "//evil.example", fallback},
		{"backslash host", `/\evil.example`, fallback},
		{"absolute URL", "https://evil.example/", fallback},
		{"javascript scheme", "javascript:alert(1)", fallback},
		{"control character", "/sites/abc\n", fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeReturnPath(tt.in, fallback); got != tt.want {
				t.Errorf("safeReturnPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

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
		{"oversized", "email=" + strings.Repeat("a", maxFormBytes), http.StatusRequestEntityTooLarge, "Request too large"},
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
