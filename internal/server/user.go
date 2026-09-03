package server

import (
	"fmt"
	"net/http"
)

func (s *Server) signupHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Display a form for signing up a new user...")
}

func (s *Server) signupFormHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Create a new user...")
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
