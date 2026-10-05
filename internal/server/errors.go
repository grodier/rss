package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// allowCandidates are the methods probed, in order, to build the Allow header.
var allowCandidates = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

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

type errorPageData struct {
	Status  int
	Title   string
	Message string
}

func (s *Server) errorHTML(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	data := errorPageData{Status: status, Title: title, Message: message}
	if err := s.renderHTML(w, status, "error.html", data); err != nil {
		s.logError(r, err)
		http.Error(w, http.StatusText(status), status)
	}
}

func (s *Server) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	s.errorHTML(w, r, http.StatusNotFound, "Page not found", "The page you're looking for doesn't exist.")
}

func (s *Server) forbiddenResponse(w http.ResponseWriter, r *http.Request) {
	s.errorHTML(w, r, http.StatusForbidden, "Request blocked", "This form was submitted from another site, so it was rejected for your security.")
}

func (s *Server) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	// chi doesn't set Allow when a custom handler is installed, so ask the router.
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.Routes != nil {
		var allowed []string
		for _, m := range allowCandidates {
			// A fresh context: Match would modify the request's own.
			if rctx.Routes.Match(chi.NewRouteContext(), m, r.URL.Path) {
				allowed = append(allowed, m)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
	}
	s.errorHTML(w, r, http.StatusMethodNotAllowed, "Method not allowed", "That action isn't supported for this page.")
}

func (s *Server) tooManyRequestsResponse(w http.ResponseWriter, r *http.Request) {
	s.errorHTML(w, r, http.StatusTooManyRequests, "Too many requests", "You've looked up a lot of sites recently. Please wait a few minutes and try again.")
}

func (s *Server) serverErrorJSON(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorResponse(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "the server encountered a problem and could not process your request", nil)
}

func (s *Server) serverErrorHTML(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorHTML(w, r, http.StatusInternalServerError, "Something went wrong", "The server encountered a problem and could not process your request.")
}
