package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
			`href="/settings/password"`,
			`<form class="settings-devices" action="/settings/log-out-others" method="post">`,
			`<form class="settings-logout" action="/logout" method="post">`,
			`<a class="rail-account" href="/settings" aria-current="page">`,
		)
	})

	t.Run("help shows the support address", func(t *testing.T) {
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", SupportEmail: "help@example.com"}, Services{})
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

		assertBodyContains(t, rr.Body.String(),
			`<a class="settings-row" href="mailto:help@example.com">`,
			"Questions or problems? Email help@example.com",
		)
	})

	t.Run("help without a support address", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

		body := rr.Body.String()
		if strings.Contains(body, "mailto:") || strings.Contains(body, "Questions or problems?") {
			t.Errorf("body has a support row without a support address: %s", body)
		}
		assertBodyContains(t, body, `href="/privacy"`, `href="/terms"`)
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
		{http.MethodGet, "/settings/password"},
		{http.MethodPost, "/settings/password"},
		{http.MethodPost, "/settings/log-out-others"},
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

func TestSettingsPasswordHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsPasswordHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		`<input type="email" id="email" value="ada@example.com" autocomplete="username" readonly />`,
		`autocomplete="current-password"`,
		`autocomplete="new-password"`,
		`<p class="hint" id="new_password-hint">At least 8 characters.</p>`,
		"You'll stay logged in here. Every other device will be logged out.",
		`<button class="btn-primary" type="submit">Change password</button>`,
		`<a class="rail-account" href="/settings" aria-current="page">`,
	)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}
}

// postSettingsPassword posts current and next to POST /settings/password as
// settingsSession. It returns the response and the flash message left in the
// session.
func postSettingsPassword(t *testing.T, s *Server, current, next string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	form := url.Values{"current_password": {current}, "new_password": {next}}
	req := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(requestWithSession(settingsSession).Context())
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.settingsPasswordFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func TestSettingsPasswordForm(t *testing.T) {
	const current, next = "old-correct-horse", "new-staple-battery"

	tests := []struct {
		name          string
		current, next string
		storeErr      error
		wantField     string
		wantError     string
	}{
		{"blank current password", "", next, nil, "current_password", "Enter your current password."},
		{"common new password", current, "password123", nil, "new_password", "This password is too common. Try a longer one, or a few unrelated words."},
		{"new password equal to current", current, current, nil, "new_password", "Your new password must be different from your current one."},
		{"wrong current password", current, next, rss.ErrWrongPassword, "current_password", "That password isn&#39;t right."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &fakeEmailQueue{}
			s := newTestServerWith(t, Services{
				UserService: &fakeUserStore{
					changePasswordFn: func(context.Context, string, string, string, string) error {
						if tt.storeErr == nil {
							t.Error("ChangePassword must not be called")
						}
						return tt.storeErr
					},
				},
				EmailQueue: queue,
			})

			rr, flash := postSettingsPassword(t, s, tt.current, tt.next)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			body := rr.Body.String()
			assertBodyContains(t, body,
				`<p class="error" id="`+tt.wantField+`-error">`+tt.wantError+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
			for _, pw := range []string{tt.current, tt.next} {
				if pw != "" && strings.Contains(body, pw) {
					t.Errorf("the page echoes the password %q back", pw)
				}
			}
			if n := len(queue.messages()); n != 0 {
				t.Errorf("queued %d emails, want 0", n)
			}
			if flash != "" {
				t.Errorf("flash = %q, want none", flash)
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		var gotUserID, gotSessionID, gotCurrent, gotNext string
		queue := &fakeEmailQueue{}
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				changePasswordFn: func(ctx context.Context, userID, keepSessionID, current, next string) error {
					gotUserID, gotSessionID, gotCurrent, gotNext = userID, keepSessionID, current, next
					return nil
				},
			},
			EmailQueue: queue,
		})

		rr, flash := postSettingsPassword(t, s, current, next)

		assertRedirect(t, rr, "/settings")
		if gotUserID != settingsSession.User.ID || gotSessionID != settingsSession.ID || gotCurrent != current || gotNext != next {
			t.Errorf("ChangePassword(%q, %q, %q, %q), want (%q, %q, %q, %q)",
				gotUserID, gotSessionID, gotCurrent, gotNext,
				settingsSession.User.ID, settingsSession.ID, current, next)
		}
		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		if msgs[0].To != settingsSession.User.Email {
			t.Errorf("To = %q, want %q", msgs[0].To, settingsSession.User.Email)
		}
		if want := "Your rss password was changed"; msgs[0].Subject != want {
			t.Errorf("Subject = %q, want %q", msgs[0].Subject, want)
		}
		if want := "Hi Ada Lovelace,"; !strings.Contains(msgs[0].Text, want) {
			t.Errorf("Text = %q, want it to contain %q", msgs[0].Text, want)
		}
		if want := "Your password was changed. Other devices were logged out."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("email fails to queue", func(t *testing.T) {
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				changePasswordFn: func(context.Context, string, string, string, string) error { return nil },
			},
			EmailQueue: &fakeEmailQueue{err: errors.New("boom")},
		})

		rr, flash := postSettingsPassword(t, s, current, next)

		assertRedirect(t, rr, "/settings")
		if want := "Your password was changed. Other devices were logged out."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				changePasswordFn: func(context.Context, string, string, string, string) error {
					return errors.New("boom")
				},
			},
			EmailQueue: &fakeEmailQueue{},
		})

		rr, _ := postSettingsPassword(t, s, current, next)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		calls := 0
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				changePasswordFn: func(context.Context, string, string, string, string) error {
					calls++
					return rss.ErrWrongPassword
				},
			},
			EmailQueue: &fakeEmailQueue{},
		})

		for i := range 10 {
			if rr, _ := postSettingsPassword(t, s, current, next); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d: status = %d, want %d", i+1, rr.Code, http.StatusUnprocessableEntity)
			}
		}
		rr, _ := postSettingsPassword(t, s, current, next)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
		if secs, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || secs < 1 {
			t.Errorf("Retry-After = %q, want a positive number of seconds", rr.Header().Get("Retry-After"))
		}
		assertBodyContains(t, rr.Body.String(), `<div class="error" role="alert">Too many attempts. Try again in 10 minutes.</div>`)
		if calls != 10 {
			t.Errorf("ChangePassword called %d times, want 10", calls)
		}
	})
}

func TestLogOutOthers(t *testing.T) {
	tests := []struct {
		name      string
		deleted   int64
		wantFlash string
	}{
		{"some deleted", 2, "You're logged out on every other device."},
		{"none deleted", 0, "There were no other devices to log out."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotUserID, gotKeepID string
			s := newTestServerWith(t, Services{SessionService: &fakeSessionStore{
				deleteOthersFn: func(ctx context.Context, userID, keepID string) (int64, error) {
					gotUserID, gotKeepID = userID, keepID
					return tt.deleted, nil
				},
			}})

			req := httptest.NewRequest(http.MethodPost, "/settings/log-out-others", nil)
			req = req.WithContext(requestWithSession(settingsSession).Context())
			rr := httptest.NewRecorder()
			var flash string
			s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.logOutOthersHandler(w, r)
				flash = s.sessionManager.GetString(r.Context(), "flash")
			})).ServeHTTP(rr, req)

			assertRedirect(t, rr, "/settings")
			if gotUserID != settingsSession.User.ID || gotKeepID != settingsSession.ID {
				t.Errorf("DeleteOthers(%q, %q), want (%q, %q)", gotUserID, gotKeepID, settingsSession.User.ID, settingsSession.ID)
			}
			if flash != tt.wantFlash {
				t.Errorf("flash = %q, want %q", flash, tt.wantFlash)
			}
		})
	}
}

func TestChangePasswordWellKnown(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/change-password", nil)
	rr := httptest.NewRecorder()

	s.router().ServeHTTP(rr, req)

	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/settings/password" {
		t.Errorf("got %d %q, want 302 %q", rr.Code, rr.Header().Get("Location"), "/settings/password")
	}
}
