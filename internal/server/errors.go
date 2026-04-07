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

func (s *Server) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	s.errorResponse(w, r, http.StatusNotFound, "NOT_FOUND", "the requested resource could not be found", nil)
}

func (s *Server) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	s.errorResponse(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "the request method is not supported for this resource", nil)
}

func (s *Server) serverErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "the server encountered a problem and could not process your request", nil)
}

func (s *Server) badRequestResponse(w http.ResponseWriter, r *http.Request, err error) {
	s.errorResponse(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
}

func (s *Server) unauthorizedResponse(w http.ResponseWriter, r *http.Request, message string) {
	s.errorResponse(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", message, nil)
}

func (s *Server) forbiddenResponse(w http.ResponseWriter, r *http.Request, errorCode string, message string) {
	s.errorResponse(w, r, http.StatusForbidden, errorCode, message, nil)
}

func (s *Server) rateLimitedResponse(w http.ResponseWriter, r *http.Request) {
	s.errorResponse(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "you have exceeded the rate limit, please try again later", nil)
}

func (s *Server) conflictResponse(w http.ResponseWriter, r *http.Request, errorCode string, message string) {
	s.errorResponse(w, r, http.StatusConflict, errorCode, message, nil)
}

func (s *Server) validationErrorResponse(w http.ResponseWriter, r *http.Request, details any) {
	s.errorResponse(w, r, http.StatusBadRequest, "INVALID_INPUT", "the request failed validation", details)
}
