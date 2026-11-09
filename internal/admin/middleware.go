package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/grodier/rss/internal/clientip"
	"github.com/grodier/rss/internal/rss"
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

// commonHeaders sends the admin's own security headers, stricter than the
// reader's: it shows no images from other sites, and no page of it may be
// framed, linked to with a referrer, or kept in a cache (static files set
// their own Cache-Control). HSTS is its own, as the reader's has no
// includeSubDomains (see "Admin" in docs/decisions.md).
func (s *Server) commonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "deny")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if s.config.Env == "production" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}

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

// clientIP returns the address of the client that sent r (see
// clientip.FromRequest, with Config.ProxyHops). If the address taken from
// X-Forwarded-For isn't public, it logs a warning once per Server: that is
// what an extra proxy hop inside the host's network looks like.
func (s *Server) clientIP(r *http.Request) string {
	ip, notPublic := clientip.FromRequest(r, s.config.ProxyHops)
	if notPublic {
		s.proxyHopsWarning.Do(func() {
			entries := strings.Count(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",") + 1
			s.logger.Warn("client IP from X-Forwarded-For is not a public address; check -proxy-hops", "ip", ip, "entries", entries, "proxy_hops", s.config.ProxyHops)
		})
	}
	return ip
}

type contextKey string

const authenticatedAdminContextKey = contextKey("authenticatedAdmin")

// authenticate loads the logged-in admin, if any, into the request's
// context. A session whose admin no longer exists is logged out.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.sessionManager.GetString(r.Context(), sessionAdminID)
		if id == "" {
			next.ServeHTTP(w, r)
			return
		}
		admin, err := s.services.Admins.Get(r.Context(), id)
		if errors.Is(err, rss.ErrNoRecord) {
			s.sessionManager.Remove(r.Context(), sessionAdminID)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), authenticatedAdminContextKey, admin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticatedAdmin returns the logged-in admin, set by authenticate. ok is
// false for anonymous requests.
func (s *Server) authenticatedAdmin(r *http.Request) (rss.Admin, bool) {
	admin, ok := r.Context().Value(authenticatedAdminContextKey).(rss.Admin)
	return admin, ok
}

// requireAuthentication sends anonymous requests to /login, which brings a
// GET back to its page once the admin has logged in.
func (s *Server) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authenticatedAdmin(r); !ok {
			if r.Method == http.MethodGet {
				s.sessionManager.Put(r.Context(), sessionReturnTo, r.URL.RequestURI())
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRecentCode guards a destructive action: when the admin's last code
// is older than recentCodeWindow, it asks for one again at /login/code,
// which then returns to the request's path. Put it, inside
// requireAuthentication, on both the action's page (GET) and its form
// (POST) at the same path, so the admin returns to the page and submits
// again.
func (s *Server) requireRecentCode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := time.Unix(s.sessionManager.GetInt64(r.Context(), sessionCodeAt), 0)
		if s.now().Sub(at) > recentCodeWindow {
			back := r.URL.Path
			if r.Method == http.MethodGet {
				back = r.URL.RequestURI()
			}
			http.Redirect(w, r, "/login/code?next="+url.QueryEscape(back), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
