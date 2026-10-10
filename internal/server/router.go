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

	// Rejects cross-origin, non-safe browser requests using Sec-Fetch-Site/Origin.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(s.forbiddenResponse))
	router.Use(cop.Handler)

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
			r.Get("/feeds/{id}", s.feedHandler)
			r.Post("/feeds/{id}/refresh", s.feedRefreshHandler)
			r.Post("/feeds/{id}/subscribe", s.feedSubscribeHandler)
			r.Post("/feeds/{id}/unsubscribe", s.feedUnsubscribeHandler)
			r.Get("/feeds", s.feedsHandler)
			r.Get("/articles/{id}", s.articleHandler)
			r.Post("/articles/{id}/read", s.articleReadHandler)
			r.Get("/sites/{id}", s.siteHandler)
			r.Get("/search", s.searchHandler)
			r.Post("/lookups", s.lookupCreateHandler)
			r.Get("/lookups/{id}", s.lookupHandler)
		})

		r.Group(func(r chi.Router) {
			r.Get("/signup", s.signupHandler)
			r.Post("/signup", s.signupFormHandler)
			r.Get("/login", s.loginHandler)
			r.Post("/login", s.loginFormHandler)
			r.Post("/logout", s.logoutHandler)

			r.Get("/", s.homeHandler)
		})
	})

	return router
}
