package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Connection", "close")
				s.serverErrorHTML(w, r, fmt.Errorf("%v", err))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func (s *Server) commonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https:; frame-src https://www.youtube-nocookie.com https://player.vimeo.com")
		w.Header().Set("Referrer-Policy", "origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "deny")
		w.Header().Set("X-XSS-Protection", "0")

		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK // nothing was written: net/http sends 200
		}
		s.logger.Info("request",
			"ip", s.clientIP(r), "proto", r.Proto, "method", r.Method, "uri", r.RequestURI,
			"status", status, "size", ww.BytesWritten(), "duration", time.Since(start))
	})
}

func (s *Server) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthenticated(r) {
			// Remembered for popReturnTo after login. Other methods aren't:
			// replaying one as a GET would give a 405.
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				s.sessionManager.Put(r.Context(), "returnTo", r.URL.RequestURI())
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		w.Header().Add("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// sessionIdleTimeout ends a login that hasn't been used for this long.
const sessionIdleTimeout = 90 * 24 * time.Hour

// sessionTouchInterval is how out of date last_seen_at may get before a
// request updates it.
const sessionTouchInterval = 24 * time.Hour

// authenticate loads the logged-in device's session (see startSession) and
// puts it and its user's ID in the request context. Requests without a
// session, or whose session is gone or idle, continue as anonymous.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id := s.sessionManager.GetString(ctx, "sessionID")
		if id == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !validator.Matches(id, validator.UUIDRX) {
			s.sessionManager.Remove(ctx, "sessionID")
			next.ServeHTTP(w, r)
			return
		}

		sess, err := s.services.SessionService.Get(ctx, id)
		if errors.Is(err, rss.ErrNoRecord) {
			s.sessionManager.Remove(ctx, "sessionID")
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}

		idle := time.Since(sess.LastSeenAt)
		if idle > sessionIdleTimeout {
			if err := s.services.SessionService.Delete(ctx, id); err != nil {
				s.logError(r, err)
			}
			s.sessionManager.Remove(ctx, "sessionID")
			next.ServeHTTP(w, r)
			return
		}
		if idle > sessionTouchInterval {
			// Documented upkeep write on any request, GETs included: it's
			// idempotent, changes nothing the user sees, and happens at most
			// once a day per device (see "Overall" in docs/decisions.md).
			if err := s.services.SessionService.Touch(ctx, id); err != nil {
				s.logError(r, err)
			}
		}

		ctx = context.WithValue(ctx, authenticatedUserIDContextKey, sess.User.ID)
		ctx = context.WithValue(ctx, authenticatedSessionContextKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
