package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
	"github.com/grodier/rss/internal/validator"
)

// changeEmailLinkTTL is how long a link to change the email works.
const changeEmailLinkTTL = 24 * time.Hour

type emailForm struct {
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

type settingsEmailPage struct {
	Nav  navData
	Form emailForm
}

// renderSettingsEmail renders settings_email.html with status. It never
// echoes the password back.
func (s *Server) renderSettingsEmail(w http.ResponseWriter, r *http.Request, status int, form emailForm) {
	form.Password = ""
	data := settingsEmailPage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_email.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsEmailHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsEmail(w, r, http.StatusOK, emailForm{})
}

func (s *Server) settingsEmailFormHandler(w http.ResponseWriter, r *http.Request) {
	var form emailForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	sess, _ := s.authenticatedSession(r)
	user := sess.User

	if ok, retryAfter := s.emailChangeLimiter.AllowWithRetry(user.ID); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderSettingsEmail(w, r, http.StatusTooManyRequests, form)
		return
	}

	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	form.CheckField(!strings.EqualFold(form.Email, user.Email), "email", "That's already your email address.")
	form.CheckField(validator.NotBlank(form.Password), "password", "Enter your password.")
	if !form.Valid() {
		s.renderSettingsEmail(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	if err := s.services.UserService.CheckPassword(r.Context(), user.ID, form.Password); err != nil {
		if errors.Is(err, rss.ErrWrongPassword) {
			form.AddFieldError("password", "That password isn't right.")
			s.renderSettingsEmail(w, r, http.StatusUnprocessableEntity, form)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	_, err := s.services.UserService.GetByEmail(r.Context(), form.Email)
	switch {
	case err == nil:
		form.AddFieldError("email", "There's already an account with this email.")
		s.renderSettingsEmail(w, r, http.StatusUnprocessableEntity, form)
		return
	case !errors.Is(err, rss.ErrNoRecord):
		s.serverErrorHTML(w, r, err)
		return
	}

	plain, hash, err := token.New()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	err = s.services.TokenService.Create(r.Context(), rss.Token{
		Hash:      hash,
		UserID:    user.ID,
		Purpose:   rss.TokenChangeEmail,
		Email:     form.Email,
		ExpiresAt: time.Now().Add(changeEmailLinkTTL),
	})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	err = s.sendEmail(r.Context(), form.Email, "confirm_new_email", struct{ Name, NewEmail, URL string }{
		Name:     user.Name,
		NewEmail: form.Email,
		URL:      s.absoluteURL("/email-change?token=" + plain),
	})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "We sent a link to "+form.Email+". Open it to finish changing your email.")
	http.Redirect(w, r, "/settings/account", http.StatusSeeOther)
}

// changeEmailLinkExpired renders the expired-link page for a link to change
// the email.
func (s *Server) changeEmailLinkExpired(w http.ResponseWriter, r *http.Request) {
	data := linkExpiredPage{
		Title: "This link has expired",
		Text:  "Links to change your email work for 24 hours and only once.",
		Link:  &linkExpiredAction{URL: "/settings/email", Label: "Try again"},
	}
	if err := s.renderHTML(w, http.StatusGone, "link_expired.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// confirmEmailChangePage is confirm_email_change.html's data. Taken says
// another account took NewEmail before the link was used; OldEmail is only
// set then.
type confirmEmailChangePage struct {
	NewEmail string
	OldEmail string
	Token    string
	Taken    bool
}

func (s *Server) emailChangeHandler(w http.ResponseWriter, r *http.Request) {
	// The token is in the URL: keep it out of Referer headers and caches.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")

	plain := r.URL.Query().Get("token")
	t, err := s.services.TokenService.Get(r.Context(), token.Hash(plain), rss.TokenChangeEmail)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.changeEmailLinkExpired(w, r)
			return
		}
		s.serverErrorHTML(w, r, err)
		return
	}

	data := confirmEmailChangePage{NewEmail: t.Email, Token: plain}
	if err := s.renderHTML(w, http.StatusOK, "confirm_email_change.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type emailChangeForm struct {
	Token string `form:"token"`
}

func (s *Server) emailChangeFormHandler(w http.ResponseWriter, r *http.Request) {
	var form emailChangeForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	old, newEmail, err := s.services.UserService.ChangeEmail(r.Context(), token.Hash(form.Token))
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrNoRecord):
			s.changeEmailLinkExpired(w, r)
		case errors.Is(err, rss.ErrDuplicateEmail):
			data := confirmEmailChangePage{NewEmail: newEmail, OldEmail: old.Email, Taken: true}
			if err := s.renderHTML(w, http.StatusConflict, "confirm_email_change.html", data); err != nil {
				s.serverErrorHTML(w, r, err)
			}
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// The email has changed either way, so a failure to queue the notice
	// doesn't fail the page.
	err = s.sendEmail(r.Context(), old.Email, "email_changed", struct{ Name, OldEmail, NewEmail string }{
		Name:     old.Name,
		OldEmail: old.Email,
		NewEmail: newEmail,
	})
	if err != nil {
		s.logError(r, err)
	}

	if s.isAuthenticated(r) {
		s.sessionManager.Put(r.Context(), "flash", "Your email address is now "+newEmail+".")
		http.Redirect(w, r, "/settings/account", http.StatusSeeOther)
		return
	}
	s.sessionManager.Put(r.Context(), "flash", "Your email address is now "+newEmail+". Log in with it to continue.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
