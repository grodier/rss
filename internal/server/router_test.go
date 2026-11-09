package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ui"
)

func TestStaticRoutes(t *testing.T) {
	h := newTestServer(t).router()

	js := entryJS("reading")
	css, err := asset("app/styles/reading.css")
	if err != nil {
		t.Fatal(err)
	}
	chunks := builtChunks(t)
	font, err := asset("shared/fonts/newsreader.woff2")
	if err != nil {
		t.Fatal(err)
	}
	logo, err := asset("img/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	const immutable = "public, max-age=31536000, immutable"

	type staticTest struct {
		path        string
		wantStatus  int
		wantContent string
		wantCache   string
	}
	tests := []staticTest{
		{js, http.StatusOK, "text/javascript", immutable},
		{css, http.StatusOK, "text/css", immutable},
		{"/static/" + chunks[0].File, http.StatusOK, "text/javascript", immutable},
		{font, http.StatusOK, "font/woff2", immutable},
		{logo, http.StatusOK, "image/svg+xml", immutable},
		{"/static/img/logo.svg", http.StatusOK, "image/svg+xml", "no-cache"},
		{"/static/img/logo.svg?v=000000000000", http.StatusOK, "image/svg+xml", "no-cache"},
		// Outdated names: an entry's or a sheet's gets the current file; a
		// chunk's is a 404.
		{"/static/assets/reading-AAAAAAAA.js", http.StatusOK, "text/javascript", "no-cache"},
		{"/static/assets/base-AAAAAAAA.css", http.StatusOK, "text/css", "no-cache"},
		{"/static/assets/nope-AAAAAAAA.js", http.StatusNotFound, "text/html", ""},
		{"/static/assets/login-AAAAAAAA.css", http.StatusNotFound, "text/html", ""},
		{"/static/assets/reading-AAAA.js", http.StatusNotFound, "text/html", ""},
		{"/static/", http.StatusNotFound, "text/html", ""},
		{"/static/assets/", http.StatusNotFound, "text/html", ""},
		{"/static/assets", http.StatusNotFound, "text/html", ""},
		{"/static/.vite/manifest.json", http.StatusNotFound, "text/html", ""},
		{"/static/nope.css", http.StatusNotFound, "text/html", ""},
	}
	for _, c := range chunks {
		tests = append(tests, staticTest{"/static/" + outdated(t, c), http.StatusNotFound, "text/html", ""})
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

// A page rendered by the version being replaced during a deploy asks for its
// own hashed entries; they get the current ones' content.
func TestStaticOutdatedEntryServesCurrent(t *testing.T) {
	h := newTestServer(t).router()
	for _, tt := range []struct{ src, outdated string }{
		{"app/entries/reading.ts", "/static/assets/reading-AAAAAAAA.js"},
		{"app/styles/base.css", "/static/assets/base-AAAAAAAA.css"},
	} {
		t.Run(tt.src, func(t *testing.T) {
			u, err := asset(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			current := httptest.NewRecorder()
			h.ServeHTTP(current, httptest.NewRequest(http.MethodGet, u, nil))
			outdated := httptest.NewRecorder()
			h.ServeHTTP(outdated, httptest.NewRequest(http.MethodGet, tt.outdated, nil))
			if outdated.Code != http.StatusOK || outdated.Body.String() != current.Body.String() {
				t.Errorf("GET %s: status %d, body of %d bytes; want 200 and the %d bytes of %s", tt.outdated, outdated.Code, outdated.Body.Len(), current.Body.Len(), u)
			}
			if got, want := outdated.Header().Get("ETag"), current.Header().Get("ETag"); got != want {
				t.Errorf("GET %s: ETag = %q, want %q", tt.outdated, got, want)
			}
		})
	}
}

func TestStaticRoutesRevalidate(t *testing.T) {
	h := newTestServer(t).router()
	for _, name := range []string{"app/entries/reading.ts", "app/styles/reading.css", "shared/fonts/newsreader.woff2", "img/logo.svg"} {
		t.Run(name, func(t *testing.T) {
			u, err := asset(name)
			if err != nil {
				t.Fatal(err)
			}
			u, _, _ = strings.Cut(u, "?")

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
			sum := sha256.Sum256(rr.Body.Bytes())
			etag := `"` + hex.EncodeToString(sum[:])[:12] + `"`
			if got := rr.Header().Get("ETag"); got != etag {
				t.Errorf("ETag = %q, want %q", got, etag)
			}

			req := httptest.NewRequest(http.MethodGet, u, nil)
			req.Header.Set("If-None-Match", etag)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNotModified {
				t.Errorf("If-None-Match %s: status = %d, want %d", etag, rr.Code, http.StatusNotModified)
			}
		})
	}
}

func TestAsset(t *testing.T) {
	h := newTestServer(t).router()

	js, err := asset("app/entries/reading.ts")
	if err != nil || !regexp.MustCompile(`^/static/assets/reading-[\w-]+\.js$`).MatchString(js) {
		t.Errorf(`asset("app/entries/reading.ts") = %q, %v; want /static/assets/reading-<hash>.js`, js, err)
	}

	// A public file's version is the hash of the bytes it's served with.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/img/logo.svg", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /static/img/logo.svg: status = %d", rr.Code)
	}
	sum := sha256.Sum256(rr.Body.Bytes())
	want := "/static/img/logo.svg?v=" + hex.EncodeToString(sum[:])[:12]
	if got, err := asset("img/logo.svg"); err != nil || got != want {
		t.Errorf(`asset("img/logo.svg") = %q, %v; want %q`, got, err, want)
	}

	for _, name := range []string{"nope.css", strings.TrimPrefix(js, "/static/"), ".vite/manifest.json"} {
		if got, err := asset(name); err == nil {
			t.Errorf("asset(%q) = %q, nil error; want error", name, got)
		}
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
		{path: "/static/img/logo.svg", status: http.StatusOK, contentType: "image/svg+xml"},
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

// entryJS returns the URL of a page group's entry script (TestAssetTags
// checks the pages' heads link it).
func entryJS(group string) string {
	u, _ := asset("app/entries/" + group + ".ts")
	return u
}

// TestRoutesRequireAuthentication walks every route and checks that an
// anonymous request is redirected to /login, unless the route is one of the
// public ones below. A new route is private until it's added to that list.
func TestRoutesRequireAuthentication(t *testing.T) {
	public := map[string]bool{
		"/static/*":                    true,
		"/sites/{id}/icon":             true,
		"/healthcheck":                 true,
		"/rum":                         true,
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
		"/waitlist":                    true,
		"/waitlist/joined":             true,
		"/waitlist/confirm":            true,
		"/waitlist/leave":              true,
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
