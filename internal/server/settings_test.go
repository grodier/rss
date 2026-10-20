package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

var settingsSession = rss.Session{
	ID:   "123e4567-e89b-12d3-a456-426614174099",
	User: rss.User{ID: "123e4567-e89b-12d3-a456-426614174001", Name: "Ada Lovelace", Email: "ada@example.com"},
}

func TestSettingsHandler(t *testing.T) {
	t.Run("shows the account", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(),
			"Ada Lovelace",
			"ada@example.com",
			`href="/settings/name"`,
			`<form class="settings-logout" action="/logout" method="post">`,
			`<a class="rail-account" href="/settings" aria-current="page">`,
		)
	})

	t.Run("flash is shown once", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		var flashLeft bool
		s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "flash", "Your name was updated.")
			s.settingsHandler(w, r)
			flashLeft = s.sessionManager.Exists(r.Context(), "flash")
		})).ServeHTTP(rr, requestWithSession(settingsSession))

		assertBodyContains(t, rr.Body.String(), `<div class="flash">Your name was updated.</div>`)
		if flashLeft {
			t.Error("flash is still in the session after the page showed it")
		}
	})
}

func TestSettingsRoutesRequireAuthentication(t *testing.T) {
	tests := []struct {
		method, target string
	}{
		{http.MethodGet, "/settings"},
		{http.MethodGet, "/settings/name"},
		{http.MethodPost, "/settings/name"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			s := newTestServer(t)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rr := httptest.NewRecorder()

			s.router().ServeHTTP(rr, req)

			assertRedirect(t, rr, "/login")
		})
	}
}

func TestSettingsNameHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsNameHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body, `value="Ada Lovelace"`, `<a class="btn" href="/settings">Cancel</a>`)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}
}

// postSettingsName posts name to POST /settings/name as settingsSession. It
// returns the response and the flash message left in the session.
func postSettingsName(t *testing.T, s *Server, name string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	form := url.Values{"name": {name}}
	req := httptest.NewRequest(http.MethodPost, "/settings/name", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(requestWithSession(settingsSession).Context())
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.settingsNameFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func TestSettingsNameForm(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantStatus int
		wantError  string
	}{
		{"blank", "   ", http.StatusUnprocessableEntity, "Name is required"},
		{"101 characters", strings.Repeat("a", 101), http.StatusUnprocessableEntity, "Name must not be more than 100 characters long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s := newTestServerWith(t, Services{UserService: &fakeUserStore{
				updateNameFn: func(context.Context, string, string) error {
					calls++
					return nil
				},
			}})

			rr, _ := postSettingsName(t, s, tt.in)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			assertBodyContains(t, rr.Body.String(),
				`<p class="error" id="name-error">`+tt.wantError+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
			if calls != 0 {
				t.Errorf("UpdateName called %d times, want 0", calls)
			}
		})
	}

	t.Run("valid name is trimmed and saved", func(t *testing.T) {
		var gotID, gotName string
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateNameFn: func(ctx context.Context, id, name string) error {
				gotID, gotName = id, name
				return nil
			},
		}})

		rr, flash := postSettingsName(t, s, "  Ann  ")

		assertRedirect(t, rr, "/settings")
		if gotID != settingsSession.User.ID || gotName != "Ann" {
			t.Errorf("UpdateName(%q, %q), want (%q, %q)", gotID, gotName, settingsSession.User.ID, "Ann")
		}
		if want := "Your name was updated."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateNameFn: func(context.Context, string, string) error {
				return errors.New("boom")
			},
		}})

		rr, flash := postSettingsName(t, s, "Ann")

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
		if flash != "" {
			t.Errorf("flash = %q, want none", flash)
		}
	})
}
