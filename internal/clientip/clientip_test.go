package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	const proxy = "10.1.2.3:5000"
	tests := []struct {
		name          string
		hops          int
		remoteAddr    string
		xff           []string
		want          string
		wantNotPublic bool
	}{
		{"no port", 0, "192.0.2.1", nil, "192.0.2.1", false},
		{"ipv6 remote", 0, "[2001:db8::1]:1234", nil, "2001:db8::1", false},
		{"zero hops ignores header", 0, proxy, []string{"203.0.113.9"}, "10.1.2.3", false},
		{"one hop", 1, proxy, []string{"198.51.100.7"}, "198.51.100.7", false},
		{"forged left entry", 1, proxy, []string{"203.0.113.9, 198.51.100.7"}, "198.51.100.7", false},
		{"two header lines", 1, proxy, []string{"203.0.113.9", "198.51.100.7"}, "198.51.100.7", false},
		{"no header", 1, proxy, nil, "10.1.2.3", false},
		{"unparsable entry", 1, proxy, []string{"garbage"}, "10.1.2.3", false},
		{"unparsable client entry", 1, proxy, []string{"198.51.100.7, garbage"}, "10.1.2.3", false},
		{"two hops", 2, proxy, []string{"203.0.113.9, 198.51.100.7, 10.0.0.5"}, "198.51.100.7", false},
		{"fewer entries than hops", 2, proxy, []string{"198.51.100.7"}, "10.1.2.3", false},
		{"ipv4-mapped client", 1, proxy, []string{"::ffff:198.51.100.7"}, "198.51.100.7", false},
		{"ipv6 client", 1, proxy, []string{"2001:db8::1"}, "2001:db8::1", false},
		{"private client", 1, proxy, []string{"10.0.0.5"}, "10.0.0.5", true},
		{"loopback client", 1, proxy, []string{"127.0.0.1"}, "127.0.0.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			got, notPublic := FromRequest(req, tt.hops)
			if got != tt.want || notPublic != tt.wantNotPublic {
				t.Errorf("FromRequest() = %q, %v; want %q, %v", got, notPublic, tt.want, tt.wantNotPublic)
			}
		})
	}
}
