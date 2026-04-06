package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	router.NotFound(s.notFoundResponse)
	router.MethodNotAllowed(s.methodNotAllowedResponse)

	router.Get("/v1/healthcheck", s.healthcheckHandler)

	return router
}
