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
// If the connection comes from one of Config.TrustedProxies, the client is the
// rightmost X-Forwarded-For entry that isn't itself a trusted proxy: each
// trusted proxy appends the address it received the request from, so only
// entries to the right of the first untrusted one can be believed, and the
// leftmost entry is whatever the client chose to send. If every entry is
// trusted, the leftmost is returned. A missing header or an unparsable entry
// falls back to the connection's address. Otherwise the header is ignored.
// See "Client IP" under Deployment in docs/decisions.md.
func (s *Server) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !s.isTrustedProxy(remote) {
		return remote
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote
	}
	entries := strings.Split(strings.Join(values, ","), ",")
	var client netip.Addr
	for i := len(entries) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(entries[i]))
		if err != nil {
			return remote
		}
		client = addr.Unmap()
		if !s.trusts(client) {
			break
		}
	}
	return client.String()
}

func (s *Server) isTrustedProxy(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && s.trusts(addr.Unmap())
}

func (s *Server) trusts(addr netip.Addr) bool {
	for _, p := range s.config.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
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
