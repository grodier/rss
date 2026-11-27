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
		{"npr. org", "https://npr.org/"},
		{"npr .org", "https://npr.org/"},
		{"NPR. Org", "https://npr.org/"},
		{"https: //npr.org", "https://npr.org/"},
		{"npr.org/ news", "https://npr.org/news"},
		// Platforms' alias hosts and Tumblr blogs' addresses on tumblr.com.
		{"m.youtube.com/@name", "https://www.youtube.com/@name"},
		{"old.reddit.com/r/golang", "https://www.reddit.com/r/golang"},
		{"http://new.reddit.com/r/golang/?x=1", "http://www.reddit.com/r/golang/?x=1"},
		{"tumblr.com/staff", "https://staff.tumblr.com/"},
		{"https://www.tumblr.com/Staff/123/a-post", "https://staff.tumblr.com/"},
		{"tumblr.com/blog/view/staff", "https://staff.tumblr.com/"},
		{"tumblr.com/explore", "https://tumblr.com/explore"},
		{"tumblr.com/blog", "https://tumblr.com/blog"},
		{"tumblr.com/", "https://tumblr.com/"},
		{"tumblr.com/bad_name", "https://tumblr.com/bad_name"},
		{"tumblr.com/staff/tagged/news/rss", "https://tumblr.com/staff/tagged/news/rss"},
		{"tumblr.com/blog/view/staff/123", "https://tumblr.com/blog/view/staff/123"},
		{"https://old.reddit.com:8443/r/golang", "https://www.reddit.com:8443/r/golang"},
		{"music.youtube.com/x", "https://music.youtube.com/x"},
		{"staff.tumblr.com", "https://staff.tumblr.com/"},

		{"hacker news", ""},
		{"example.com/a b", ""},
		{"example.com/a\tb", ""},
		{"news on npr.org", ""},
		{"the verge", ""},
		{"npr org", ""},
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
		// Newsletters: their feeds' URLs, and hosts that would key to their
		// internal site (rss.NewsletterSiteHost) or another single label.
		{"newsletter:0b7c6f9e-2f4d-4a0e-9d55-3c1f2a8b9e71", ""},
		{"newsletters", ""},
		{"www.newsletters", ""},
		{"https://WWW.newsletters/", ""},
		{"www.com", ""},
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

func TestCanonicalFeedURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"HTTPS://Example.com:443/feed.xml#x", "https://example.com/feed.xml"},
		{"http://Example.com:80/rss", "http://example.com/rss"},
		{"http://example.com:443/rss", "http://example.com:443/rss"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com?a=1", "https://example.com/?a=1"},
		{"https://example.com/Feed?A=1", "https://example.com/Feed?A=1"},
		{"https://old.reddit.com/r/golang/.rss", "https://www.reddit.com/r/golang/.rss"},
		{"https://M.YouTube.com/feeds/videos.xml?channel_id=UCabc", "https://www.youtube.com/feeds/videos.xml?channel_id=UCabc"},
		{"https://staff.tumblr.com/rss", "https://staff.tumblr.com/rss"},
		{"https://www.tumblr.com/staff", "https://www.tumblr.com/staff"}, // only alias hosts: feeds are on the blog's host
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
