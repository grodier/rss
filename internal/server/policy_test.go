package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testSupportEmail = "help@example.com"

// newPolicyTestServer is newTestServerConfig with a support address and
// signup open to testInviteCode.
func newPolicyTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{
		Port:         4000,
		Env:          "development",
		InviteCode:   testInviteCode,
		SupportEmail: testSupportEmail,
	}, Services{})
}

func TestPolicyPages(t *testing.T) {
	for _, target := range []string{"/privacy", "/terms"} {
		t.Run(target, func(t *testing.T) {
			s := newPolicyTestServer(t)
			rr := httptest.NewRecorder()
			s.router().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s without login = %d, want %d", target, rr.Code, http.StatusOK)
			}
			assertBodyContains(t, rr.Body.String(),
				`<a href="mailto:help@example.com">help@example.com</a>`,
				`Last updated <time datetime="`,
				`<a href="/">Back to Paper Round</a>`,
			)
		})
	}
}

// TestPolicyLinks checks that signup, login, the logged-out home page and
// Settings link to the privacy policy and terms.
func TestPolicyLinks(t *testing.T) {
	s := newPolicyTestServer(t)
	get := func(target string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
		return rr
	}
	settings := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(settings, requestWithSession(settingsSession))

	pages := map[string]*httptest.ResponseRecorder{
		"GET /signup":   get("/signup"),
		"GET /login":    get("/login"),
		"GET /":         get("/"),
		"GET /settings": settings,
	}
	for name, rr := range pages {
		t.Run(name, func(t *testing.T) {
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			assertBodyContains(t, rr.Body.String(), `href="/privacy"`, `href="/terms"`)
		})
	}
}
