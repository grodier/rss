package server

import (
	"context"
	"net/http"
	"time"
)

// healthcheckTimeout bounds the database ping, so a hung database makes the
// check say "degraded" rather than hang.
const healthcheckTimeout = time.Second

// healthcheckHandler always answers 200: Render's health check only reads the
// status code, and a failing one would stop routing to the instance and restart
// it, which can't fix the database. The uptime monitor reads the body's
// "status" instead, so "degraded" must not contain its keyword "available".
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

	err := s.writeJSON(w, http.StatusOK, data)
	if err != nil {
		s.serverErrorJSON(w, r, err)
	}
}
