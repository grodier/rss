package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-playground/form/v4"
)

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any, headers http.Header) error {
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}

	js = append(js, '\n')

	for k, values := range headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

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

func (s *Server) decodePostForm(r *http.Request, dst any) error {
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

func (s *Server) isAuthenticated(r *http.Request) bool {
	_, ok := s.authenticatedUserID(r)
	return ok
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
