package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "feeds.html", nil); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feedName := chi.URLParam(r, "feedName")
	if feedName == "" {
		// TODO: Fix this
		s.badRequestResponse(w, r, &MalformedRequest{Msg: "feedName is required"})
		return
	}

	data := struct {
		Name string
	}{Name: feedName}

	if err := s.renderHTML(w, http.StatusOK, "feed.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
