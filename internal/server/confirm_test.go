package server

import (
	"bytes"
	"context"
	"errors"
	"io"
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

var (
	unconfirmedSession = rss.Session{
		ID:   testSessionID,
		User: rss.User{ID: "user-1", Name: "Ada Lovelace", Email: "ada@example.com"},
	}
	confirmedSession = rss.Session{
		ID:   testSessionID,
		User: rss.User{ID: "user-1", Name: "Ada Lovelace", Email: "ada@example.com", EmailConfirmedAt: time.Now()},
	}
)

// confirmLinkRX finds the token in a confirmation email's link.
var confirmLinkRX = regexp.MustCompile(`/confirm-email\?token=([A-Za-z0-9_-]{43})`)

// serveConfirm sends req to h as sess (anonymous when nil) and returns the
// response and the flash message left in the session.
func serveConfirm(t *testing.T, s *Server, h http.HandlerFunc, req *http.Request, sess *rss.Session) (*httptest.ResponseRecorder, string) {
	t.Helper()
	if sess != nil {
		req = req.WithContext(requestWithSession(*sess).Context())
	}
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func postFormRequest(target string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestSignupSendsConfirmation(t *testing.T) {
	newServer := func(tokens *fakeTokenStore, queue *fakeEmailQueue) *Server {
		return newSignupTestServer(t, Services{UserService: &fakeUserStore{
			createFn: func(context.Context, string, string, string) (string, time.Time, error) {
				return "user-1", time.Now(), nil
			},
		}, SessionService: &fakeSessionStore{
			createFn: func(context.Context, string, time.Time) (string, error) {
				return testSessionID, nil
			},
		}, TokenService: tokens, EmailQueue: queue})
	}

	t.Run("queues one email with the link", func(t *testing.T) {
		tokens, queue := &fakeTokenStore{}, &fakeEmailQueue{}
		s := newServer(tokens, queue)

		rr := postSignup(t, s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)))

		assertRedirect(t, rr, "/")
		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		m := msgs[0]
		if m.To != "dup@example.com" {
			t.Errorf("To = %q, want dup@example.com", m.To)
		}
		if want := "Confirm your email address for rss"; m.Subject != want {
			t.Errorf("Subject = %q, want %q", m.Subject, want)
		}
		match := confirmLinkRX.FindStringSubmatch(m.Text)
		if match == nil {
			t.Fatalf("Text has no confirmation link: %q", m.Text)
		}

		if len(tokens.created) != 1 {
			t.Fatalf("created %d tokens, want 1", len(tokens.created))
		}
		tok := tokens.created[0]
		if !bytes.Equal(token.Hash(match[1]), tok.Hash) {
			t.Error("the link's token doesn't hash to the stored hash")
		}
		if tok.UserID != "user-1" || tok.Purpose != rss.TokenConfirmEmail || tok.Email != "dup@example.com" {
			t.Errorf("token = %+v, want user-1, %s, dup@example.com", tok, rss.TokenConfirmEmail)
		}
		if d := time.Until(tok.ExpiresAt) - 7*24*time.Hour; d < -time.Minute || d > time.Minute {
			t.Errorf("ExpiresAt = %v, want about 7 days from now", tok.ExpiresAt)
		}
	})

	t.Run("a token failure still completes signup", func(t *testing.T) {
		queue := &fakeEmailQueue{}
		s := newServer(&fakeTokenStore{createErr: io.ErrUnexpectedEOF}, queue)

		rr := postSignup(t, s.sessionManager.LoadAndSave(http.HandlerFunc(s.signupFormHandler)))

		assertRedirect(t, rr, "/")
		if n := len(queue.messages()); n != 0 {
			t.Errorf("queued %d emails, want 0", n)
		}
	})
}

func TestConfirmEmailHandler(t *testing.T) {
	const plain = "valid-token"
	tokens := &fakeTokenStore{getFn: func(ctx context.Context, hash []byte, purpose string) (rss.Token, error) {
		if purpose != rss.TokenConfirmEmail {
			t.Errorf("purpose = %q, want %q", purpose, rss.TokenConfirmEmail)
		}
		if !bytes.Equal(hash, token.Hash(plain)) {
			return rss.Token{}, rss.ErrNoRecord
		}
		return rss.Token{UserID: "user-1", Purpose: purpose, Email: "ada@example.com"}, nil
	}}
	s := newTestServerWith(t, Services{TokenService: tokens})

	t.Run("valid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/confirm-email?token="+plain, nil)
		rr, _ := serveConfirm(t, s, s.confirmEmailHandler, req, nil)

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
			"<h1>Confirm your email address</h1>",
			"Confirm ada@example.com for your rss account.",
			`<form action="/confirm-email" method="post">`,
			`<input type="hidden" name="token" value="valid-token" />`,
			"Confirm email address</button>",
		)
	})

	tests := []struct {
		name   string
		sess   *rss.Session
		want   []string
		absent []string
	}{
		{
			name:   "anonymous",
			want:   []string{`<a class="btn" href="/login">Log in to get a new link</a>`},
			absent: []string{"/confirm-email/resend"},
		},
		{
			name:   "unconfirmed",
			sess:   &unconfirmedSession,
			want:   []string{`<form action="/confirm-email/resend" method="post">`, "Send a new link</button>"},
			absent: []string{"Log in to get a new link", "already confirmed"},
		},
		{
			name:   "confirmed",
			sess:   &confirmedSession,
			want:   []string{"Your email address is already confirmed.", `href="/settings"`},
			absent: []string{"/confirm-email/resend", "Log in to get a new link"},
		},
	}
	for _, tt := range tests {
		t.Run("not valid, "+tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/confirm-email?token=used-token", nil)
			rr, _ := serveConfirm(t, s, s.confirmEmailHandler, req, tt.sess)

			if rr.Code != http.StatusGone {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
			}
			if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
			body := rr.Body.String()
			assertBodyContains(t, body, "<h1>This link has expired</h1>", "Confirmation links work for 7 days and only once.")
			assertBodyContains(t, body, tt.want...)
			for _, a := range tt.absent {
				if strings.Contains(body, a) {
					t.Errorf("body contains %q: %s", a, body)
				}
			}
		})
	}

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{TokenService: &fakeTokenStore{
			getFn: func(context.Context, []byte, string) (rss.Token, error) { return rss.Token{}, io.ErrUnexpectedEOF },
		}})
		req := httptest.NewRequest(http.MethodGet, "/confirm-email?token="+plain, nil)
		rr, _ := serveConfirm(t, s, s.confirmEmailHandler, req, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestConfirmEmailFormHandler(t *testing.T) {
	const plain = "valid-token"
	var gotHash []byte
	s := newTestServerWith(t, Services{UserService: &fakeUserStore{
		confirmEmailFn: func(ctx context.Context, hash []byte) (string, error) {
			gotHash = hash
			if !bytes.Equal(hash, token.Hash(plain)) {
				return "", rss.ErrNoRecord
			}
			return "user-1", nil
		},
	}})

	tests := []struct {
		name string
		sess *rss.Session
		want string
	}{
		{"logged in", &unconfirmedSession, "/"},
		{"anonymous", nil, "/login"},
	}
	for _, tt := range tests {
		t.Run("valid, "+tt.name, func(t *testing.T) {
			req := postFormRequest("/confirm-email", url.Values{"token": {plain}})
			rr, flash := serveConfirm(t, s, s.confirmEmailFormHandler, req, tt.sess)

			assertRedirect(t, rr, tt.want)
			if want := "Thanks, your email address is confirmed."; flash != want {
				t.Errorf("flash = %q, want %q", flash, want)
			}
			if !bytes.Equal(gotHash, token.Hash(plain)) {
				t.Error("ConfirmEmail wasn't given the token's hash")
			}
		})
	}

	t.Run("not valid", func(t *testing.T) {
		req := postFormRequest("/confirm-email", url.Values{"token": {"used-token"}})
		rr, flash := serveConfirm(t, s, s.confirmEmailFormHandler, req, nil)

		if rr.Code != http.StatusGone {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
		}
		assertBodyContains(t, rr.Body.String(), "<h1>This link has expired</h1>")
		if flash != "" {
			t.Errorf("flash = %q, want none", flash)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			confirmEmailFn: func(context.Context, []byte) (string, error) { return "", errors.New("boom") },
		}})
		req := postFormRequest("/confirm-email", url.Values{"token": {plain}})
		rr, _ := serveConfirm(t, s, s.confirmEmailFormHandler, req, nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestConfirmResendHandler(t *testing.T) {
	newServer := func() (*Server, *fakeTokenStore, *fakeEmailQueue) {
		tokens, queue := &fakeTokenStore{}, &fakeEmailQueue{}
		return newTestServerWith(t, Services{TokenService: tokens, EmailQueue: queue}), tokens, queue
	}
	resend := func(s *Server, sess rss.Session, returnTo string) (*httptest.ResponseRecorder, string) {
		req := postFormRequest("/confirm-email/resend", url.Values{"return_to": {returnTo}})
		return serveConfirm(t, s, s.confirmResendHandler, req, &sess)
	}

	t.Run("sends a new link", func(t *testing.T) {
		s, tokens, queue := newServer()
		rr, flash := resend(s, unconfirmedSession, "/feeds")

		assertRedirect(t, rr, "/feeds")
		if want := "We sent a new link to ada@example.com."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		msgs := queue.messages()
		if len(msgs) != 1 || msgs[0].To != "ada@example.com" {
			t.Fatalf("queued %+v, want one email to ada@example.com", msgs)
		}
		if len(tokens.created) != 1 || tokens.created[0].UserID != "user-1" {
			t.Errorf("created %+v, want one token for user-1", tokens.created)
		}
	})

	t.Run("already confirmed", func(t *testing.T) {
		s, _, queue := newServer()
		rr, flash := resend(s, confirmedSession, "")

		assertRedirect(t, rr, "/settings")
		if want := "Your email address is already confirmed."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if n := len(queue.messages()); n != 0 {
			t.Errorf("queued %d emails, want 0", n)
		}
	})

	t.Run("a fourth resend within the hour", func(t *testing.T) {
		s, _, queue := newServer()
		for range 3 {
			resend(s, unconfirmedSession, "/")
		}
		rr, flash := resend(s, unconfirmedSession, "/")

		assertRedirect(t, rr, "/")
		if want := "We've already sent you several links. Check your spam folder, or try again in an hour."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if n := len(queue.messages()); n != 3 {
			t.Errorf("queued %d emails, want 3", n)
		}
	})

	t.Run("return_to that isn't a local path", func(t *testing.T) {
		s, _, _ := newServer()
		rr, _ := resend(s, unconfirmedSession, "//evil.example")
		assertRedirect(t, rr, "/settings")
	})

	t.Run("requires login", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, postFormRequest("/confirm-email/resend", nil))
		assertRedirect(t, rr, "/login")
	})
}

func TestConfirmNotice(t *testing.T) {
	const notice = "Confirm your email address: we sent a link to <strong>ada@example.com</strong>."
	pages := []struct {
		name    string
		target  string
		handler func(s *Server) http.HandlerFunc
	}{
		{"timeline", "/", func(s *Server) http.HandlerFunc { return s.homeHandler }},
		{"settings", "/settings", func(s *Server) http.HandlerFunc { return s.settingsHandler }},
	}
	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			s := newTestServerWith(t, Services{ArticleService: &fakeArticleStore{}, ReadService: &fakeReadStore{}})
			get := func(sess rss.Session) string {
				rr, _ := serveConfirm(t, s, p.handler(s), httptest.NewRequest(http.MethodGet, p.target, nil), &sess)
				if rr.Code != http.StatusOK {
					t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
				}
				return rr.Body.String()
			}

			body := get(unconfirmedSession)
			assertBodyContains(t, body,
				notice,
				`<form action="/confirm-email/resend" method="post">`,
				`<input type="hidden" name="return_to" value="`+p.target+`" />`,
				"Resend</button>",
			)

			if body := get(confirmedSession); strings.Contains(body, "confirm-email/resend") {
				t.Errorf("confirmed user sees the notice: %s", body)
			}
		})
	}
}

func TestSettingsNotConfirmedTag(t *testing.T) {
	s := newTestServer(t)
	for _, tt := range []struct {
		name string
		sess rss.Session
		want bool
	}{
		{"unconfirmed", unconfirmedSession, true},
		{"confirmed", confirmedSession, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr, _ := serveConfirm(t, s, s.settingsHandler, httptest.NewRequest(http.MethodGet, "/settings", nil), &tt.sess)
			if got := strings.Contains(rr.Body.String(), `<span class="chip">Not confirmed</span>`); got != tt.want {
				t.Errorf("Not confirmed tag shown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTimelineFlash(t *testing.T) {
	s := newTestServerWith(t, Services{ArticleService: &fakeArticleStore{}, ReadService: &fakeReadStore{}})
	rr := httptest.NewRecorder()
	var flashLeft bool
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.sessionManager.Put(r.Context(), "flash", "Thanks, your email address is confirmed.")
		s.homeHandler(w, r)
		flashLeft = s.sessionManager.Exists(r.Context(), "flash")
	})).ServeHTTP(rr, requestWithSession(confirmedSession))

	assertBodyContains(t, rr.Body.String(), `<div class="flash">Thanks, your email address is confirmed.</div>`)
	if flashLeft {
		t.Error("flash is still in the session after the timeline showed it")
	}
}
