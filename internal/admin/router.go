package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// router has the reader's middleware order (internal/server/router.go).
// Every page but the login's needs a logged-in admin.
func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	// Outermost, so every response is logged, including those from recoverPanic
	// and CrossOriginProtection.
	router.Use(s.logRequest)
	router.Use(s.recoverPanic)

	// Rejects cross-origin, non-safe browser requests using
	// Sec-Fetch-Site/Origin. Only same-origin posts get through: the reader
	// is same-site (a sibling subdomain) but not same-origin, so none of its
	// pages can post here.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(s.forbiddenResponse))
	router.Use(cop.Handler)

	router.Use(s.commonHeaders)

	// Serves HEAD with the GET handler.
	router.Use(middleware.GetHead)

	// Error pages load the session, so their nav shows a logged-in admin's
	// sections.
	router.NotFound(s.withSession(http.HandlerFunc(s.notFoundResponse)).ServeHTTP)
	router.MethodNotAllowed(s.withSession(http.HandlerFunc(s.methodNotAllowedResponse)).ServeHTTP)

	// Static files and the healthcheck need no session.
	router.Handle("/static/*", http.HandlerFunc(s.staticHandler))
	router.Get("/healthcheck", s.healthcheckHandler)

	router.Group(func(r chi.Router) {
		r.Use(s.sessionManager.LoadAndSave)
		r.Use(s.authenticate)

		r.Get("/login", s.loginHandler)
		r.Post("/login", s.loginFormHandler)
		r.Get("/login/code", s.loginCodeHandler)
		r.Post("/login/code", s.loginCodeFormHandler)
		r.Post("/logout", s.logoutHandler)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuthentication)
			r.Get("/", s.homeHandler)
			r.Get("/sample/charts", s.sampleChartsHandler) // #571's checkpoint only
		})
	})

	return router
}

// withSession loads the session and the logged-in admin for next, for
// handlers outside the router's session group.
func (s *Server) withSession(next http.Handler) http.Handler {
	return s.sessionManager.LoadAndSave(s.authenticate(next))
}
