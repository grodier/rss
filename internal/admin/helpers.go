package admin

import (
	"encoding/json"
	"net/http"
)

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
