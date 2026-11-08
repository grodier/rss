package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/ui"
)

// pinger is a DatabasePinger that returns err.
type pinger struct{ err error }

func (p pinger) PingContext(context.Context) error { return p.err }

// newTestServer returns a development Server whose database answers.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWith(t, Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001"}, Services{Database: pinger{}})
}

func newTestServerWith(t *testing.T, cfg Config, services Services) *Server {
	t.Helper()
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, services)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func TestHome(t *testing.T) {
	rr := get(t, newTestServer(t), "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<div class="admin">`,
		`<span class="admin-mark">Admin</span>`,
		`<a href="/" aria-current="page">Overview</a>`,
		`<h1>Overview</h1>`,
		`<p class="empty">Nothing here yet.</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %s:\n%s", want, body)
		}
	}
	// The phone menu and the sidebar list the same links.
	if n := strings.Count(body, `aria-current="page"`); n != 2 {
		t.Errorf("GET / marks %d links current, want 2 (the sidebar's and the menu's)", n)
	}
	tags, err := static.Tags("admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, string(tags)) {
		t.Errorf("GET / lacks the admin group's tags %s", tags)
	}
}

func TestNotFound(t *testing.T) {
	for _, path := range []string{"/nope", "/static/nope.css", "/static/.vite/manifest.json"} {
		t.Run(path, func(t *testing.T) {
			rr := get(t, newTestServer(t), path)
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
			}
			body := rr.Body.String()
			if !strings.Contains(body, "<h1>Page not found</h1>") || !strings.Contains(body, `<div class="admin">`) {
				t.Errorf("GET %s isn't the admin's 404 page:\n%s", path, body)
			}
			// No section is current on an error page.
			if strings.Contains(body, `aria-current`) {
				t.Errorf("the 404 page marks a section current")
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	newTestServer(t).router().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
	if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if !strings.Contains(rr.Body.String(), "<h1>Method not allowed</h1>") {
		t.Errorf("not the admin's 405 page:\n%s", rr.Body)
	}
}

func TestHeaders(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "deny",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	}
	for _, env := range []string{"development", "production"} {
		t.Run(env, func(t *testing.T) {
			s := newTestServerWith(t, Config{Port: 4001, Env: env, BaseURL: "https://admin.example.com"}, Services{Database: pinger{}})
			for _, path := range []string{"/", "/nope"} {
				rr := get(t, s, path)
				for name, value := range want {
					if got := rr.Header().Get(name); got != value {
						t.Errorf("GET %s: %s = %q, want %q", path, name, got, value)
					}
				}
				hsts := rr.Header().Get("Strict-Transport-Security")
				if wantHSTS := map[string]string{"production": "max-age=31536000"}[env]; hsts != wantHSTS {
					t.Errorf("GET %s: Strict-Transport-Security = %q, want %q", path, hsts, wantHSTS)
				}
			}
		})
	}
}

func TestCrossOriginPost(t *testing.T) {
	tests := []struct {
		site       string
		wantStatus int
	}{
		// The reader is a sibling subdomain: same-site, not same-origin.
		{"same-site", http.StatusForbidden},
		{"cross-site", http.StatusForbidden},
		// Gets through to the router, which has no POST / (405).
		{"same-origin", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.site, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=1"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", tt.site)
			newTestServer(t).router().ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusForbidden && !strings.Contains(rr.Body.String(), "<h1>Request blocked</h1>") {
				t.Errorf("not the admin's 403 page:\n%s", rr.Body)
			}
		})
	}
}

func TestHealthcheck(t *testing.T) {
	tests := []struct {
		name             string
		ping             error
		status, database string
	}{
		{"database answers", nil, "available", "ok"},
		{"database down", errors.New("connection refused"), "degraded", "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerWith(t, Config{Port: 4001, Env: "development", Version: "abc123"}, Services{Database: pinger{tt.ping}})
			rr := get(t, s, "/healthcheck")
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			var body struct {
				Status     string `json:"status"`
				Database   string `json:"database"`
				SystemInfo struct {
					Environment string `json:"environment"`
					Version     string `json:"version"`
				} `json:"system_info"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("%v: %s", err, rr.Body)
			}
			if body.Status != tt.status || body.Database != tt.database || body.SystemInfo.Environment != "development" || body.SystemInfo.Version != "abc123" {
				t.Errorf("body = %+v", body)
			}
		})
	}
}

func TestStatic(t *testing.T) {
	s := newTestServer(t)
	css, err := static.URL("admin/styles/base.css")
	if err != nil {
		t.Fatal(err)
	}
	logo, err := static.URL("img/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ path, contentType, cache string }{
		{css, "text/css", "public, max-age=31536000, immutable"},
		{logo, "image/svg+xml", "public, max-age=31536000, immutable"},
		{"/static/img/logo.svg", "image/svg+xml", "no-cache"},
	}
	for _, tt := range tests {
		rr := get(t, s, tt.path)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", tt.path, rr.Code, http.StatusOK)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tt.contentType) {
			t.Errorf("GET %s: Content-Type = %q, want %q", tt.path, ct, tt.contentType)
		}
		if cc := rr.Header().Get("Cache-Control"); cc != tt.cache {
			t.Errorf("GET %s: Cache-Control = %q, want %q", tt.path, cc, tt.cache)
		}
	}
	// The reader's files aren't the admin's.
	if _, err := static.URL("app/styles/base.css"); err == nil {
		t.Error(`static.URL("app/styles/base.css") has no error; the reader's sheets aren't in the admin's build`)
	}
}

// Every admin page names its group once, through the assets partial, links
// no stylesheet or script of its own, and has the admin's head.
func TestPagesNameOneGroup(t *testing.T) {
	assetsUse := regexp.MustCompile(`\{\{template "assets" "([^"]*)"\}\}`)
	own := regexp.MustCompile(`<script|rel="(stylesheet|modulepreload)"`)

	pages, err := fs.Glob(ui.AdminTemplates, "admin/*.html")
	if err != nil {
		t.Fatal(err)
	}
	partials, err := fs.Glob(ui.AdminTemplates, "admin/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("found no admin pages")
	}
	for _, f := range append(pages, partials...) {
		b, err := fs.ReadFile(ui.AdminTemplates, f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if own.MatchString(src) {
			t.Errorf("%s links a stylesheet or script of its own; name the page's group instead", f)
		}
		if strings.Contains(f, "/partials/") {
			continue
		}
		if !strings.Contains(src, `{{template "admin-head"}}`) {
			t.Errorf(`%s has no {{template "admin-head"}}`, f)
		}
		uses := assetsUse.FindAllStringSubmatch(src, -1)
		if len(uses) != 1 {
			t.Errorf(`%s uses {{template "assets" "<group>"}} %d times, want once`, f, len(uses))
			continue
		}
		if _, err := static.Tags(uses[0][1]); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
