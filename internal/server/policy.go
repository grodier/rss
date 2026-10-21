package server

import "net/http"

// policyPage is the data of privacy.html and terms.html.
type policyPage struct {
	SupportEmail string
}

func (s *Server) privacyHandler(w http.ResponseWriter, r *http.Request) {
	s.renderPolicy(w, r, "privacy.html")
}

func (s *Server) termsHandler(w http.ResponseWriter, r *http.Request) {
	s.renderPolicy(w, r, "terms.html")
}

func (s *Server) renderPolicy(w http.ResponseWriter, r *http.Request, page string) {
	data := policyPage{SupportEmail: s.config.SupportEmail}
	if err := s.renderHTML(w, http.StatusOK, page, data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
