package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/grodier/rss/internal/ui"
)

func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	// Outermost, so every response is logged, including those from recoverPanic
	// and CrossOriginProtection.
	router.Use(s.logRequest)
	router.Use(s.recoverPanic)

	// Rejects cross-origin, non-safe browser requests using Sec-Fetch-Site/Origin.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(s.forbiddenResponse))
	router.Use(cop.Handler)

	router.Use(s.commonHeaders)

	// Serves HEAD with the GET handler (upkeep side effects included).
	router.Use(middleware.GetHead)

	router.NotFound(s.notFoundResponse)
	router.MethodNotAllowed(s.methodNotAllowedResponse)

	// Directory requests would otherwise get an auto-generated file listing.
	fileServer := http.FileServerFS(ui.Static)
	router.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			s.notFoundResponse(w, r)
			return
		}
		// Only a request for the file's current version is cached; any other
		// (no v, or a stale one) is served without Cache-Control.
		if v := r.URL.Query().Get("v"); v != "" && v == staticVersions[strings.TrimPrefix(r.URL.Path, "/static/")] {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
			if s.config.InviteCode != "" {
				r.Get("/signup", s.signupHandler)
				r.Post("/signup", s.signupFormHandler)
			}
			r.Get("/login", s.loginHandler)
			r.Post("/login", s.loginFormHandler)
			r.Post("/logout", s.logoutHandler)

			r.Get("/", s.homeHandler)
		})
	})

	return router
}
