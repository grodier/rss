package server

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIP(t *testing.T) {
	const proxy = "10.1.2.3:5000"
	tests := []struct {
		name       string
		hops       int
		remoteAddr string
		xff        []string
		want       string
	}{
		{"no port", 0, "192.0.2.1", nil, "192.0.2.1"},
		{"ipv6 remote", 0, "[2001:db8::1]:1234", nil, "2001:db8::1"},
		{"zero hops ignores header", 0, proxy, []string{"203.0.113.9"}, "10.1.2.3"},
		{"one hop", 1, proxy, []string{"198.51.100.7"}, "198.51.100.7"},
		{"forged left entry", 1, proxy, []string{"203.0.113.9, 198.51.100.7"}, "198.51.100.7"},
		{"two header lines", 1, proxy, []string{"203.0.113.9", "198.51.100.7"}, "198.51.100.7"},
		{"no header", 1, proxy, nil, "10.1.2.3"},
		{"unparsable entry", 1, proxy, []string{"garbage"}, "10.1.2.3"},
		{"unparsable client entry", 1, proxy, []string{"198.51.100.7, garbage"}, "10.1.2.3"},
		{"two hops", 2, proxy, []string{"203.0.113.9, 198.51.100.7, 10.0.0.5"}, "198.51.100.7"},
		{"fewer entries than hops", 2, proxy, []string{"198.51.100.7"}, "10.1.2.3"},
		{"ipv4-mapped client", 1, proxy, []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"ipv6 client", 1, proxy, []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", ProxyHops: tt.hops}, Services{})
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := s.clientIP(req); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

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

func TestIPRateLimitKey(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"192.0.2.1", "192.0.2.1"},
		{"::ffff:192.0.2.1", "192.0.2.1"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"},
		{"not-an-ip", "not-an-ip"},
	}
	for _, tt := range tests {
		if got := ipRateLimitKey(tt.ip); got != tt.want {
			t.Errorf("ipRateLimitKey(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}

	if a, b := ipRateLimitKey("2001:db8:1:3::1"), ipRateLimitKey("2001:db8:1:2::1"); a == b {
		t.Errorf("addresses in different /64s share key %q", a)
	}
}
