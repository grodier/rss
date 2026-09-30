package server

import (
	"net/http"
)

type MalformedRequest struct{ Msg string }

func (m *MalformedRequest) Error() string { return m.Msg }

func (s *Server) errorResponse(w http.ResponseWriter, r *http.Request, status int, errorCode string, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}

	data := map[string]any{
		"error_code": errorCode,
		"message":    message,
		"details":    details,
	}

	err := s.writeJSON(w, status, data, nil)
	if err != nil {
		s.logError(r, err)
	}
}

func (s *Server) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	s.errorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "the requested resource could not be found", nil)
}

func (s *Server) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	s.errorResponse(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "the request method is not supported for this resource", nil)
}

func (s *Server) serverErrorJSON(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "the server encountered a problem and could not process your request", nil)
}

func (s *Server) serverErrorHTML(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	if renderErr := s.renderHTML(w, http.StatusInternalServerError, "error.html", nil); renderErr != nil {
		s.logError(r, renderErr)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
