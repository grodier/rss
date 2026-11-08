package server

import (
	"net/http"
	"net/netip"
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

// ipRateLimitKey returns the rate-limit key for ip: the address itself for
// IPv4 (including IPv4-mapped IPv6), and its /64 prefix for IPv6. An
// unparsable ip is returned unchanged.
func ipRateLimitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}
