package server

import "net/http"

func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "feeds.html", nil); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
