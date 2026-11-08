// Package clientip finds the address of the client that sent a request,
// behind a known number of reverse proxies. Both servers (internal/server,
// internal/admin) use it.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// FromRequest returns the address of the client that sent r, without the
// port. It is the only code that reads r.RemoteAddr.
//
// proxyHops is how many reverse proxies stand between the client and the
// app, each appending the address it received the request from to
// X-Forwarded-For. The client is therefore the entry proxyHops places from the
// right: the entries to its left are whatever the client chose to send, so
// they can be forged. With proxyHops 0 the header is ignored. A missing
// header, fewer entries than proxyHops, or an unparsable entry falls back to
// the connection's address. This is only sound when the proxies are the only
// way to reach the app.
//
// notPublic reports that the address was taken from the header and isn't
// public: that is what an extra proxy hop inside the host's network looks
// like, so the servers log a warning once. An extra public hop, such as a CDN,
// can't be told apart from a client. Fallbacks never report it. See "Client
// IP" under Deployment in docs/decisions.md.
func FromRequest(r *http.Request, proxyHops int) (ip string, notPublic bool) {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if proxyHops == 0 {
		return remote, false
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote, false
	}
	entries := strings.Split(strings.Join(values, ","), ",")
	if len(entries) < proxyHops {
		return remote, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(entries[len(entries)-proxyHops]))
	if err != nil {
		return remote, false
	}
	addr = addr.Unmap()
	return addr.String(), !addr.IsGlobalUnicast() || addr.IsPrivate()
}
