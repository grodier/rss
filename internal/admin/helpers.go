package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/validator"
)

// writeJSON is only for /healthcheck (and its serverErrorJSON error path):
// admin pages are HTML. See httpserve.WriteJSON.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) error {
	return httpserve.WriteJSON(w, status, data)
}

func (s *Server) logError(r *http.Request, err error) {
	s.logger.Error(err.Error(), "method", r.Method, "uri", r.URL.RequestURI())
}

// decodePostForm parses the posted form, at most httpserve.MaxFormBytes,
// into dst, a struct with form tags. On error, the handler calls
// formErrorResponse.
func (s *Server) decodePostForm(w http.ResponseWriter, r *http.Request, dst any) error {
	return s.decodePostFormLimit(w, r, dst, httpserve.MaxFormBytes)
}

// decodePostFormLimit is decodePostForm for a route whose forms may be
// larger, at most limit bytes.
func (s *Server) decodePostFormLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	return httpserve.DecodePostForm(w, r, s.formDecoder, dst, limit)
}

// uuidParam returns the URL parameter name if it's a UUID. If ok is
// false it has already answered 404.
func (s *Server) uuidParam(w http.ResponseWriter, r *http.Request, name string) (id string, ok bool) {
	id = chi.URLParam(r, name)
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return "", false
	}
	return id, true
}
