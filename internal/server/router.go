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

	// After logRequest and recoverPanic, so redirects are logged; before
	// everything else, so a request for another host never reaches a handler.
	router.Use(s.redirectToCanonicalHost)

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

	// The manifest needs no session, like /static: browsers fetch it without
	// cookies. It has its own handler because the file server would sniff
	// its type (see "Mobile" in docs/decisions.md).
	router.Get("/manifest.webmanifest", s.manifestHandler)

	// Icons need no session, like /static: they aren't private.
	router.Get("/sites/{id}/icon", s.siteIconHandler)

	router.Get("/healthcheck", s.healthcheckHandler)

	// The policy pages need no session: they're the same for everyone.
	router.Get("/privacy", s.privacyHandler)
	router.Get("/terms", s.termsHandler)

	// Shared timelines are public and need no session: their owner opted in,
	// and their links carry an unguessable token.
	router.Get("/shared/{token}", s.sharedTimelineHandler)
	router.Get("/shared/{token}/feed.atom", s.sharedFeedHandler)

	router.Group(func(r chi.Router) {
		r.Use(s.sessionManager.LoadAndSave)
		r.Use(s.authenticate)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuthentication)
			r.Get("/feeds/{id}", s.feedHandler)
			r.Post("/feeds/{id}/refresh", s.feedRefreshHandler)
			r.Post("/feeds/{id}/subscribe", s.feedSubscribeHandler)
			r.Post("/feeds/{id}/unsubscribe", s.feedUnsubscribeHandler)
			r.Get("/feeds/{id}/edit", s.subscriptionEditHandler)
			r.Post("/feeds/{id}/edit", s.subscriptionEditPostHandler)
			r.Get("/feeds", s.feedsHandler)
			r.Get("/tags", s.tagsHandler)
			r.Post("/tags", s.tagCreateHandler)
			r.Get("/tags/{id}", s.tagHandler)
			r.Post("/tags/{id}/feeds", s.tagFeedsHandler)
			r.Post("/tags/{id}/rename", s.tagRenameHandler)
			r.Post("/tags/{id}/delete", s.tagDeleteHandler)
			r.Get("/timelines", s.timelinesHandler)
			r.Post("/timelines", s.timelineCreateHandler)
			r.Get("/timelines/new", s.timelineNewHandler)
			r.Get("/timelines/{id}", s.timelineHandler)
			r.Get("/timelines/{id}/edit", s.timelineEditHandler)
			r.Post("/timelines/{id}/edit", s.timelineEditPostHandler)
			r.Post("/timelines/{id}/delete", s.timelineDeleteHandler)
			r.Post("/timelines/{id}/move", s.timelineMoveHandler)
			r.Post("/timelines/{id}/share", s.timelineShareHandler)
			r.Post("/timelines/{id}/unshare", s.timelineUnshareHandler)
			r.Post("/timelines/{id}/share/new-link", s.timelineNewLinkHandler)
			r.Post("/timeline/seen", s.timelineSeenHandler)
			r.Get("/saved", s.savedHandler)
			r.Get("/articles/{id}", s.articleHandler)
			r.Post("/articles/{id}/read", s.articleReadHandler)
			r.Post("/articles/{id}/save", s.articleSaveHandler)
			r.Post("/articles/{id}/unsave", s.articleUnsaveHandler)
			r.Post("/articles/{id}/star", s.articleStarHandler)
			r.Post("/articles/{id}/unstar", s.articleUnstarHandler)
			r.Get("/sites/{id}", s.siteHandler)
			r.Get("/search", s.searchHandler)
			r.Post("/lookups", s.lookupCreateHandler)
			r.Get("/lookups/{id}", s.lookupHandler)
			r.Get("/settings", s.settingsHandler)
			r.Get("/settings/name", s.settingsNameHandler)
			r.Post("/settings/name", s.settingsNameFormHandler)
			r.Get("/settings/theme", s.settingsThemeHandler)
			r.Post("/settings/theme", s.settingsThemeFormHandler)
			r.Get("/settings/password", s.settingsPasswordHandler)
			r.Post("/settings/password", s.settingsPasswordFormHandler)
			r.Get("/settings/email", s.settingsEmailHandler)
			r.Post("/settings/email", s.settingsEmailFormHandler)
			r.Post("/settings/log-out-others", s.logOutOthersHandler)
			r.Post("/settings/debug", s.settingsDebugFormHandler)
			r.Get("/settings/export", s.settingsExportHandler)
			r.Get("/settings/subscriptions.opml", s.exportOPMLHandler)
			r.Get("/settings/import", s.settingsImportHandler)
			r.Post("/settings/import", s.settingsImportFormHandler)
			r.Get("/settings/imports/{id}", s.importHandler)
			r.Get("/settings/delete", s.settingsDeleteHandler)
			r.Post("/settings/delete", s.settingsDeleteFormHandler)
			r.Post("/confirm-email/resend", s.confirmResendHandler)

			r.Group(func(r chi.Router) {
				r.Use(s.requireDebug)
				r.Post("/debug/feeds/{id}/inspect", s.feedInspectHandler)
				r.Post("/debug/articles/{id}/inspect-page", s.articlePageInspectHandler)
				r.Post("/debug/sites/{id}/discover", s.siteDiscoverHandler)
			})
		})

		r.Group(func(r chi.Router) {
			if s.config.InviteCode != "" {
				r.Get("/signup", s.signupHandler)
				r.Post("/signup", s.signupFormHandler)
			}
			r.Get("/login", s.loginHandler)
			r.Post("/login", s.loginFormHandler)
			r.Post("/logout", s.logoutHandler)

			// Email links work in any browser, logged in or not.
			r.Get("/confirm-email", s.confirmEmailHandler)
			r.Post("/confirm-email", s.confirmEmailFormHandler)
			r.Get("/confirm-email/done", s.confirmEmailDoneHandler)
			r.Get("/email-change", s.emailChangeHandler)
			r.Post("/email-change", s.emailChangeFormHandler)

			r.Get("/password/forgot", s.forgotPasswordHandler)
			r.Post("/password/forgot", s.forgotPasswordFormHandler)
			r.Get("/password/forgot/sent", s.forgotPasswordSentHandler)
			r.Get("/password/reset", s.resetPasswordHandler)
			r.Post("/password/reset", s.resetPasswordFormHandler)

			// Lets password managers send people to the change-password page
			// (https://w3c.github.io/webappsec-change-password-url/).
			// Outside requireAuthentication, so it redirects before any login,
			// and login's returnTo brings people back.
			r.Get("/.well-known/change-password", s.changePasswordWellKnownHandler)

			r.Get("/", s.homeHandler)
		})
	})

	return router
}
