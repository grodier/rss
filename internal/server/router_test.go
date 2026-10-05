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
		{"/static/", http.StatusNotFound, "text/html"},
		{"/static/css/", http.StatusNotFound, "text/html"},
		{"/static/css/main.css", http.StatusOK, "text/css"},
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

func TestNotFoundRendersHTML(t *testing.T) {
	h := newTestServer(t).router()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), "Page not found") {
		t.Errorf("body does not contain %q: %s", "Page not found", rr.Body.String())
	}
}

func TestLogoutRequiresPost(t *testing.T) {
	h := newTestServer(t).router()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/logout", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /logout status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /logout Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), "Method not allowed") {
		t.Errorf("GET /logout body does not contain %q: %s", "Method not allowed", rr.Body.String())
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

func TestCrossOriginProtection(t *testing.T) {
	h := newTestServer(t).router()

	tests := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int // 0 means "anything but 403"
	}{
		{"cross-site POST", http.MethodPost, "/login", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"cross-origin via Origin", http.MethodPost, "/login", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"same-origin POST", http.MethodPost, "/login", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusUnprocessableEntity},
		{"non-browser POST", http.MethodPost, "/login", nil, http.StatusUnprocessableEntity},
		{"cross-site GET", http.MethodGet, "/login", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Errorf("status = %d, want %d", rr.Code, tt.want)
			}
			if tt.want == http.StatusForbidden {
				if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("Content-Type = %q, want text/html", ct)
				}
				if !strings.Contains(rr.Body.String(), "Request blocked") {
					t.Errorf("body does not contain %q: %s", "Request blocked", rr.Body.String())
				}
			}
		})
	}
}

func TestMethodNotAllowedSetsAllow(t *testing.T) {
	h := newTestServer(t).router()

	tests := []struct {
		method, path, want string
	}{
		{http.MethodGet, "/logout", "POST"},
		{http.MethodPut, "/login", "GET, POST"},
		{http.MethodDelete, "/healthcheck", "GET"},
		{http.MethodPut, "/", "GET"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
			}
			if got := rr.Header().Get("Allow"); got != tt.want {
				t.Errorf("Allow = %q, want %q", got, tt.want)
			}
		})
	}
}
