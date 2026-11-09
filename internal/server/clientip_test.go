package server

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPWarnsOnPrivateAddress(t *testing.T) {
	const warning = "client IP from X-Forwarded-For is not a public address"
	// newServer returns a server with ProxyHops 1 that logs to the returned
	// buffer.
	newServer := func(t *testing.T) (*Server, *bytes.Buffer) {
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", ProxyHops: 1}, Services{})
		var buf bytes.Buffer
		s.logger = slog.New(slog.NewTextHandler(&buf, nil))
		return s, &buf
	}
	clientIP := func(s *Server, xff string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.1.2.3:5000"
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return s.clientIP(req)
	}

	s, buf := newServer(t)
	if got := clientIP(s, "10.0.0.5"); got != "10.0.0.5" {
		t.Errorf("clientIP() = %q, want %q", got, "10.0.0.5")
	}
	if n := strings.Count(buf.String(), warning); n != 1 {
		t.Fatalf("after 10.0.0.5: %d warnings, want 1; log:\n%s", n, buf)
	}
	if !strings.Contains(buf.String(), "ip=10.0.0.5") {
		t.Errorf("warning doesn't name the address; log:\n%s", buf)
	}
	clientIP(s, "192.168.1.1")
	if n := strings.Count(buf.String(), warning); n != 1 {
		t.Errorf("after a second private address: %d warnings, want 1; log:\n%s", n, buf)
	}

	s, buf = newServer(t)
	clientIP(s, "198.51.100.7")
	if buf.Len() != 0 {
		t.Errorf("public address logged:\n%s", buf)
	}

	s, buf = newServer(t)
	if got := clientIP(s, ""); got != "10.1.2.3" {
		t.Errorf("clientIP() without header = %q, want %q", got, "10.1.2.3")
	}
	if buf.Len() != 0 {
		t.Errorf("fallback logged:\n%s", buf)
	}
}
