package admin

import "net/http"

// homeHandler shows the Overview, empty until #575 fills it.
func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.renderHTML(w, http.StatusOK, "home.html", homePage{Nav: s.nav(r, "overview")}); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
