package server

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name       string
		trusted    []netip.Prefix
		remoteAddr string
		xff        []string
		want       string
	}{
		{"no port", nil, "192.0.2.1", nil, "192.0.2.1"},
		{"ipv6 remote", nil, "[2001:db8::1]:1234", nil, "2001:db8::1"},
		{"no trusted proxies ignores header", nil, "192.0.2.1:1234", []string{"1.2.3.4"}, "192.0.2.1"},
		{"untrusted remote ignores header", trusted, "203.0.113.1:5000", []string{"1.2.3.4"}, "203.0.113.1"},
		{"trusted remote", trusted, "10.1.2.3:5000", []string{"1.2.3.4"}, "1.2.3.4"},
		{"forged left entry", trusted, "10.1.2.3:5000", []string{"9.9.9.9, 1.2.3.4"}, "1.2.3.4"},
		{"trusted right entry", trusted, "10.1.2.3:5000", []string{"1.2.3.4, 10.0.0.7"}, "1.2.3.4"},
		{"two header lines", trusted, "10.1.2.3:5000", []string{"9.9.9.9", "1.2.3.4"}, "1.2.3.4"},
		{"no header", trusted, "10.1.2.3:5000", nil, "10.1.2.3"},
		{"unparsable entry", trusted, "10.1.2.3:5000", []string{"1.2.3.4, nonsense"}, "10.1.2.3"},
		{"ipv6 client", trusted, "10.1.2.3:5000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"ipv4-mapped client", trusted, "10.1.2.3:5000", []string{"::ffff:1.2.3.4"}, "1.2.3.4"},
		{"all trusted", trusted, "10.1.2.3:5000", []string{"10.0.0.8, 10.0.0.7"}, "10.0.0.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", TrustedProxies: tt.trusted}, Services{})
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
