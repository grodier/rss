package discovery

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseInput(t *testing.T) {
	long := "example.com/" + strings.Repeat("a", maxInputLen-len("example.com/"))

	tests := []struct {
		in   string
		want string // "" means ErrNotURL
	}{
		{"example.com", "https://example.com/"},
		{"  Example.COM  ", "https://example.com/"},
		{"www.example.com/blog?x=1#top", "https://www.example.com/blog?x=1"},
		{"http://example.com:80/", "http://example.com/"},
		{"https://example.com:443", "https://example.com/"},
		{"https://example.com:8443/a", "https://example.com:8443/a"},
		{"HTTPS://Example.com", "https://example.com/"},
		{"xn--bcher-kva.example", "https://xn--bcher-kva.example/"},
		{"blog.example.org/Posts/A", "https://blog.example.org/Posts/A"},
		{long, "https://" + long},

		{"hacker news", ""},
		{"example.com/a b", ""},
		{"example.com/a\tb", ""},
		{"localhost", ""},
		{"news", ""},
		{"", ""},
		{"   ", ""},
		{"ftp://example.com", ""},
		{"javascript:alert(1)", ""},
		{"mailto:me@example.com", ""},
		{"http://user:pw@example.com", ""},
		{"192.168.0.1", ""},
		{"http://192.168.0.1/", ""},
		{"http://[::1]/", ""},
		{"-bad-.com", ""},
		{"example.c0m", ""},
		{"example.c", ""},
		{"example..com", ""},
		{"example.com.", ""},
		{"exa_mple.com", ""},
		{strings.Repeat("a", 64) + ".com", ""},
		{long + "a", ""},
	}
	for _, tt := range tests {
		got, err := ParseInput(tt.in)
		if tt.want == "" {
			if !errors.Is(err, ErrNotURL) {
				t.Errorf("ParseInput(%q) = %v, %v; want ErrNotURL", tt.in, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseInput(%q) error: %v", tt.in, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("ParseInput(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestLooksLikeURL(t *testing.T) {
	for _, in := range []string{"example.com", "HTTPS://Example.com", "hacker news", "localhost", "ftp://example.com", ""} {
		_, err := ParseInput(in)
		if got, want := LooksLikeURL(in), err == nil; got != want {
			t.Errorf("LooksLikeURL(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestSiteKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://www.Example.com:443/a", "example.com"},
		{"https://example.com/", "example.com"},
		{"https://blog.example.com/", "blog.example.com"},
		{"http://www2.example.com", "www2.example.com"},
	}
	for _, tt := range tests {
		if got := SiteKey(mustParse(t, tt.in)); got != tt.want {
			t.Errorf("SiteKey(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestCanonicalFeedURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"HTTPS://Example.com:443/feed.xml#x", "https://example.com/feed.xml"},
		{"http://Example.com:80/rss", "http://example.com/rss"},
		{"http://example.com:443/rss", "http://example.com:443/rss"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com?a=1", "https://example.com/?a=1"},
		{"https://example.com/Feed?A=1", "https://example.com/Feed?A=1"},
	}
	for _, tt := range tests {
		u := mustParse(t, tt.in)
		if got := CanonicalFeedURL(u); got != tt.want {
			t.Errorf("CanonicalFeedURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
		if u.String() != mustParse(t, tt.in).String() {
			t.Errorf("CanonicalFeedURL(%q) modified its argument to %q", tt.in, u)
		}
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", s, err)
	}
	return u
}
