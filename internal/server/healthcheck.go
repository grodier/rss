package server

import "net/http"

func (s *Server) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"status": "available",
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
