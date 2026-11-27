package discovery

import (
	"strings"
	"testing"
)

func TestPlatforms(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range platforms {
		if p.host != strings.ToLower(p.host) || strings.HasPrefix(p.host, "www.") || p.host == "" {
			t.Errorf("host %q: want a non-empty, lowercase host without a leading www.", p.host)
		}
		if seen[p.host] {
			t.Errorf("host %q: listed twice", p.host)
		}
		seen[p.host] = true
		if p.name == "" {
			t.Errorf("host %q: no name", p.host)
		}
		if got := Platform(p.host + "/x"); got != p.name {
			t.Errorf("Platform(%q) = %q, want %q", p.host+"/x", got, p.name)
		}
	}
}
