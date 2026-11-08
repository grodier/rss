package admin

import (
	"context"
	"net/http"
	"time"
)

// healthcheckTimeout bounds the database ping, so a hung database makes the
// check say "degraded" rather than hang.
const healthcheckTimeout = time.Second

// healthcheckHandler answers Render's health check like www's
// (internal/server/healthcheck.go): always 200, since a failing check would
// restart the service, which can't fix the database; the body's "status" says
// whether the database answered.
func (s *Server) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	status, database := "available", "ok"
	ctx, cancel := context.WithTimeout(r.Context(), healthcheckTimeout)
	defer cancel()
	if err := s.services.Database.PingContext(ctx); err != nil {
		s.logError(r, err)
		status, database = "degraded", "unreachable"
	}

	data := map[string]any{
		"status":   status,
		"database": database,
		"system_info": map[string]any{
			"environment": s.config.Env,
			"version":     s.config.Version,
		},
	}
	if err := s.writeJSON(w, http.StatusOK, data); err != nil {
		s.serverErrorJSON(w, r, err)
	}
}
