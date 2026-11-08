package admin

import "net/http"

// sampleHandler shows the design checkpoint's sample page (#562): on the
// branch only, not merged.
func (s *Server) sampleHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "sample.html", pageData{}); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
