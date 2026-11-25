package server

import (
	"net/http"

	"github.com/grodier/rss/internal/httpserve"
)

// healthcheckHandler always answers 200: Render's health check only reads the
// status code, and a failing one would stop routing to the instance and restart
// it, which can't fix the database. The uptime monitor reads the body's
// "status" instead (see httpserve.Health).
func (s *Server) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data, err := httpserve.Health(r.Context(), s.services.Database, s.config.Env, s.config.Version)
	if err != nil {
		s.logError(r, err)
	}
	if err := s.writeJSON(w, http.StatusOK, data); err != nil {
		s.serverErrorJSON(w, r, err)
	}
}
