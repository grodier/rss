package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) settingsHandler(w http.ResponseWriter, r *http.Request) {
	nav := s.nav(r, "settings")
	data := struct {
		Nav   navData
		User  rss.User
		Flash string
	}{
		Nav:   nav,
		User:  nav.User,
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}

	if err := s.renderHTML(w, http.StatusOK, "settings.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type nameForm struct {
	Name                string `form:"name"`
	validator.Validator `form:"-"`
}

type settingsNamePage struct {
	Nav  navData
	Form nameForm
}

// renderSettingsName renders settings_name.html with status.
func (s *Server) renderSettingsName(w http.ResponseWriter, r *http.Request, status int, form nameForm) {
	data := settingsNamePage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_name.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsNameHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticatedSession(r)
	s.renderSettingsName(w, r, http.StatusOK, nameForm{Name: sess.User.Name})
}

func (s *Server) settingsNameFormHandler(w http.ResponseWriter, r *http.Request) {
	var form nameForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	form.Name = strings.TrimSpace(form.Name)
	form.CheckName(form.Name)
	if !form.Valid() {
		s.renderSettingsName(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.UserService.UpdateName(r.Context(), userID, form.Name); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Your name was updated.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type passwordForm struct {
	CurrentPassword     string `form:"current_password"`
	NewPassword         string `form:"new_password"`
	validator.Validator `form:"-"`
}

type settingsPasswordPage struct {
	Nav  navData
	Form passwordForm
}

// renderSettingsPassword renders settings_password.html with status. It never
// echoes the passwords back.
func (s *Server) renderSettingsPassword(w http.ResponseWriter, r *http.Request, status int, form passwordForm) {
	form.CurrentPassword, form.NewPassword = "", ""
	data := settingsPasswordPage{Nav: s.nav(r, "settings"), Form: form}
	if err := s.renderHTML(w, status, "settings_password.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) settingsPasswordHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsPassword(w, r, http.StatusOK, passwordForm{})
}

func (s *Server) settingsPasswordFormHandler(w http.ResponseWriter, r *http.Request) {
	var form passwordForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	sess, _ := s.authenticatedSession(r)
	user := sess.User

	if ok, retryAfter := s.passwordChangeLimiter.AllowWithRetry(user.ID); !ok {
		setRetryAfter(w, retryAfter)
		form.AddNonFieldError(tooManyAttemptsMessage(retryAfter))
		s.renderSettingsPassword(w, r, http.StatusTooManyRequests, form)
		return
	}

	form.CheckField(validator.NotBlank(form.CurrentPassword), "current_password", "Enter your current password.")
	form.CheckNewPassword("new_password", form.NewPassword, user.Email)
	form.CheckField(form.NewPassword != form.CurrentPassword, "new_password", "Your new password must be different from your current one.")
	if !form.Valid() {
		s.renderSettingsPassword(w, r, http.StatusUnprocessableEntity, form)
		return
	}

	err := s.services.UserService.ChangePassword(r.Context(), user.ID, sess.ID, form.CurrentPassword, form.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, rss.ErrWrongPassword):
			form.AddFieldError("current_password", "That password isn't right.")
			s.renderSettingsPassword(w, r, http.StatusUnprocessableEntity, form)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// The password has changed either way, so a failure to queue the notice
	// doesn't fail the page.
	if err := s.sendEmail(r.Context(), user.Email, "password_changed", struct{ Name string }{user.Name}); err != nil {
		s.logError(r, err)
	}

	s.sessionManager.Put(r.Context(), "flash", "Your password was changed. Other devices were logged out.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) logOutOthersHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticatedSession(r)
	n, err := s.services.SessionService.DeleteOthers(r.Context(), sess.User.ID, sess.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	flash := "You're logged out on every other device."
	if n == 0 {
		flash = "There were no other devices to log out."
	}
	s.sessionManager.Put(r.Context(), "flash", flash)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) changePasswordWellKnownHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/password", http.StatusFound)
}
