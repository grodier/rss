package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ui"
)

func TestStaticRoutes(t *testing.T) {
	s := newTestServer(t)
	h := s.router()

	css, err := asset("css/main.css")
	if err != nil {
		t.Fatal(err)
	}
	const immutable = "public, max-age=31536000, immutable"

	tests := []struct {
		path        string
		wantStatus  int
		wantContent string
		wantCache   string
	}{
		{"/static/", http.StatusNotFound, "text/html", ""},
		{"/static/css/", http.StatusNotFound, "text/html", ""},
		{"/static/css/main.css", http.StatusOK, "text/css", ""},
		{"/static/css/main.css?v=000000000000", http.StatusOK, "text/css", ""},
		{css, http.StatusOK, "text/css", immutable},
		{"/static/js/app.js", http.StatusOK, "text/javascript", ""},
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
			if cc := rr.Header().Get("Cache-Control"); cc != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", cc, tt.wantCache)
			}
		})
	}
}

func TestAsset(t *testing.T) {
	for _, name := range []string{"css/main.css", "js/app.js"} {
		b, err := fs.ReadFile(ui.Static, "static/"+name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		want := "/static/" + name + "?v=" + hex.EncodeToString(sum[:])[:12]
		if got, err := asset(name); err != nil || got != want {
			t.Errorf("asset(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := asset("nope.css"); err == nil {
		t.Error(`asset("nope.css") = nil error, want error`)
	}
}

func TestTemplatesLinkAssetsThroughAsset(t *testing.T) {
	var files []string
	for _, glob := range []string{"templates/*.html", "templates/partials/*.html"} {
		m, err := fs.Glob(ui.Templates, glob)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"/static/`) {
			t.Errorf("%s: link static files through the asset function", f)
		}
	}

	rr := httptest.NewRecorder()
	newTestServer(t).router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(rr.Body.String(), `href="/static/css/main.css?v=`) {
		t.Errorf("GET /login does not link a versioned main.css:\n%s", rr.Body.String())
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
	for _, code := range []string{"", testInviteCode} {
		t.Run(fmt.Sprintf("code=%q", code), func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", InviteCode: code}, Services{})
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
			if code != "" {
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
		})
	}
}

// jsVersion is the version in the app.js URL the templates render.
var jsVersion = staticVersions["js/app.js"]

// TestRoutesRequireAuthentication walks every route and checks that an
// anonymous request is redirected to /login, unless the route is one of the
// public ones below. A new route is private until it's added to that list.
func TestRoutesRequireAuthentication(t *testing.T) {
	public := map[string]bool{
		"/static/*":                    true,
		"/sites/{id}/icon":             true,
		"/healthcheck":                 true,
		"/manifest.webmanifest":        true,
		"/privacy":                     true,
		"/terms":                       true,
		"/shared/{token}":              true,
		"/shared/{token}/feed.atom":    true,
		"/signup":                      true,
		"/login":                       true,
		"/logout":                      true,
		"/confirm-email":               true,
		"/confirm-email/done":          true,
		"/email-change":                true,
		"/password/forgot":             true,
		"/password/forgot/sent":        true,
		"/password/reset":              true,
		"/.well-known/change-password": true,
		"/":                            true,
	}

	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", InviteCode: testInviteCode, DebugEmails: debugConfig.DebugEmails}, Services{})
	h := s.router()
	var private int
	err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if public[route] {
			return nil
		}
		private++
		target := strings.ReplaceAll(route, "{id}", testFeedID)
		t.Run(method+" "+route, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), method, target, nil))
			assertRedirect(t, rr, "/login")
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if private == 0 {
		t.Fatal("walked no private routes")
	}
}
