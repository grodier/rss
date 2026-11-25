package admin

import (
	"net/http"

	"github.com/grodier/rss/internal/httpserve"
)

// healthcheckHandler answers Render's health check like www's
// (internal/server/healthcheck.go): always 200, since a failing check would
// restart the service, which can't fix the database; the body's "status" says
// whether the database answered (see httpserve.Health).
func (s *Server) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data, err := httpserve.Health(r.Context(), s.services.Database, s.config.Env, s.config.Version)
	if err != nil {
		s.logError(r, err)
	}
	if err := s.writeJSON(w, http.StatusOK, data); err != nil {
		s.serverErrorJSON(w, r, err)
	}
}
