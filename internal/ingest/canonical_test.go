package ingest

import "testing"

func TestCanonicalURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"scheme and host lowercased, path case kept", "HTTPS://Example.COM/Post", "https://example.com/Post"},
		{"default https port removed", "https://example.com:443/a", "https://example.com/a"},
		{"default http port removed", "http://example.com:80/a", "http://example.com/a"},
		{"other port kept", "https://example.com:8080/a", "https://example.com:8080/a"},
		{"https port kept for http", "http://example.com:443/a", "http://example.com:443/a"},
		{"fragment dropped", "https://example.com/a#section", "https://example.com/a"},
		{"tracking parameters removed", "https://example.com/a?utm_source=x&id=7&utm_medium=y", "https://example.com/a?id=7"},
		{"query dropped when all removed", "https://example.com/a?fbclid=1", "https://example.com/a"},
		{"every listed parameter removed", "https://example.com/a?fbclid=1&gclid=2&dclid=3&msclkid=4&mc_cid=5&mc_eid=6&igshid=7&_hsenc=8&_hsmi=9&utm_campaign=10", "https://example.com/a"},
		{"query order kept", "https://example.com/a?b=2&a=1", "https://example.com/a?b=2&a=1"},
		{"query encoding kept", "https://example.com/a?q=a%20b", "https://example.com/a?q=a%20b"},
		{"parameter names case-sensitive", "https://example.com/a?UTM_source=x", "https://example.com/a?UTM_source=x"},
		{"trailing slash kept", "https://example.com/post/", "https://example.com/post/"},
		{"no trailing slash kept", "https://example.com/post", "https://example.com/post"},
		{"empty", "", ""},
		{"mailto", "mailto:x@example.com", ""},
		{"ftp", "ftp://example.com/a", ""},
		{"unparseable", "://bad", ""},
		{"no host", "https:///a", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalURL(tt.in); got != tt.want {
				t.Errorf("CanonicalURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
