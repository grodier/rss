package server

import (
	"net/http"
)

// clientIP returns the address of the client that sent r (see
// httpserve.ClientIP, with Config.ProxyHops).
func (s *Server) clientIP(r *http.Request) string {
	return s.clientIPs.Of(r)
}
