package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordNotEchoed(t *testing.T) {
	const sentinel = "sentinel-pw-123"
	tests := []struct {
		name     string
		template string
		data     any
	}{
		{
			name:     "login",
			template: "login.html",
			data: struct {
				Flash string
				Form  any
			}{Form: loginForm{Email: "a@b.co", Password: sentinel}},
		},
		{
			name:     "signup",
			template: "signup.html",
			data: struct{ Form any }{
				Form: signupForm{Name: "Ann", Email: "a@b.co", Password: sentinel},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			rr := httptest.NewRecorder()
			if err := s.renderHTML(rr, http.StatusUnprocessableEntity, tt.template, tt.data); err != nil {
				t.Fatal(err)
			}
			body := rr.Body.String()
			if strings.Contains(body, sentinel) {
				t.Error("response body contains the submitted password")
			}
			if !strings.Contains(body, "a@b.co") {
				t.Error("response body no longer echoes the email")
			}
		})
	}
}
