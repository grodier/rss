package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// resetLinkRX finds the token in a reset email's link.
var resetLinkRX = regexp.MustCompile(`https://rss\.example\.com/password/reset\?token=([A-Za-z0-9_-]{43})`)

var resetUser = rss.User{ID: "user-1", Name: "Ada Lovelace", Email: "ada@example.com"}

// newResetTestServer returns a server whose GetByEmail knows only resetUser,
// with its token store and email queue.
func newResetTestServer(t *testing.T) (*Server, *fakeTokenStore, *fakeEmailQueue) {
	t.Helper()
	tokens, queue := &fakeTokenStore{}, &fakeEmailQueue{}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com", SupportEmail: "help@example.com"}, Services{
		UserService: &fakeUserStore{getByEmailFn: func(ctx context.Context, email string) (rss.User, error) {
			if !strings.EqualFold(email, resetUser.Email) {
				return rss.User{}, rss.ErrNoRecord
			}
			return resetUser, nil
		}},
		TokenService: tokens,
		EmailQueue:   queue,
	})
	return s, tokens, queue
}

func TestForgotPasswordHandler(t *testing.T) {
	s, _, _ := newResetTestServer(t)
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/password/forgot?email="+url.QueryEscape(`a"<b@example.com`), nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		"Enter the email you signed up with and we'll send you a link to choose a new password.",
		`name="email" value="a&#34;&lt;b@example.com"`,
		`autocomplete="username"`,
		"Send reset link</button>",
		`Can't get into your email? Write to <a href="mailto:help@example.com">help@example.com</a>.`,
	)
	if strings.Contains(body, `a"<b`) {
		t.Errorf("body has the email unescaped: %s", body)
	}
}

func TestForgotPasswordFormHandler(t *testing.T) {
	post := func(t *testing.T, s *Server, ip, email string) *httptest.ResponseRecorder {
		t.Helper()
		h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.forgotPasswordFormHandler)).ServeHTTP
		return postForm(t, h, "/password/forgot", ip, url.Values{"email": {email}})
	}

	t.Run("unknown email", func(t *testing.T) {
		s, tokens, queue := newResetTestServer(t)
		rr := post(t, s, "192.0.2.1:1234", "nobody@example.com")

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		assertBodyContains(t, rr.Body.String(),
			`<p class="error" id="email-error">There&#39;s no account with this email.</p>`,
			`name="email" value="nobody@example.com"`,
		)
		if len(tokens.created) != 0 || len(queue.messages()) != 0 {
			t.Errorf("created %d tokens and queued %d emails, want none", len(tokens.created), len(queue.messages()))
		}
	})

	t.Run("not an email address", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			getByEmailFn: func(context.Context, string) (rss.User, error) {
				t.Error("GetByEmail must not be called")
				return rss.User{}, rss.ErrNoRecord
			},
		}})
		rr := post(t, s, "192.0.2.1:1234", "not-an-email")

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		assertBodyContains(t, rr.Body.String(), "Email must be a valid email address")
	})

	t.Run("known email", func(t *testing.T) {
		s, tokens, queue := newResetTestServer(t)
		h := s.router()

		form := url.Values{"email": {"Ada@Example.com"}}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, postFormRequest("/password/forgot", form))

		assertRedirect(t, rr, "/password/forgot/sent")
		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		m := msgs[0]
		if m.To != resetUser.Email {
			t.Errorf("To = %q, want %q", m.To, resetUser.Email)
		}
		if want := "Reset your rss password"; m.Subject != want {
			t.Errorf("Subject = %q, want %q", m.Subject, want)
		}
		match := resetLinkRX.FindStringSubmatch(m.Text)
		if match == nil {
			t.Fatalf("Text has no reset link: %q", m.Text)
		}

		if len(tokens.created) != 1 {
			t.Fatalf("created %d tokens, want 1", len(tokens.created))
		}
		tok := tokens.created[0]
		if !bytes.Equal(token.Hash(match[1]), tok.Hash) {
			t.Error("the link's token doesn't hash to the stored hash")
		}
		if tok.UserID != resetUser.ID || tok.Purpose != rss.TokenResetPassword || tok.Email != resetUser.Email {
			t.Errorf("token = %+v, want %s, %s, %s", tok, resetUser.ID, rss.TokenResetPassword, resetUser.Email)
		}
		if d := time.Until(tok.ExpiresAt) - time.Hour; d < -time.Minute || d > time.Minute {
			t.Errorf("ExpiresAt = %v, want about 1 hour from now", tok.ExpiresAt)
		}

		req := httptest.NewRequest(http.MethodGet, "/password/forgot/sent", nil)
		for _, c := range rr.Result().Cookies() {
			req.AddCookie(c)
		}
		sent := httptest.NewRecorder()
		h.ServeHTTP(sent, req)
		if sent.Code != http.StatusOK {
			t.Fatalf("sent page: status = %d, want %d", sent.Code, http.StatusOK)
		}
		assertBodyContains(t, sent.Body.String(),
			"<h1>Check your email</h1>",
			"We sent a link to ada@example.com. It works for 1 hour.",
			`<form class="alt" action="/password/forgot" method="post">`,
			`<input type="hidden" name="email" value="ada@example.com" />`,
			"send another link</button>",
			`<a href="mailto:help@example.com">help@example.com</a>`,
		)
	})

	t.Run("a fourth request for one email within the hour", func(t *testing.T) {
		s, _, queue := newResetTestServer(t)
		// From a new IP each time, so only the per-email limit counts.
		for i := range 3 {
			if rr := post(t, s, fmt.Sprintf("192.0.2.%d:1234", i+1), "ada@example.com"); rr.Code != http.StatusSeeOther {
				t.Fatalf("request %d: status = %d, want %d", i+1, rr.Code, http.StatusSeeOther)
			}
		}
		rr := post(t, s, "192.0.2.4:1234", "ADA@example.com")

		assertRateLimitedForm(t, rr, "ADA@example.com")
		if n := len(queue.messages()); n != 3 {
			t.Errorf("queued %d emails, want 3", n)
		}
	})

	t.Run("an eleventh request from one IP within the hour", func(t *testing.T) {
		s, _, _ := newResetTestServer(t)
		for i := range 10 {
			if rr := post(t, s, "192.0.2.1:1234", fmt.Sprintf("user%d@example.com", i)); rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("request %d: status = %d, want %d", i+1, rr.Code, http.StatusUnprocessableEntity)
			}
		}
		assertRateLimitedForm(t, post(t, s, "192.0.2.1:1234", "ada@example.com"), "ada@example.com")
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			getByEmailFn: func(context.Context, string) (rss.User, error) { return rss.User{}, errors.New("boom") },
		}})
		if rr := post(t, s, "192.0.2.1:1234", "ada@example.com"); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestForgotPasswordSentHandlerWithoutEmail(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/password/forgot/sent", nil))
	assertRedirect(t, rr, "/password/forgot")
}

// resetTokens is a token store whose only valid token is "valid-token", a
// reset_password token for resetUser.
func resetTokens(t *testing.T) *fakeTokenStore {
	return &fakeTokenStore{getFn: func(ctx context.Context, hash []byte, purpose string) (rss.Token, error) {
		if purpose != rss.TokenResetPassword {
			t.Errorf("purpose = %q, want %q", purpose, rss.TokenResetPassword)
		}
		if !bytes.Equal(hash, token.Hash("valid-token")) {
			return rss.Token{}, rss.ErrNoRecord
		}
		return rss.Token{UserID: resetUser.ID, Purpose: purpose, Email: resetUser.Email}, nil
	}}
}

func assertResetLinkExpired(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
	}
	assertBodyContains(t, rr.Body.String(),
		"<h1>This link has expired</h1>",
		"Reset links work for 1 hour and only once.",
		`<a class="btn" href="/password/forgot">Send a new link</a>`,
	)
}

func TestResetPasswordHandler(t *testing.T) {
	s := newTestServerWith(t, Services{TokenService: resetTokens(t)})
	get := func(plain string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/password/reset?token="+plain, nil))
		return rr
	}

	t.Run("valid", func(t *testing.T) {
		rr := get("valid-token")

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
			"<h1>Choose a new password</h1>",
			`<input type="email" id="email" value="ada@example.com" autocomplete="username" readonly />`,
			`<input type="hidden" name="token" value="valid-token" />`,
			`autocomplete="new-password"`,
			`<p class="hint" id="password-hint">At least 8 characters.</p>`,
			"Set new password</button>",
		)
	})

	t.Run("not valid", func(t *testing.T) {
		rr := get("used-token")
		assertResetLinkExpired(t, rr)
		if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
	})
}

func TestResetPasswordFormHandler(t *testing.T) {
	const newPassword = "staple-tuna-lamp"

	type result struct {
		rr       *httptest.ResponseRecorder
		flash    string
		resets   int
		sessions []string
		queue    *fakeEmailQueue
	}
	post := func(t *testing.T, plain, password string, resetErr error) result {
		t.Helper()
		var res result
		res.queue = &fakeEmailQueue{}
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com"}, Services{
			TokenService: resetTokens(t),
			UserService: &fakeUserStore{resetPasswordFn: func(ctx context.Context, hash []byte, pw string) (rss.User, error) {
				res.resets++
				if !bytes.Equal(hash, token.Hash(plain)) || pw != password {
					t.Errorf("ResetPassword got another hash or password %q, want %q", pw, password)
				}
				if resetErr != nil {
					return rss.User{}, resetErr
				}
				return resetUser, nil
			}},
			SessionService: &fakeSessionStore{createFn: func(ctx context.Context, userID string, idleBefore time.Time) (string, error) {
				res.sessions = append(res.sessions, userID)
				return testSessionID, nil
			}},
			EmailQueue: res.queue,
		})
		req := postFormRequest("/password/reset", url.Values{"token": {plain}, "password": {password}})
		res.rr, res.flash = serveConfirm(t, s, s.resetPasswordFormHandler, req, nil)
		return res
	}

	t.Run("valid", func(t *testing.T) {
		res := post(t, "valid-token", newPassword, nil)

		assertRedirect(t, res.rr, "/")
		if res.resets != 1 {
			t.Errorf("ResetPassword called %d times, want 1", res.resets)
		}
		if len(res.sessions) != 1 || res.sessions[0] != resetUser.ID {
			t.Errorf("sessions started for %q, want [%s]", res.sessions, resetUser.ID)
		}
		msgs := res.queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		if msgs[0].To != resetUser.Email || msgs[0].Subject != "Your rss password was changed" {
			t.Errorf("queued %q to %q, want the password_changed email to %q", msgs[0].Subject, msgs[0].To, resetUser.Email)
		}
		if want := "reset your password now (https://rss.example.com/password/forgot)"; !strings.Contains(msgs[0].Text, want) {
			t.Errorf("Text = %q, want it to contain %q", msgs[0].Text, want)
		}
		if want := "Your password was changed, and you're logged in."; res.flash != want {
			t.Errorf("flash = %q, want %q", res.flash, want)
		}
	})

	t.Run("a common password", func(t *testing.T) {
		res := post(t, "valid-token", "password123", nil)

		if res.rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", res.rr.Code, http.StatusUnprocessableEntity)
		}
		body := res.rr.Body.String()
		assertBodyContains(t, body,
			`<p class="error" id="password-error">This password is too common.`,
			`<input type="hidden" name="token" value="valid-token" />`,
			`value="ada@example.com"`,
		)
		if strings.Contains(body, "password123") {
			t.Error("body echoes the password")
		}
		if res.resets != 0 {
			t.Errorf("ResetPassword called %d times, want 0", res.resets)
		}
	})

	t.Run("the password is the email", func(t *testing.T) {
		res := post(t, "valid-token", "ADA@example.com", nil)
		if res.rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", res.rr.Code, http.StatusUnprocessableEntity)
		}
		if res.resets != 0 {
			t.Errorf("ResetPassword called %d times, want 0", res.resets)
		}
	})

	t.Run("token not valid", func(t *testing.T) {
		res := post(t, "used-token", newPassword, nil)
		assertResetLinkExpired(t, res.rr)
		if res.resets != 0 {
			t.Errorf("ResetPassword called %d times, want 0", res.resets)
		}
	})

	t.Run("ResetPassword finds no token", func(t *testing.T) {
		res := post(t, "valid-token", newPassword, rss.ErrNoRecord)

		assertResetLinkExpired(t, res.rr)
		if len(res.sessions) != 0 || len(res.queue.messages()) != 0 || res.flash != "" {
			t.Errorf("sessions %q, emails %d, flash %q; want none", res.sessions, len(res.queue.messages()), res.flash)
		}
	})

	t.Run("store error", func(t *testing.T) {
		res := post(t, "valid-token", newPassword, errors.New("boom"))
		if res.rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", res.rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestLoginLinksToReset(t *testing.T) {
	t.Run("before an attempt", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
		assertBodyContains(t, rr.Body.String(), `<a href="/password/forgot">Forgot password?</a>`)
	})

	t.Run("after a failed attempt", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			authenticateFn: func(context.Context, string, string) (string, error) { return "", rss.ErrWrongPassword },
		}})
		h := s.sessionManager.LoadAndSave(http.HandlerFunc(s.loginFormHandler)).ServeHTTP
		rr := postForm(t, h, "/login", "192.0.2.1:1234", url.Values{"email": {"a+b@example.com"}, "password": {"wrong-password"}})

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		body := rr.Body.String()
		assertBodyContains(t, body, `<a href="/password/forgot?email=a%2bb%40example.com">Forgot password?</a>`)
		if strings.Contains(body, "Reset it</a>") {
			t.Error("a page that isn't rate-limited has the rate-limit reset link")
		}
	})
}
