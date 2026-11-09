package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/form/v4"
)

// maxFormBytes caps a posted form's body, like the reader's.
const maxFormBytes = 64 << 10

// writeJSON is only for /healthcheck (and its serverErrorJSON error path):
// admin pages are HTML.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) error {
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}
	js = append(js, '\n')

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(js)
	return err
}

func (s *Server) logError(r *http.Request, err error) {
	s.logger.Error(err.Error(), "method", r.Method, "uri", r.URL.RequestURI())
}

// decodePostForm parses the posted form, at most maxFormBytes, into dst, a
// struct with form tags. On error, the handler calls formErrorResponse.
func (s *Server) decodePostForm(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return err
	}
	if err := s.formDecoder.Decode(dst, r.PostForm); err != nil {
		// A programming error: dst isn't a pointer to a struct.
		if _, ok := errors.AsType[*form.InvalidDecoderError](err); ok {
			panic(err)
		}
		return err
	}
	return nil
}
