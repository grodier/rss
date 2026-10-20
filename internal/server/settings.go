package server

import (
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
