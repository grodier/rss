package server

import "net/http"

func (s *Server) discoverHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "discover.html", nil); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
