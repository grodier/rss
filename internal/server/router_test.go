package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/ui"
)

func TestStaticRoutes(t *testing.T) {
	s := newTestServer(t)
	h := s.router()

	tests := []struct {
		path        string
		wantStatus  int
		wantContent string
	}{
		{"/static/", http.StatusNotFound, ""},
		{"/static/css/", http.StatusNotFound, ""},
		{"/static/css/main.css", http.StatusOK, "text/css"},
		{"/static/js/main.js", http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tt.wantContent) {
				t.Errorf("Content-Type = %q, want it to contain %q", ct, tt.wantContent)
			}
		})
	}
}

func TestLogoutRequiresPost(t *testing.T) {
	h := newTestServer(t).router()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/logout", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /logout status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("POST /logout status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("POST /logout Location = %q, want /login", loc)
	}
}

func TestTemplatesLogoutFormUsesPost(t *testing.T) {
	files, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `action="/logout" method="get"`) {
			t.Errorf("%s: logout form must use method=\"post\"", f)
		}
	}
}
