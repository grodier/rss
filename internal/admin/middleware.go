package admin

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/grodier/rss/internal/clientip"
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
