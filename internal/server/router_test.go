package server

import (
	"fmt"
	"io"
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
		{"/static/js/app.js", http.StatusOK, "text/javascript"},
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
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	partials, err := fs.Glob(ui.Templates, "templates/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(pages, partials...) {
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

func TestHeadRequests(t *testing.T) {
	h := newTestServer(t).router()

	tests := []struct {
		path        string
		status      int
		contentType string // prefix; empty skips the check
		location    string
		allow       string
	}{
		{path: "/healthcheck", status: http.StatusOK, contentType: "application/json"},
		{path: "/", status: http.StatusOK, contentType: "text/html"},
		{path: "/login", status: http.StatusOK, contentType: "text/html"},
		{path: "/feeds", status: http.StatusSeeOther, location: "/login"},
		{path: "/static/css/main.css", status: http.StatusOK, contentType: "text/css"},
		{path: "/nope", status: http.StatusNotFound},
		{path: "/logout", status: http.StatusMethodNotAllowed, allow: "POST"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodHead, tt.path, nil))
			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d", rr.Code, tt.status)
			}
			if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
				t.Errorf("Content-Type = %q, want prefix %q", got, tt.contentType)
			}
			if got := rr.Header().Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
			if got := rr.Header().Get("Allow"); got != tt.allow {
				t.Errorf("Allow = %q, want %q", got, tt.allow)
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
		{http.MethodPut, "/login", "GET, HEAD, POST"},
		{http.MethodDelete, "/healthcheck", "GET, HEAD"},
		{http.MethodPut, "/", "GET, HEAD"},
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

func TestSignupRoutes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", SignupEnabled: enabled}, Services{})
			h := s.router()

			do := func(method, target string) *httptest.ResponseRecorder {
				var body io.Reader
				if method == http.MethodPost {
					body = strings.NewReader("")
				}
				req := httptest.NewRequestWithContext(t.Context(), method, target, body)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				return rr
			}

			get, post := do(http.MethodGet, "/signup"), do(http.MethodPost, "/signup")
			if enabled {
				if get.Code != http.StatusOK {
					t.Errorf("GET /signup = %d, want 200", get.Code)
				}
				if post.Code != http.StatusUnprocessableEntity {
					t.Errorf("POST /signup = %d, want 422", post.Code)
				}
			} else {
				for name, rr := range map[string]*httptest.ResponseRecorder{"GET": get, "POST": post} {
					if rr.Code != http.StatusNotFound {
						t.Errorf("%s /signup = %d, want 404", name, rr.Code)
					}
					if !strings.Contains(rr.Body.String(), "<html") {
						t.Errorf("%s /signup body is not the HTML 404 page", name)
					}
				}
			}

			login := do(http.MethodGet, "/login")
			home := do(http.MethodGet, "/")
			failed := do(http.MethodPost, "/login")
			if failed.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /login = %d, want 422", failed.Code)
			}
			for name, rr := range map[string]*httptest.ResponseRecorder{"GET /login": login, "GET /": home, "failed login": failed} {
				if got := strings.Contains(rr.Body.String(), `href="/signup"`); got != enabled {
					t.Errorf("%s links to /signup = %v, want %v", name, got, enabled)
				}
			}
			if !strings.Contains(home.Body.String(), `href="/login"`) {
				t.Error("GET / has no link to /login")
			}
		})
	}
}
