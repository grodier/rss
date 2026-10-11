package server

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       string
	}{
		{"192.0.2.1:1234", "192.0.2.1"},
		{"[2001:db8::1]:1234", "2001:db8::1"},
		{"192.0.2.1", "192.0.2.1"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = tt.remoteAddr
		if got := clientIP(req); got != tt.want {
			t.Errorf("clientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
		}
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
