package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ui"
)

func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	router.Use(s.recoverPanic)

	router.NotFound(s.notFoundResponse)
	router.MethodNotAllowed(s.methodNotAllowedResponse)

	router.Handle("/static/*", http.FileServerFS(ui.Static))

	router.Get("/healthcheck", s.healthcheckHandler)

	router.Get("/feeds", s.feedsHandler)
	router.Get("/", s.homeHandler)

	return router
}
