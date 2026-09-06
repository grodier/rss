package server

import "net/http"

func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	var homeTmpl string
	if s.isAuthenticated(r) {
		homeTmpl = "home_auth.html"
	} else {
		homeTmpl = "home.html"
	}
	if err := s.renderHTML(w, http.StatusOK, homeTmpl, nil); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
