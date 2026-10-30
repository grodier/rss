package server

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/opml"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
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
			`<a class="settings-row" href="/settings/email">`,
			`<form class="settings-devices" action="/settings/log-out-others" method="post">`,
			`<a class="settings-row" href="/settings/import" aria-describedby="import-hint">`,
			"Bring your feeds from another reader with an OPML file.",
			`<a class="settings-row" href="/settings/export"`,
			"An OPML file you can import into another reader.",
			`<form class="settings-logout" action="/logout" method="post">`,
			`<a href="/settings/delete">Delete account</a>`,
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

// postSettingsTheme posts theme to POST /settings/theme as sess. It returns
// the response and the flash message left in the session.
func postSettingsTheme(t *testing.T, s *Server, sess rss.Session, theme string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	form := url.Values{"theme": {theme}}
	req := httptest.NewRequest(http.MethodPost, "/settings/theme", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(requestWithSession(sess).Context())
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.settingsThemeFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func TestSettingsTheme(t *testing.T) {
	for _, theme := range []string{rss.ThemeSystem, rss.ThemeLight, rss.ThemeDark} {
		t.Run("GET checks "+theme, func(t *testing.T) {
			s := newTestServer(t)
			sess := settingsSession
			sess.User.Theme = theme
			rr := httptest.NewRecorder()
			s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsThemeHandler)).ServeHTTP(rr, requestWithSession(sess))

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()
			if got := strings.Count(body, " checked"); got != 1 {
				t.Errorf("%d checked choices, want 1", got)
			}
			assertBodyContains(t, body, `value="`+theme+`" checked`, `<a class="btn" href="/settings">Cancel</a>`)
			if strings.Contains(body, "autofocus") {
				t.Error("the form has autofocus without an error")
			}
		})
	}

	for _, theme := range []string{rss.ThemeSystem, rss.ThemeLight, rss.ThemeDark} {
		t.Run("POST saves "+theme, func(t *testing.T) {
			var gotID, gotTheme string
			s := newTestServerWith(t, Services{UserService: &fakeUserStore{
				updateThemeFn: func(ctx context.Context, id, theme string) error {
					gotID, gotTheme = id, theme
					return nil
				},
			}})

			rr, flash := postSettingsTheme(t, s, settingsSession, theme)

			assertRedirect(t, rr, "/settings")
			if gotID != settingsSession.User.ID || gotTheme != theme {
				t.Errorf("UpdateTheme(%q, %q), want (%q, %q)", gotID, gotTheme, settingsSession.User.ID, theme)
			}
			if want := "Your theme was updated."; flash != want {
				t.Errorf("flash = %q, want %q", flash, want)
			}
		})
	}

	for _, theme := range []string{"", "sepia", "Dark"} {
		t.Run(fmt.Sprintf("POST %q is rejected", theme), func(t *testing.T) {
			calls := 0
			s := newTestServerWith(t, Services{UserService: &fakeUserStore{
				updateThemeFn: func(context.Context, string, string) error {
					calls++
					return nil
				},
			}})

			rr, flash := postSettingsTheme(t, s, settingsSession, theme)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			assertBodyContains(t, rr.Body.String(),
				`<p class="error" id="theme-error">Choose System, Light or Dark.</p>`,
				`aria-describedby="theme-hint theme-error"`,
				`aria-invalid="true"`,
				"autofocus",
			)
			if calls != 0 {
				t.Errorf("UpdateTheme called %d times, want 0", calls)
			}
			if flash != "" {
				t.Errorf("flash = %q, want none", flash)
			}
		})
	}

	t.Run("POST saves for the logged-in user only", func(t *testing.T) {
		other := rss.Session{ID: "123e4567-e89b-12d3-a456-426614174098", User: rss.User{ID: "123e4567-e89b-12d3-a456-426614174002", Name: "Grace"}}
		var gotIDs []string
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateThemeFn: func(ctx context.Context, id, theme string) error {
				gotIDs = append(gotIDs, id)
				return nil
			},
		}})

		postSettingsTheme(t, s, other, rss.ThemeDark)

		if want := []string{other.User.ID}; !reflect.DeepEqual(gotIDs, want) {
			t.Errorf("UpdateTheme ids = %q, want %q", gotIDs, want)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateThemeFn: func(context.Context, string, string) error {
				return errors.New("boom")
			},
		}})

		rr, flash := postSettingsTheme(t, s, settingsSession, rss.ThemeDark)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
		if flash != "" {
			t.Errorf("flash = %q, want none", flash)
		}
	})

	t.Run("Settings shows the current choice", func(t *testing.T) {
		for theme, want := range map[string]string{rss.ThemeSystem: "System", rss.ThemeLight: "Light", rss.ThemeDark: "Dark"} {
			s := newTestServer(t)
			sess := settingsSession
			sess.User.Theme = theme
			rr := httptest.NewRecorder()
			s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(sess))

			assertBodyContains(t, rr.Body.String(), `<span class="settings-value">`+want+`</span>`)
		}
	})
}

func TestThemeAttribute(t *testing.T) {
	const systemColors = `<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)" />
  <meta name="theme-color" content="#0f1626" media="(prefers-color-scheme: dark)" />`
	tests := []struct {
		theme      string
		want       string // the <html> tag
		themeColor string // the theme-color metas
	}{
		{rss.ThemeDark, `<html lang="en" data-theme="dark">`, `<meta name="theme-color" content="#0f1626" />`},
		{rss.ThemeLight, `<html lang="en" data-theme="light">`, `<meta name="theme-color" content="#ffffff" />`},
		{rss.ThemeSystem, `<html lang="en">`, systemColors},
	}
	for _, tt := range tests {
		t.Run(tt.theme, func(t *testing.T) {
			s := newTestServer(t)
			sess := settingsSession
			sess.User.Theme = tt.theme
			rr := httptest.NewRecorder()
			s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(sess))

			body := rr.Body.String()
			assertBodyContains(t, body, tt.want, tt.themeColor)
			if got := strings.Count(body, `name="theme-color"`); got != strings.Count(tt.themeColor, `name="theme-color"`) {
				t.Errorf("%d theme-color metas, want only %q", got, tt.themeColor)
			}
		})
	}

	t.Run("logged-out page follows the system", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		assertBodyContains(t, body, `<html lang="en">`, systemColors)
		if strings.Contains(body, "data-theme") {
			t.Error("the login page has a data-theme")
		}
	})

	// Every logged-in page must carry the user's theme, or it would show
	// the system's.
	t.Run("every logged-in page has the attribute", func(t *testing.T) {
		files, err := fs.Glob(ui.Templates, "templates/*.html")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			b, err := fs.ReadFile(ui.Templates, f)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			if !strings.Contains(src, `{{template "app-nav" .Nav}}`) {
				continue
			}
			if !strings.Contains(src, `<html lang="en"{{template "html-theme" .Nav}}>`) {
				t.Errorf("%s has the app nav but not the html-theme attribute", f)
			}
			if !strings.Contains(src, `{{template "app-head" .Nav}}`) {
				t.Errorf("%s has the app nav but doesn't pass .Nav to app-head, so its theme-color ignores the user's theme", f)
			}
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
		if want := "Your Paper Round password was changed"; msgs[0].Subject != want {
			t.Errorf("Subject = %q, want %q", msgs[0].Subject, want)
		}
		for _, want := range []string{"Hi Ada Lovelace,", "reset your password now (/password/forgot)"} {
			if !strings.Contains(msgs[0].Text, want) {
				t.Errorf("Text = %q, want it to contain %q", msgs[0].Text, want)
			}
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

// getOPML requests the OPML export as settingsSession from a server whose
// subscriptions come from listByUser.
func getOPML(t *testing.T, listByUser func(context.Context, string) ([]rss.Subscription, error)) *httptest.ResponseRecorder {
	t.Helper()
	return getOPMLQuery(t, "", listByUser)
}

// getOPMLQuery is getOPML with query appended to the export's URL.
func getOPMLQuery(t *testing.T, query string, listByUser func(context.Context, string) ([]rss.Subscription, error)) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestServerWith(t, Services{SubscriptionService: &fakeSubscriptionStore{listByUserFn: listByUser}})
	rr := httptest.NewRecorder()
	req := requestWithSession(settingsSession)
	req.URL.RawQuery = query
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.exportOPMLHandler)).ServeHTTP(rr, req)
	return rr
}

func TestSettingsExportHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsExportHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	assertBodyContains(t, rr.Body.String(),
		`<form class="settings-form" action="/settings/subscriptions.opml" method="get">`,
		`<input type="radio" name="tags" value="folders" checked> Turn each tag into a folder`,
		`<input type="radio" name="tags" value="ignore"> Ignore tags`,
	)
}

// parseOPML checks the export's status and headers and decodes its body.
func parseOPML(t *testing.T, rr *httptest.ResponseRecorder) opmlDoc {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got, want := rr.Header().Get("Content-Type"), "text/x-opml; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	// The date is UTC; allow for the request straddling midnight.
	var dates []string
	for _, d := range []time.Time{time.Now().UTC(), time.Now().UTC().Add(-time.Minute)} {
		dates = append(dates, `attachment; filename="paper-round-subscriptions-`+d.Format(time.DateOnly)+`.opml"`)
	}
	if got := rr.Header().Get("Content-Disposition"); got != dates[0] && got != dates[1] {
		t.Errorf("Content-Disposition = %q, want %q", got, dates[0])
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, xml.Header) {
		t.Errorf("body doesn't start with the XML header: %q", body)
	}

	var doc opmlDoc
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parsing the body: %v\n%s", err, body)
	}
	if doc.Version != "2.0" {
		t.Errorf("version = %q, want %q", doc.Version, "2.0")
	}
	if doc.Head.Title != "Paper Round subscriptions" {
		t.Errorf("head title = %q, want %q", doc.Head.Title, "Paper Round subscriptions")
	}
	if _, err := time.Parse(time.RFC1123Z, doc.Head.DateCreated); err != nil {
		t.Errorf("dateCreated %q isn't RFC 1123: %v", doc.Head.DateCreated, err)
	}
	return doc
}

func TestExportOPML(t *testing.T) {
	t.Run("subscriptions", func(t *testing.T) {
		const title = `Tom & Jerry's <"Best"> Feed`
		subs := []rss.Subscription{
			{
				Feed: rss.Feed{ID: "feed-1", Url: "https://a.example/feed.xml"},
				Site: rss.Site{URL: "https://a.example/"},
			},
			{
				Feed: rss.Feed{ID: "feed-2", Url: "https://b.example/rss?x=1&y=2", SiteUrl: "https://b.example/blog", Title: title, GoneAt: time.Now()},
				Site: rss.Site{URL: "https://b.example/"},
			},
			{
				Feed:  rss.Feed{ID: "feed-3", Url: "https://c.example/feed", Title: "C's own title"},
				Site:  rss.Site{URL: "https://c.example/"},
				Title: "My name for C",
			},
		}
		var gotUserID string
		rr := getOPML(t, func(ctx context.Context, userID string) ([]rss.Subscription, error) {
			gotUserID = userID
			return subs, nil
		})

		doc := parseOPML(t, rr)
		if gotUserID != settingsSession.User.ID {
			t.Errorf("ListByUser(%q), want %q", gotUserID, settingsSession.User.ID)
		}
		want := []opmlOutline{
			{Type: "rss", Text: "https://a.example/feed.xml", XMLURL: "https://a.example/feed.xml", HTMLURL: "https://a.example/"},
			{Type: "rss", Text: title, Title: title, XMLURL: "https://b.example/rss?x=1&y=2", HTMLURL: "https://b.example/blog"},
			{Type: "rss", Text: "My name for C", Title: "My name for C", XMLURL: "https://c.example/feed", HTMLURL: "https://c.example/"},
		}
		if !reflect.DeepEqual(doc.Body, want) {
			t.Errorf("outlines = %+v, want %+v", doc.Body, want)
		}
	})

	ai := rss.Tag{ID: "tag-ai", Name: "AI"}
	news := rss.Tag{ID: "tag-news", Name: "news"}
	tagged := []rss.Subscription{
		{Feed: rss.Feed{ID: "feed-1", Url: "https://a.example/feed", Title: "A"}, Tags: []rss.Tag{news}},
		{Feed: rss.Feed{ID: "feed-2", Url: "https://b.example/feed", Title: "B"}},
		{Feed: rss.Feed{ID: "feed-3", Url: "https://c.example/feed", Title: "C"}, Tags: []rss.Tag{ai, news}},
		{Feed: rss.Feed{ID: "feed-4", Url: "https://d.example/feed", Title: "D"}, Tags: []rss.Tag{ai}},
		{Feed: rss.Feed{ID: "feed-5", Url: "https://e.example/feed", Title: "E"}},
	}
	listTagged := func(context.Context, string) ([]rss.Subscription, error) { return tagged, nil }
	feed := func(name, host string) opmlOutline {
		return opmlOutline{Type: "rss", Text: name, Title: name, XMLURL: "https://" + host + "/feed"}
	}

	t.Run("a folder per tag, then the untagged feeds", func(t *testing.T) {
		for _, query := range []string{"", "tags=folders"} {
			rr := getOPMLQuery(t, query, listTagged)

			doc := parseOPML(t, rr)
			want := []opmlOutline{
				{Text: "AI", Title: "AI", Outlines: []opmlOutline{feed("C", "c.example"), feed("D", "d.example")}},
				{Text: "news", Title: "news", Outlines: []opmlOutline{feed("A", "a.example"), feed("C", "c.example")}},
				feed("B", "b.example"),
				feed("E", "e.example"),
			}
			if !reflect.DeepEqual(doc.Body, want) {
				t.Errorf("%q: outlines = %+v, want %+v", query, doc.Body, want)
			}
			// Folders have no type or xmlUrl.
			assertBodyContains(t, rr.Body.String(), `<outline text="AI" title="AI">`, `<outline text="news" title="news">`)
		}
	})

	t.Run("tags=ignore lists every feed once, flat", func(t *testing.T) {
		doc := parseOPML(t, getOPMLQuery(t, "tags=ignore", listTagged))
		want := []opmlOutline{
			feed("A", "a.example"), feed("B", "b.example"), feed("C", "c.example"), feed("D", "d.example"), feed("E", "e.example"),
		}
		if !reflect.DeepEqual(doc.Body, want) {
			t.Errorf("outlines = %+v, want %+v", doc.Body, want)
		}
	})

	t.Run("round trip through opml.Parse", func(t *testing.T) {
		outlines, err := opml.Parse(getOPMLQuery(t, "", listTagged).Body)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		want := []opml.Outline{
			{URL: "https://c.example/feed", Title: "C", Folders: []string{"AI", "news"}},
			{URL: "https://d.example/feed", Title: "D", Folders: []string{"AI"}},
			{URL: "https://a.example/feed", Title: "A", Folders: []string{"news"}},
			{URL: "https://b.example/feed", Title: "B"},
			{URL: "https://e.example/feed", Title: "E"},
		}
		if !reflect.DeepEqual(outlines, want) {
			t.Errorf("Parse = %+v, want %+v", outlines, want)
		}
	})

	t.Run("no subscriptions", func(t *testing.T) {
		rr := getOPML(t, func(context.Context, string) ([]rss.Subscription, error) {
			return nil, nil
		})

		doc := parseOPML(t, rr)
		if len(doc.Body) != 0 {
			t.Errorf("got %d outlines, want none", len(doc.Body))
		}
		assertBodyContains(t, rr.Body.String(), "<body></body>")
	})

	t.Run("store error", func(t *testing.T) {
		rr := getOPML(t, func(context.Context, string) ([]rss.Subscription, error) {
			return nil, errors.New("boom")
		})

		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
		if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q, want HTML", got)
		}
		if got := rr.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("Content-Disposition = %q, want none", got)
		}
	})
}

func TestSettingsDeleteHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsDeleteHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		"<h1>Delete your account</h1>",
		"This permanently deletes your account, your subscriptions and your reading history. Feeds and articles stay, because other readers share them. You can't undo this.",
		`Want to keep your subscriptions? <a href="/settings/subscriptions.opml" download>Export them first</a>.`,
		`<input type="email" id="email" value="ada@example.com" autocomplete="username" readonly />`,
		`<input type="password" id="password" name="password" autocomplete="current-password"`,
		`<button class="btn-danger" type="submit">Delete my account</button>`,
		`<a class="rail-account" href="/settings" aria-current="page">`,
	)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}
}

// postSettingsDelete posts password to POST /settings/delete as
// settingsSession. It returns the response, the flash message left in the
// session and whether the session still holds a sessionID.
func postSettingsDelete(t *testing.T, s *Server, password string) (rr *httptest.ResponseRecorder, flash string, loggedIn bool) {
	t.Helper()
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/settings/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(requestWithSession(settingsSession).Context())
	rr = httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.sessionManager.Put(r.Context(), "sessionID", settingsSession.ID)
		s.settingsDeleteFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
		loggedIn = s.sessionManager.Exists(r.Context(), "sessionID")
	})).ServeHTTP(rr, req)
	return rr, flash, loggedIn
}

func TestSettingsDeleteForm(t *testing.T) {
	const password = "correct-horse-battery"
	const wantFlash = "Your account was deleted. Thanks for trying Paper Round."

	tests := []struct {
		name      string
		password  string
		storeErr  error
		wantError string
	}{
		{"blank password", "", nil, "Enter your password."},
		{"wrong password", password, rss.ErrWrongPassword, "That password isn&#39;t right."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &fakeEmailQueue{}
			s := newTestServerWith(t, Services{
				UserService: &fakeUserStore{
					deleteFn: func(context.Context, string, string) (rss.User, error) {
						if tt.storeErr == nil {
							t.Error("Delete must not be called")
						}
						return rss.User{}, tt.storeErr
					},
				},
				EmailQueue: queue,
			})

			rr, flash, loggedIn := postSettingsDelete(t, s, tt.password)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			body := rr.Body.String()
			assertBodyContains(t, body,
				`<p class="error" id="password-error">`+tt.wantError+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
			if tt.password != "" && strings.Contains(body, tt.password) {
				t.Errorf("the page echoes the password back")
			}
			if n := len(queue.messages()); n != 0 {
				t.Errorf("queued %d emails, want 0", n)
			}
			if flash != "" {
				t.Errorf("flash = %q, want none", flash)
			}
			if !loggedIn {
				t.Error("the session lost its sessionID")
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		var gotUserID, gotPassword string
		queue := &fakeEmailQueue{}
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				deleteFn: func(ctx context.Context, userID, pw string) (rss.User, error) {
					gotUserID, gotPassword = userID, pw
					return settingsSession.User, nil
				},
			},
			EmailQueue: queue,
		})

		rr, flash, loggedIn := postSettingsDelete(t, s, password)

		assertRedirect(t, rr, "/")
		if gotUserID != settingsSession.User.ID || gotPassword != password {
			t.Errorf("Delete(%q, %q), want (%q, %q)", gotUserID, gotPassword, settingsSession.User.ID, password)
		}
		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		if msgs[0].To != settingsSession.User.Email {
			t.Errorf("To = %q, want %q", msgs[0].To, settingsSession.User.Email)
		}
		if want := "Your Paper Round account was deleted"; msgs[0].Subject != want {
			t.Errorf("Subject = %q, want %q", msgs[0].Subject, want)
		}
		if want := "Hi Ada Lovelace,"; !strings.Contains(msgs[0].Text, want) {
			t.Errorf("Text = %q, want it to contain %q", msgs[0].Text, want)
		}
		if flash != wantFlash {
			t.Errorf("flash = %q, want %q", flash, wantFlash)
		}
		if loggedIn {
			t.Error("the session still holds a sessionID")
		}
	})

	t.Run("email fails to queue", func(t *testing.T) {
		deleted := false
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				deleteFn: func(context.Context, string, string) (rss.User, error) {
					deleted = true
					return settingsSession.User, nil
				},
			},
			EmailQueue: &fakeEmailQueue{err: errors.New("boom")},
		})

		rr, flash, loggedIn := postSettingsDelete(t, s, password)

		assertRedirect(t, rr, "/")
		if !deleted {
			t.Error("Delete was not called")
		}
		if flash != wantFlash {
			t.Errorf("flash = %q, want %q", flash, wantFlash)
		}
		if loggedIn {
			t.Error("the session still holds a sessionID")
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				deleteFn: func(context.Context, string, string) (rss.User, error) {
					return rss.User{}, errors.New("boom")
				},
			},
			EmailQueue: &fakeEmailQueue{},
		})

		rr, _, _ := postSettingsDelete(t, s, password)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		calls := 0
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{
				deleteFn: func(context.Context, string, string) (rss.User, error) {
					calls++
					return rss.User{}, rss.ErrWrongPassword
				},
			},
			EmailQueue: &fakeEmailQueue{},
		})

		for i := range 10 {
			if rr, _, _ := postSettingsDelete(t, s, password); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d: status = %d, want %d", i+1, rr.Code, http.StatusUnprocessableEntity)
			}
		}
		rr, _, _ := postSettingsDelete(t, s, password)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
		if secs, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || secs < 1 {
			t.Errorf("Retry-After = %q, want a positive number of seconds", rr.Header().Get("Retry-After"))
		}
		assertBodyContains(t, rr.Body.String(), `<div class="error" role="alert">Too many attempts. Try again in 10 minutes.</div>`)
		if calls != 10 {
			t.Errorf("Delete called %d times, want 10", calls)
		}
	})
}

// TestDeleteAccountLogsOut logs in, deletes the account and checks that the
// next request through the same cookie jar is anonymous and shows the flash on
// the home page.
func TestDeleteAccountLogsOut(t *testing.T) {
	user := rss.User{ID: "user-1", Name: "Ada", Email: "ada@example.com"}
	deleted := false
	s := newTestServerWith(t, Services{
		UserService: &fakeUserStore{
			authenticateFn: func(context.Context, string, string) (string, error) {
				return user.ID, nil
			},
			deleteFn: func(context.Context, string, string) (rss.User, error) {
				deleted = true
				return user, nil
			},
		},
		SessionService: &fakeSessionStore{
			createFn: func(context.Context, string, time.Time) (string, error) {
				return testSessionID, nil
			},
			getFn: func(context.Context, string) (rss.Session, error) {
				// The cascade deletes the user_sessions row with the user.
				if deleted {
					return rss.Session{}, rss.ErrNoRecord
				}
				return rss.Session{ID: testSessionID, User: user, LastSeenAt: time.Now()}, nil
			},
		},
		EmailQueue: &fakeEmailQueue{},
	})
	ts := httptest.NewServer(s.router())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	post := func(target string, form url.Values) {
		t.Helper()
		resp, err := c.PostForm(ts.URL+target, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("POST %s: status = %d, want %d", target, resp.StatusCode, http.StatusSeeOther)
		}
	}

	post("/login", url.Values{"email": {user.Email}, "password": {"correct-horse-battery"}})
	post("/settings/delete", url.Values{"password": {"correct-horse-battery"}})

	resp, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertBodyContains(t, string(b),
		`<a class="btn btn-primary" href="/login">Log in</a>`,
		`<div class="flash">Your account was deleted. Thanks for trying Paper Round.</div>`,
	)
}
