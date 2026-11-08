package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// router has the reader's middleware order (internal/server/router.go),
// without what the admin doesn't have yet: sessions and logins (#567).
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

	router.NotFound(s.notFoundResponse)
	router.MethodNotAllowed(s.methodNotAllowedResponse)

	router.Handle("/static/*", http.HandlerFunc(s.staticHandler))
	router.Get("/healthcheck", s.healthcheckHandler)

	router.Get("/", s.homeHandler)

	return router
}
