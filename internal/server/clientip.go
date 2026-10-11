package server

import (
	"net"
	"net/http"
	"net/netip"
)

// clientIP returns the address of the client that sent r, without the port.
// It is the only code that reads r.RemoteAddr: when the app runs behind a
// reverse proxy, this is where the proxy's forwarded header will be read
// (see "Client IP behind a proxy" in docs/todo.md).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
