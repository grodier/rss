package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ui"
)

func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	router.Use(s.recoverPanic)
	router.Use(s.logRequest)
	router.Use(s.commonHeaders)

	router.NotFound(s.notFoundResponse)
	router.MethodNotAllowed(s.methodNotAllowedResponse)

	//TODO: disable index routes
	router.Handle("/static/*", http.FileServerFS(ui.Static))

	router.Get("/healthcheck", s.healthcheckHandler)

	router.Post("/subscribe", s.subscribeFeedHandler)
	router.Get("/feeds/{id}", s.feedHandler)
	router.Get("/feeds", s.feedsHandler)
	router.Get("/", s.homeHandler)

	router.Post("/feeds", s.createFeedHandler)

	return router
}
