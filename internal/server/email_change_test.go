package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// changeEmailLinkRX finds the token in a change-email email's link.
var changeEmailLinkRX = regexp.MustCompile(`https://rss\.example\.com/email-change\?token=([A-Za-z0-9_-]{43})`)

const changeEmailPassword = "correct-horse-battery"

// newChangeEmailTestServer returns a server whose CheckPassword accepts only
// changeEmailPassword and whose GetByEmail knows only taken@example.com, with
// its token store and email queue.
func newChangeEmailTestServer(t *testing.T) (*Server, *fakeTokenStore, *fakeEmailQueue) {
	t.Helper()
	tokens, queue := &fakeTokenStore{}, &fakeEmailQueue{}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com"}, Services{
		UserService: &fakeUserStore{
			checkPasswordFn: func(ctx context.Context, userID, password string) error {
				if userID != settingsSession.User.ID {
					t.Errorf("CheckPassword user = %q, want %q", userID, settingsSession.User.ID)
				}
				if password != changeEmailPassword {
					return rss.ErrWrongPassword
				}
				return nil
			},
			getByEmailFn: func(ctx context.Context, email string) (rss.User, error) {
				if strings.EqualFold(email, "taken@example.com") {
					return rss.User{ID: "other-user", Email: "taken@example.com"}, nil
				}
				return rss.User{}, rss.ErrNoRecord
			},
		},
		TokenService: tokens,
		EmailQueue:   queue,
	})
	return s, tokens, queue
}

func TestSettingsEmailHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsEmailHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		"<h1>Change email</h1>",
		"Your email address is ada@example.com.",
		`<form class="settings-form" action="/settings/email" method="post">`,
		`<input type="email" id="email" name="email" value=""`,
		`autocomplete="email" autocapitalize="none" spellcheck="false"`,
		`<input type="password" id="password" name="password" autocomplete="current-password"`,
		"We'll send a link to your new address. Your email won't change until you open it.",
		`<button class="btn-primary" type="submit">Send link</button>`,
		`<a class="rail-account" href="/settings" aria-current="page">`,
	)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}
}

// postSettingsEmail posts email and password to POST /settings/email as
// settingsSession. It returns the response and the flash message left in the
// session.
func postSettingsEmail(t *testing.T, s *Server, email, password string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := postFormRequest("/settings/email", url.Values{"email": {email}, "password": {password}})
	return serveConfirm(t, s, s.settingsEmailFormHandler, req, &settingsSession)
}

func TestSettingsEmailForm(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		password  string
		field     string
		wantError string
	}{
		{"the same address", "Ada@Example.com", changeEmailPassword, "email", "That&#39;s already your email address."},
		{"blank address", "", changeEmailPassword, "email", "Email is required"},
		{"invalid address", "not-an-email", changeEmailPassword, "email", "Email must be a valid email address"},
		{"blank password", "new@example.com", "", "password", "Enter your password."},
		{"wrong password", "new@example.com", "wrong-password", "password", "That password isn&#39;t right."},
		{"taken address", "Taken@example.com", changeEmailPassword, "email", "There&#39;s already an account with this email."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, tokens, queue := newChangeEmailTestServer(t)
			rr, flash := postSettingsEmail(t, s, tt.email, tt.password)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			body := rr.Body.String()
			assertBodyContains(t, body,
				`<p class="error" id="`+tt.field+`-error">`+tt.wantError+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
			if tt.password != "" && strings.Contains(body, tt.password) {
				t.Error("the page echoes the password back")
			}
			if len(tokens.created) != 0 || len(queue.messages()) != 0 {
				t.Errorf("created %d tokens and queued %d emails, want none", len(tokens.created), len(queue.messages()))
			}
			if flash != "" {
				t.Errorf("flash = %q, want none", flash)
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		s, tokens, queue := newChangeEmailTestServer(t)
		rr, flash := postSettingsEmail(t, s, "new@example.com", changeEmailPassword)

		assertRedirect(t, rr, "/settings")
		if want := "We sent a link to new@example.com. Open it to finish changing your email."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		m := msgs[0]
		if m.To != "new@example.com" {
			t.Errorf("To = %q, want new@example.com", m.To)
		}
		if want := "Confirm your new email address for rss"; m.Subject != want {
			t.Errorf("Subject = %q, want %q", m.Subject, want)
		}
		match := changeEmailLinkRX.FindStringSubmatch(m.Text)
		if match == nil {
			t.Fatalf("Text has no change-email link: %q", m.Text)
		}
		if len(tokens.created) != 1 {
			t.Fatalf("created %d tokens, want 1", len(tokens.created))
		}
		tok := tokens.created[0]
		if tok.Purpose != rss.TokenChangeEmail || tok.Email != "new@example.com" || tok.UserID != settingsSession.User.ID {
			t.Errorf("token = %+v, want change_email for new@example.com and %s", tok, settingsSession.User.ID)
		}
		if !bytes.Equal(tok.Hash, token.Hash(match[1])) {
			t.Error("the stored hash isn't the link's token's")
		}
		if d := time.Until(tok.ExpiresAt); d < 23*time.Hour || d > 24*time.Hour {
			t.Errorf("ExpiresAt is %v from now, want 24 hours", d)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		s, _, queue := newChangeEmailTestServer(t)
		for range 5 {
			postSettingsEmail(t, s, "new@example.com", changeEmailPassword)
		}
		rr, _ := postSettingsEmail(t, s, "new@example.com", changeEmailPassword)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
		assertBodyContains(t, rr.Body.String(), "Too many attempts. Try again in")
		if n := len(queue.messages()); n != 5 {
			t.Errorf("queued %d emails, want 5", n)
		}
	})
}

func TestEmailChangeHandler(t *testing.T) {
	const plain = "valid-token"
	s := newTestServerWith(t, Services{TokenService: &fakeTokenStore{
		getFn: func(ctx context.Context, hash []byte, purpose string) (rss.Token, error) {
			if purpose != rss.TokenChangeEmail {
				t.Errorf("purpose = %q, want %q", purpose, rss.TokenChangeEmail)
			}
			if !bytes.Equal(hash, token.Hash(plain)) {
				return rss.Token{}, rss.ErrNoRecord
			}
			return rss.Token{UserID: "user-1", Purpose: purpose, Email: "new@example.com"}, nil
		},
	}})

	t.Run("valid", func(t *testing.T) {
		rr, _ := serveConfirm(t, s, s.emailChangeHandler, httptest.NewRequest(http.MethodGet, "/email-change?token="+plain, nil), nil)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		assertBodyContains(t, rr.Body.String(),
			"<h1>Change your email address</h1>",
			"Make new@example.com the email for your rss account?",
			`<form action="/email-change" method="post">`,
			`<input type="hidden" name="token" value="valid-token" />`,
			"Change email address</button>",
		)
	})

	t.Run("not valid", func(t *testing.T) {
		rr, _ := serveConfirm(t, s, s.emailChangeHandler, httptest.NewRequest(http.MethodGet, "/email-change?token=used-token", nil), nil)

		if rr.Code != http.StatusGone {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
		}
		if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
		assertBodyContains(t, rr.Body.String(),
			"<h1>This link has expired</h1>",
			"Links to change your email work for 24 hours and only once.",
			`<a class="btn" href="/settings/email">Try again</a>`,
		)
	})

	t.Run("works without login", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/email-change?token="+plain, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})
}

func TestEmailChangeFormHandler(t *testing.T) {
	const plain = "valid-token"
	old := rss.User{ID: "user-1", Name: "Ada Lovelace", Email: "ada@example.com"}
	newServer := func(t *testing.T, err error) (*Server, *fakeEmailQueue) {
		t.Helper()
		queue := &fakeEmailQueue{}
		s := newTestServerWith(t, Services{
			UserService: &fakeUserStore{changeEmailFn: func(ctx context.Context, hash []byte) (rss.User, string, error) {
				if !bytes.Equal(hash, token.Hash(plain)) {
					return rss.User{}, "", rss.ErrNoRecord
				}
				return old, "new@example.com", err
			}},
			EmailQueue: queue,
		})
		return s, queue
	}
	post := func(t *testing.T, s *Server, plain string, sess *rss.Session) (*httptest.ResponseRecorder, string) {
		t.Helper()
		return serveConfirm(t, s, s.emailChangeFormHandler, postFormRequest("/email-change", url.Values{"token": {plain}}), sess)
	}

	tests := []struct {
		name      string
		sess      *rss.Session
		wantPath  string
		wantFlash string
	}{
		{"logged in", &confirmedSession, "/settings", "Your email address is now new@example.com."},
		{"anonymous", nil, "/login", "Your email address is now new@example.com. Log in with it to continue."},
	}
	for _, tt := range tests {
		t.Run("valid, "+tt.name, func(t *testing.T) {
			s, queue := newServer(t, nil)
			rr, flash := post(t, s, plain, tt.sess)

			assertRedirect(t, rr, tt.wantPath)
			if flash != tt.wantFlash {
				t.Errorf("flash = %q, want %q", flash, tt.wantFlash)
			}
			msgs := queue.messages()
			if len(msgs) != 1 {
				t.Fatalf("queued %d emails, want 1", len(msgs))
			}
			if msgs[0].To != "ada@example.com" {
				t.Errorf("To = %q, want the old address", msgs[0].To)
			}
			if want := "Your rss email address was changed"; msgs[0].Subject != want {
				t.Errorf("Subject = %q, want %q", msgs[0].Subject, want)
			}
			if want := "changed from ada@example.com to new@example.com."; !strings.Contains(msgs[0].Text, want) {
				t.Errorf("Text = %q, want it to contain %q", msgs[0].Text, want)
			}
		})
	}

	t.Run("not valid", func(t *testing.T) {
		s, queue := newServer(t, nil)
		rr, flash := post(t, s, "used-token", nil)

		if rr.Code != http.StatusGone {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
		}
		assertBodyContains(t, rr.Body.String(), "Links to change your email work for 24 hours and only once.")
		if flash != "" || len(queue.messages()) != 0 {
			t.Errorf("flash = %q and %d emails, want none", flash, len(queue.messages()))
		}
	})

	t.Run("address taken in the meantime", func(t *testing.T) {
		s, queue := newServer(t, rss.ErrDuplicateEmail)
		rr, flash := post(t, s, plain, &confirmedSession)

		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusConflict)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>That address is taken</h1>",
			"Another account started using new@example.com in the meantime. Your email is still ada@example.com.",
		)
		if strings.Contains(body, `action="/email-change"`) {
			t.Error("the page still offers to change the email")
		}
		if flash != "" || len(queue.messages()) != 0 {
			t.Errorf("flash = %q and %d emails, want none", flash, len(queue.messages()))
		}
	})

	t.Run("notice fails to queue", func(t *testing.T) {
		s, _ := newServer(t, nil)
		s.services.EmailQueue = &fakeEmailQueue{err: errors.New("boom")}
		rr, _ := post(t, s, plain, nil)
		assertRedirect(t, rr, "/login")
	})

	t.Run("store error", func(t *testing.T) {
		s, _ := newServer(t, errors.New("boom"))
		rr, _ := post(t, s, plain, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}
