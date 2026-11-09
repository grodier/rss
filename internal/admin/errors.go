package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// allowCandidates are the methods probed, in order, to build the Allow header.
var allowCandidates = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// errorPageData is the error page's: like the reader's, plus the nav, with
// no section current.
type errorPageData struct {
	Nav     navData
	Status  int
	Title   string
	Message string
}

func (s *Server) errorHTML(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	data := errorPageData{Nav: s.nav(r, ""), Status: status, Title: title, Message: message}
	if err := s.renderHTML(w, status, "error.html", data); err != nil {
		s.logError(r, err)
		http.Error(w, http.StatusText(status), status)
	}
}

// formErrorResponse answers a form decodePostForm couldn't read: 413 when
// it was too large, else 400.
func (s *Server) formErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		s.errorHTML(w, r, http.StatusRequestEntityTooLarge, "Request too large", "That form submission was too large.")
		return
	}
	s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That form submission couldn't be read.")
}

func (s *Server) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	s.errorHTML(w, r, http.StatusNotFound, "Page not found", "There's no admin page here.")
}

func (s *Server) forbiddenResponse(w http.ResponseWriter, r *http.Request) {
	s.errorHTML(w, r, http.StatusForbidden, "Request blocked", "This form was submitted from another site, so it was rejected.")
}

func (s *Server) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	// chi doesn't set Allow when a custom handler is installed, so ask the router.
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.Routes != nil {
		var allowed []string
		for _, m := range allowCandidates {
			probe := m
			if m == http.MethodHead {
				probe = http.MethodGet // GetHead serves HEAD with the GET handler
			}
			// A fresh context: Match would modify the request's own.
			if rctx.Routes.Match(chi.NewRouteContext(), probe, r.URL.Path) {
				allowed = append(allowed, m)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
	}
	s.errorHTML(w, r, http.StatusMethodNotAllowed, "Method not allowed", "That action isn't supported for this page.")
}

func (s *Server) serverErrorHTML(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	s.errorHTML(w, r, http.StatusInternalServerError, "Something went wrong", "The server encountered a problem and could not process your request.")
}

func (s *Server) serverErrorJSON(w http.ResponseWriter, r *http.Request, err error) {
	s.logError(r, err)
	data := map[string]any{
		"error_code": "INTERNAL_ERROR",
		"message":    "the server encountered a problem and could not process your request",
	}
	if err := s.writeJSON(w, http.StatusInternalServerError, data); err != nil {
		s.logError(r, err)
	}
}
