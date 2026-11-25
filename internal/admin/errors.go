package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/httpserve"
)

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
	if allowed := httpserve.AllowedMethods(r); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
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
