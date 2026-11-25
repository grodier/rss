package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/form/v4"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) error {
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}

	js = append(js, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := w.Write(js); err != nil {
		return err
	}

	return nil
}

func (s *Server) logError(r *http.Request, err error) {
	var (
		method = r.Method
		uri    = r.URL.RequestURI()
	)

	s.logger.Error(err.Error(), "method", method, "uri", uri)
}

// maxFormBytes caps the size of a decoded form body.
const maxFormBytes = 64 << 10

// decodePostForm parses the request body, capped at maxFormBytes, into dst.
// Respond to its errors with formErrorResponse.
func (s *Server) decodePostForm(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	err := r.ParseForm()
	if err != nil {
		return err
	}

	err = s.formDecoder.Decode(dst, r.PostForm)
	if err != nil {
		if _, ok := errors.AsType[*form.InvalidDecoderError](err); ok {
			panic(err)
		}

		return err
	}

	return nil
}

// authenticatedUserID returns the ID of the logged-in user, set by the
// authenticate middleware. ok is false for anonymous requests.
func (s *Server) authenticatedUserID(r *http.Request) (id string, ok bool) {
	id, ok = r.Context().Value(authenticatedUserIDContextKey).(string)
	return id, ok && id != ""
}

// authenticatedSession returns the logged-in device's session and user, set
// by authenticate. ok is false for anonymous requests.
func (s *Server) authenticatedSession(r *http.Request) (rss.Session, bool) {
	sess, ok := r.Context().Value(authenticatedSessionContextKey).(rss.Session)
	return sess, ok
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	_, ok := s.authenticatedUserID(r)
	return ok
}

// fetched reports whether r was sent by a script's fetch rather than a form
// submission or a navigation, from its Sec-Fetch-Mode: a handler answers
// it with a 204 instead of a redirect. A beacon isn't fetched (see
// scripted).
func fetched(r *http.Request) bool {
	m := r.Header.Get("Sec-Fetch-Mode")
	return m == "cors" || m == "same-origin"
}

// scripted reports whether r was sent by a script (a fetch or a beacon,
// whose mode is no-cors) rather than a navigation, from its
// Sec-Fetch-Mode. A request without the header isn't.
func scripted(r *http.Request) bool {
	m := r.Header.Get("Sec-Fetch-Mode")
	return m != "" && m != "navigate"
}

// returnForm is a form whose only field says where to go back to: the
// subscribe, unsubscribe, save and unsave buttons post it.
type returnForm struct {
	// ReturnTo is the local path to redirect back to, usually the page the
	// form was posted from.
	ReturnTo string `form:"return_to"`
}

// safeReturnPath returns p if it is a local path (for redirecting back to the
// page a form was posted from), else fallback. Prevents open redirects.
func safeReturnPath(p, fallback string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return fallback
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return p
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
