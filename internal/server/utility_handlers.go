package server

import "net/http"

func (s *Server) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	data := envelope{
		"status": "available",
		"system_info": map[string]any{
			"environment": s.Env,
			"version":     s.Version,
		},
	}

	err := s.writeJSON(w, http.StatusOK, data, nil)
	if err != nil {
		s.serverErrorResponse(w, r, err)
	}
}
