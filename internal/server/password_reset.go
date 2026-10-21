package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// resetLinkTTL is how long a password reset link works.
const resetLinkTTL = time.Hour

// sendPasswordReset emails user a link to choose a new password.
func (s *Server) sendPasswordReset(ctx context.Context, user rss.User) error {
	plain, hash, err := token.New()
	if err != nil {
		return err
	}

	err = s.services.TokenService.Create(ctx, rss.Token{
		Hash:      hash,
		UserID:    user.ID,
		Purpose:   rss.TokenResetPassword,
		Email:     user.Email,
		ExpiresAt: time.Now().Add(resetLinkTTL),
	})
	if err != nil {
		return err
	}

	return s.sendEmail(ctx, user.Email, "reset_password", struct{ Name, URL string }{
		Name: user.Name,
		URL:  s.absoluteURL("/password/reset?token=" + plain),
	})
}

// sendPasswordChanged tells user their password was changed, with a link to
// reset it in case it wasn't them.
func (s *Server) sendPasswordChanged(ctx context.Context, user rss.User) error {
	return s.sendEmail(ctx, user.Email, "password_changed", struct{ Name, ResetURL string }{
		Name:     user.Name,
		ResetURL: s.absoluteURL("/password/forgot"),
	})
}

type forgotPasswordForm struct {
	Email               string `form:"email"`
	validator.Validator `form:"-"`
}

type forgotPasswordPage struct {
	Form         forgotPasswordForm
	SupportEmail string
}

// renderForgotPassword renders forgot_password.html with status.
func (s *Server) renderForgotPassword(w http.ResponseWriter, r *http.Request, status int, form forgotPasswordForm) {
	data := forgotPasswordPage{Form: form, SupportEmail: s.config.SupportEmail}
	if err := s.renderHTML(w, status, "forgot_password.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) forgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
	s.renderForgotPassword(w, r, http.StatusOK, forgotPasswordForm{Email: r.URL.Query().Get("email")})
}

func (s *Server) forgotPasswordFormHandler(w http.ResponseWriter, r *http.Request) {
	var form forgotPasswordForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	if !form.Valid() {
		s.renderForgotPassword(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	// Per email lowercased, because users.email is citext.
	for _, l := range []struct {
		limiter *rateLimiter
		key     string
	}{
		{s.resetIPLimiter, ipRateLimitKey(s.clientIP(r))},
		{s.resetEmailLimiter, strings.ToLower(form.Email)},
	} {
		if ok, retryAfter := l.limiter.AllowWithRetry(l.key); !ok {
			setRetryAfter(w, retryAfter)
			form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
			s.renderForgotPassword(w, r, http.StatusTooManyRequests, form)
			return
		}
	}

	user, err := s.services.UserService.GetByEmail(r.Context(), form.Email)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			form.AddFieldError("email", "There's no account with this email.")
			s.renderForgotPassword(w, r, http.StatusUnprocessableEntity, form)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	if err := s.sendPasswordReset(r.Context(), user); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "resetSentTo", user.Email)
	http.Redirect(w, r, "/password/forgot/sent", http.StatusSeeOther)
}

func (s *Server) forgotPasswordSentHandler(w http.ResponseWriter, r *http.Request) {
	email := s.sessionManager.GetString(r.Context(), "resetSentTo")
	if email == "" {
		http.Redirect(w, r, "/password/forgot", http.StatusSeeOther)
		return
	}

	data := struct {
		Email        string
		SupportEmail string
	}{Email: email, SupportEmail: s.config.SupportEmail}
	if err := s.renderHTML(w, http.StatusOK, "forgot_password_sent.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// resetLinkExpired renders the expired-link page for a reset link.
func (s *Server) resetLinkExpired(w http.ResponseWriter, r *http.Request) {
	data := linkExpiredPage{
		Title: "This link has expired",
		Text:  "Reset links work for 1 hour and only once.",
		Link:  &linkExpiredAction{URL: "/password/forgot", Label: "Send a new link"},
	}
	if err := s.renderHTML(w, http.StatusGone, "link_expired.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type resetPasswordForm struct {
	Token               string `form:"token"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

type resetPasswordPage struct {
	// Email is the token's address, shown read-only for password managers.
	Email string
	Form  resetPasswordForm
}

// renderResetPassword renders reset_password.html with status. It never
// echoes the password back.
func (s *Server) renderResetPassword(w http.ResponseWriter, r *http.Request, status int, email string, form resetPasswordForm) {
	form.Password = ""
	data := resetPasswordPage{Email: email, Form: form}
	if err := s.renderHTML(w, status, "reset_password.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// resetToken returns the valid reset_password token whose plain text is
// plain. When there's none, or on an error, it has responded and returns
// false.
func (s *Server) resetToken(w http.ResponseWriter, r *http.Request, plain string) (rss.Token, bool) {
	// The token is in the URL or the page: keep it out of Referer headers
	// and caches.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

	t, err := s.services.TokenService.Get(r.Context(), token.Hash(plain), rss.TokenResetPassword)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.resetLinkExpired(w, r)
			return rss.Token{}, false
		}
		s.serverErrorHTML(w, r, err)
		return rss.Token{}, false
	}
	return t, true
}

func (s *Server) resetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	plain := r.URL.Query().Get("token")
	t, ok := s.resetToken(w, r, plain)
	if !ok {
		return
	}
	s.renderResetPassword(w, r, http.StatusOK, t.Email, resetPasswordForm{Token: plain})
}

func (s *Server) resetPasswordFormHandler(w http.ResponseWriter, r *http.Request) {
	var form resetPasswordForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	t, ok := s.resetToken(w, r, form.Token)
	if !ok {
		return
	}

	form.CheckNewPassword("password", form.Password, t.Email)
	if !form.Valid() {
		s.renderResetPassword(w, r, http.StatusUnprocessableEntity, t.Email, form)
		return
	}

	user, err := s.services.UserService.ResetPassword(r.Context(), token.Hash(form.Token), form.Password)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.resetLinkExpired(w, r)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	if err := s.startSession(r, user.ID); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// The password has changed either way, so a failure to queue the notice
	// doesn't fail the page.
	if err := s.sendPasswordChanged(r.Context(), user); err != nil {
		s.logError(r, err)
	}

	s.sessionManager.Put(r.Context(), "flash", "Your password was changed, and you're logged in.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
