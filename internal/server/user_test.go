package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSignupFormHandlerPasswordTooManyBytes(t *testing.T) {
	s := newTestServer(t)

	// 30 characters but 120 bytes: over bcrypt's 72-byte limit.
	form := url.Values{
		"name":     {"a"},
		"email":    {"a@b.co"},
		"password": {strings.Repeat("😀", 30)},
	}
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.signupFormHandler(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rr.Body.String(), "Password is too long") {
		t.Errorf("body does not contain %q", "Password is too long")
	}
}
