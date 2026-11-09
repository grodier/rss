package server

import (
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/clientip"
)

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
