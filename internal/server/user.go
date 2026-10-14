package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

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
	data := struct {
		Form any
	}{Form: signupForm{Invite: r.URL.Query().Get("invite")}}
	if err := s.renderHTML(w, http.StatusOK, "signup.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) signupFormHandler(w http.ResponseWriter, r *http.Request) {
	var form signupForm
	err := s.decodePostForm(r, &form)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	if ok, retryAfter := s.signupLimiter.AllowWithRetry(ipRateLimitKey(s.clientIP(r))); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		data := struct {
			Form any
		}{Form: form}
		if err := s.renderHTML(w, http.StatusTooManyRequests, "signup.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	form.CheckField(validator.NotBlank(form.Invite), "invite", "Invite code is required")
	form.CheckField(subtle.ConstantTimeCompare([]byte(form.Invite), []byte(s.config.InviteCode)) == 1, "invite", "This invite code isn't valid")
	form.CheckNewAccount(form.Name, form.Email, form.Password)

	if !form.Valid() {
		data := struct {
			Form any
		}{Form: form}

		if err := s.renderHTML(w, http.StatusUnprocessableEntity, "signup.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	_, _, err = s.services.UserService.Create(r.Context(), form.Name, form.Email, form.Password)
	// A duplicate email gets the same response as a new account, so signup
	// doesn't reveal which emails have one.
	if err != nil && !errors.Is(err, rss.ErrDuplicateEmail) {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Your signup was successful. Please log in.")

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
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
	err := s.decodePostForm(r, &form)
	if err != nil {
		s.serverErrorHTML(w, r, err)
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

	// Failures are counted per submitted email, whether or not it has an
	// account, so a 429 doesn't reveal which emails do. Lowercased because
	// users.email is citext. Counted before Authenticate, so concurrent
	// attempts can't all get past the limit; refunded below unless the
	// password was wrong.
	emailKey := strings.ToLower(form.Email)
	if ok, retryAfter := s.loginFailureLimiter.AllowWithRetry(emailKey); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderLogin(w, r, http.StatusTooManyRequests, form, "")
		return
	}

	id, err := s.services.UserService.Authenticate(r.Context(), form.Email, form.Password)
	if !errors.Is(err, rss.ErrInvalidCredentials) {
		s.loginFailureLimiter.Refund(emailKey)
	}
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrInvalidCredentials):
			form.AddNonFieldError("Email or password is incorrect")
			s.renderLogin(w, r, http.StatusUnprocessableEntity, form, "")
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	err = s.sessionManager.RenewToken(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "authenticatedUserID", id)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	err := s.sessionManager.RenewToken(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Remove(r.Context(), "authenticatedUserID")
	s.sessionManager.Put(r.Context(), "flash", "You've been logged out successfully!")

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
