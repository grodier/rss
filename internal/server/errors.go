package server

import (
	"net/http"
)

func (s *Server) errorResponse(w http.ResponseWriter, r *http.Request, status int, errorCode string, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}

	data := envelope{
		"error_code": errorCode,
		"message":    message,
		"details":    details,
	}

	err := s.writeJSON(w, status, data, nil)
	if err != nil {
		s.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (s *Server) serverErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "the server encountered a problem and could not process your request", nil)
}
