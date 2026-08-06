package server

import "net/http"

func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "home.html", nil); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
