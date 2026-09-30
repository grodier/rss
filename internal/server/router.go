package server

import (
	"net/http"
	"strings"

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

	// Directory requests would otherwise get an auto-generated file listing.
	fileServer := http.FileServerFS(ui.Static)
	router.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			s.notFoundResponse(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	}))

	router.Get("/healthcheck", s.healthcheckHandler)

	router.Group(func(r chi.Router) {
		r.Use(s.sessionManager.LoadAndSave)
		r.Use(s.authenticate)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuthentication)
			r.Post("/subscribe", s.subscribeFeedHandler)
			r.Get("/feeds/{id}", s.feedHandler)
			r.Get("/feeds", s.feedsHandler)
			r.Get("/discover", s.discoverHandler)
			//needs to move to discover??
			r.Post("/feeds", s.createFeedHandler)
		})

		r.Group(func(r chi.Router) {
			r.Get("/signup", s.signupHandler)
			r.Post("/signup", s.signupFormHandler)
			r.Get("/login", s.loginHandler)
			r.Post("/login", s.loginFormHandler)
			r.Get("/logout", s.logoutHandler)

			r.Get("/", s.homeHandler)
		})
	})

	return router
}
