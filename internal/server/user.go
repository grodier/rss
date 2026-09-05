package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/validator"
)

type signupForm struct {
	Name                string `form:"name"`
	Email               string `form:"email"`
	Password            string `form:"password"`
	validator.Validator `form:"-"`
}

func (s *Server) signupHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "signup.html", nil); err != nil {
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

	form.CheckField(validator.NotBlank(form.Name), "name", "Name is required")
	form.CheckField(validator.NotBlank(form.Email), "email", "Email is required")
	form.CheckField(validator.Matches(form.Email, validator.EmailRX), "email", "Email must be a valid email address")
	form.CheckField(validator.NotBlank(form.Password), "password", "Password is required")
	form.CheckField(validator.MinChars(form.Password, 8), "password", "Password must be at least 8 characters long")
	form.CheckField(validator.MaxChars(form.Password, 72), "password", "Password must not be more than 72 characters long")

	if !form.Valid() {
		data := struct {
			Form any
		}{Form: form}

		if err := s.renderHTML(w, http.StatusUnprocessableEntity, "signup.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		return
	}

	id, createdAt, err := s.services.UserService.Create(form.Name, form.Email, form.Password)
	if err != nil {
		switch {
		case errors.Is(err, psql.ErrDuplicateEmail):
			form.AddFieldError("email", "Email is already in use")
			data := struct {
				Form any
			}{Form: form}
			if err := s.renderHTML(w, http.StatusUnprocessableEntity, "signup.html", data); err != nil {
				s.serverErrorHTML(w, r, err)
			}
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", fmt.Sprintf("User account %s created successfully at %s", id, createdAt.Format(time.RFC1123)))

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Display a form for logging in a user...")
}

func (s *Server) loginFormHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Authenticate and login the user...")
}

func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Logout the user...")
}
