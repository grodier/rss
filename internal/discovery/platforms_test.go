package discovery

import (
	"net/url"
	"slices"
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
		if (len(p.aliases) > 0) != (p.mainHost != "") || (p.mainHost != "" && keyHost(&url.URL{Host: p.mainHost}) != p.host) {
			t.Errorf("host %q: aliases %q with main host %q, want both or neither, the main host keying as the host", p.host, p.aliases, p.mainHost)
		}
		for _, a := range p.aliases {
			if a != strings.ToLower(a) || strings.HasPrefix(a, "www.") || seen[a] || platformsByHost[a].host != "" {
				t.Errorf("alias %q: want a lowercase host without a leading www., listed once, without an entry of its own", a)
			}
			seen[a] = true
		}
	}
}

func TestPlatformFeeds(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"https://medium.com/@Alice", []string{"https://medium.com/feed/@Alice"}},
		{"https://www.youtube.com/channel/UCabc", []string{"https://www.youtube.com/feeds/videos.xml?channel_id=UCabc"}},
		{"https://www.youtube.com/@handle", nil},
		{"https://www.youtube.com/c/name", nil},
		{"https://www.youtube.com/user/name", nil},
		{"https://www.reddit.com/r/golang", []string{"https://www.reddit.com/r/golang/.rss"}},
		{"https://reddit.com/u/spez", []string{"https://www.reddit.com/user/spez/.rss"}},
		{"https://github.com/owner/repo", []string{
			"https://github.com/owner/repo/releases.atom",
			"https://github.com/owner/repo/tags.atom",
			"https://github.com/owner/repo/commits.atom",
		}},
		{"https://github.com/owner", []string{"https://github.com/owner.atom"}},
		{"https://bsky.app/profile/alice.bsky.social", []string{"https://bsky.app/profile/alice.bsky.social/rss"}},
		{"https://mastodon.social/@bob", []string{"https://mastodon.social/@bob.rss"}},
		{"https://mastodon.social/@bob/123", []string{"https://mastodon.social/@bob.rss"}},
		// The platforms' own pages and ordinary sites.
		{"https://medium.com/", nil},
		{"https://github.com/", nil},
		{"https://github.com/explore", nil},
		{"https://example.com/blog", nil},
		{"https://example.com/", nil},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		got := platformFeeds(SiteRoot(u), SiteKey(u))
		if !slices.Equal(got, tt.want) {
			t.Errorf("platformFeeds(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
