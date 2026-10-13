package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP returns the address of the client that sent r, without the port.
// It is the only code that reads r.RemoteAddr.
//
// Config.ProxyHops is how many reverse proxies stand between the client and
// the app, each appending the address it received the request from to
// X-Forwarded-For. The client is therefore the entry ProxyHops places from the
// right: the entries to its left are whatever the client chose to send, so
// they can be forged. With ProxyHops 0 the header is ignored. A missing
// header, fewer entries than ProxyHops, or an unparsable entry falls back to
// the connection's address. This is only sound when the proxies are the only
// way to reach the app.
//
// If the address taken from the header isn't public, clientIP logs a warning
// once per Server: that is what an extra proxy hop added by the host looks
// like. Fallbacks never warn. See "Client IP" under Deployment in
// docs/decisions.md.
func (s *Server) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if s.config.ProxyHops == 0 {
		return remote
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote
	}
	entries := strings.Split(strings.Join(values, ","), ",")
	if len(entries) < s.config.ProxyHops {
		return remote
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(entries[len(entries)-s.config.ProxyHops]))
	if err != nil {
		return remote
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		s.proxyHopsWarning.Do(func() {
			s.logger.Warn("client IP from X-Forwarded-For is not a public address; check -proxy-hops", "ip", addr.String(), "entries", len(entries), "proxy_hops", s.config.ProxyHops)
		})
	}
	return addr.String()
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
