package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

type signupForm struct {
	Invite              string `form:"invite"`
	Name                string `form:"name"`
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) signupHandler(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderSignup(w, r, http.StatusOK, signupPage{Form: signupForm{Invite: r.URL.Query().Get("invite")}})
}

type signupPage struct {
	Form signupForm
	// DuplicateEmail adds a link to /login under the email field's error.
	DuplicateEmail bool
}

// renderSignup renders signup.html with status.
func (s *Server) renderSignup(w http.ResponseWriter, r *http.Request, status int, data signupPage) {
	if err := s.renderHTML(w, status, "signup.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) signupFormHandler(w http.ResponseWriter, r *http.Request) {
	var form signupForm
	err := s.decodePostForm(w, r, &form)
	if err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if ok, retryAfter := s.signupLimiter.AllowWithRetry(ipRateLimitKey(s.clientIP(r))); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderSignup(w, r, http.StatusTooManyRequests, signupPage{Form: form})
		return
	}

	form.Name = strings.TrimSpace(form.Name)
	form.CheckField(validator.NotBlank(form.Invite), "invite", "Invite code is required")
	form.CheckField(subtle.ConstantTimeCompare([]byte(form.Invite), []byte(s.config.InviteCode)) == 1, "invite", "This invite code isn't valid")
	form.CheckNewAccount(form.Name, form.Email, form.Password)

	if !form.Valid() {
		s.renderSignup(w, r, http.StatusUnprocessableEntity, signupPage{Form: form})
		return
	}

	id, _, err := s.services.UserService.Create(r.Context(), form.Name, form.Email, form.Password)
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrDuplicateEmail):
			form.AddFieldError("email", "There's already an account with this email.")
			s.renderSignup(w, r, http.StatusUnprocessableEntity, signupPage{Form: form, DuplicateEmail: true})
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	if err := s.startSession(r, id); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// The account works without a confirmed address, and the notice offers
	// Resend, so a failure here doesn't fail signup.
	if err := s.sendConfirmation(r.Context(), rss.User{ID: id, Name: form.Name, Email: form.Email}); err != nil {
		s.logError(r, err)
	}

	http.Redirect(w, r, s.popReturnTo(r), http.StatusSeeOther)
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	flash := s.sessionManager.PopString(r.Context(), "flash")
	s.renderLogin(w, r, http.StatusOK, loginForm{}, flash)
}

type loginPage struct {
	Form  loginForm
	Flash string
}

// renderLogin renders login.html with status.
func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, form loginForm, flash string) {
	data := loginPage{Form: form, Flash: flash}
	if err := s.renderHTML(w, status, "login.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type loginForm struct {
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) loginFormHandler(w http.ResponseWriter, r *http.Request) {
	var form loginForm
	err := s.decodePostForm(w, r, &form)
	if err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if ok, retryAfter := s.loginLimiter.AllowWithRetry(ipRateLimitKey(s.clientIP(r))); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, form, "")
		return
	}

	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	form.CheckField(validator.NotBlank(form.Password), "password", "Password is required")
	form.CheckField(validator.MaxChars(form.Password, 256), "password", "Password is too long")

	if !form.Valid() {
		s.renderLogin(w, r, http.StatusUnprocessableEntity, form, "")
		return
	}

	// Failures are counted per email, to slow password guessing against an
	// account. Lowercased because users.email is citext. Counted before
	// Authenticate, so concurrent attempts can't all get past the limit;
	// refunded below unless the password was wrong.
	emailKey := strings.ToLower(form.Email)
	if ok, retryAfter := s.loginFailureLimiter.AllowWithRetry(emailKey); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, form, "")
		return
	}

	id, err := s.services.UserService.Authenticate(r.Context(), form.Email, form.Password)
	if !errors.Is(err, rss.ErrWrongPassword) {
		s.loginFailureLimiter.Refund(emailKey)
	}
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrNoAccount):
			form.AddFieldError("email", "There's no account with this email.")
			s.renderLogin(w, r, http.StatusUnprocessableEntity, form, "")
		case errors.Is(err, rss.ErrWrongPassword):
			form.AddFieldError("password", "That password isn't right.")
			s.renderLogin(w, r, http.StatusUnprocessableEntity, form, "")
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	if err := s.startSession(r, id); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	http.Redirect(w, r, s.popReturnTo(r), http.StatusSeeOther)
}

// popReturnTo returns the page requireAuthentication sent the user away
// from, if it's a local path, else "/", and forgets it.
func (s *Server) popReturnTo(r *http.Request) string {
	return safeReturnPath(s.sessionManager.PopString(r.Context(), "returnTo"), "/")
}

// startSession logs userID in on this device: it renews the session token
// (against session fixation), creates a user_sessions row, and keeps its ID
// in the session.
func (s *Server) startSession(r *http.Request, userID string) error {
	if err := s.sessionManager.RenewToken(r.Context()); err != nil {
		return err
	}

	id, err := s.services.SessionService.Create(r.Context(), userID, time.Now().Add(-sessionIdleTimeout))
	if err != nil {
		return err
	}

	s.sessionManager.Put(r.Context(), "sessionID", id)
	return nil
}

func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.authenticatedSession(r); ok {
		if err := s.services.SessionService.Delete(r.Context(), sess.ID); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	err := s.sessionManager.RenewToken(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Remove(r.Context(), "sessionID")
	s.sessionManager.Put(r.Context(), "flash", "You've been logged out successfully!")

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
