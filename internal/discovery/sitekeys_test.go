package discovery

import "testing"

func TestSiteKey(t *testing.T) {
	tests := []struct{ in, want string }{
		// A host's own site.
		{"https://www.Example.com:443/a", "example.com"},
		{"https://example.com/", "example.com"},
		{"https://blog.example.com/", "blog.example.com"},
		{"http://www2.example.com", "www2.example.com"},
		{"https://medium.com/", "medium.com"},
		{"https://medium.com/feed", "medium.com"},
		{"https://github.com/explore", "github.com"},
		{"https://github.com/Orgs/x", "github.com"},
		{"https://www.reddit.com/", "reddit.com"},
		{"https://www.youtube.com/watch?v=x", "youtube.com"},
		{"https://www.youtube.com/feeds/videos.xml?playlist_id=PL1", "youtube.com"},
		{"https://bsky.app/", "bsky.app"},
		{"https://example.com/@", "example.com"},
		{"https://example.com/@.rss", "example.com"},
		{"https://example.com/a/@alice", "example.com"},

		// Any other host: @name (Mastodon and the fediverse, Threads, …).
		{"https://mastodon.social/@Bob", "mastodon.social/@bob"},
		{"https://mastodon.social/@bob/111", "mastodon.social/@bob"},
		{"https://mastodon.social/@bob.rss", "mastodon.social/@bob"},
		{"https://mastodon.social/@bob.atom", "mastodon.social/@bob"},
		{"https://www.threads.net/@alice", "threads.net/@alice"},

		// medium.com
		{"https://medium.com/@Alice", "medium.com/@alice"},
		{"https://medium.com/@alice/a-post-123", "medium.com/@alice"},
		{"https://medium.com/feed/@alice", "medium.com/@alice"},
		{"https://medium.com/@alice?source=rss", "medium.com/@alice"},

		// youtube.com
		{"https://www.youtube.com/@GoogleDevelopers", "youtube.com/@googledevelopers"},
		{"https://www.youtube.com/@googledevelopers/videos", "youtube.com/@googledevelopers"},
		{"https://www.youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw", "youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw"},
		{"https://www.youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw/videos", "youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw"},
		{"https://www.youtube.com/feeds/videos.xml?channel_id=UC_x5XG1OV2P6uZZ5FSM9Ttw", "youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw"},
		{"https://www.youtube.com/c/Name", "youtube.com/c/name"},
		{"https://www.youtube.com/c/name/videos", "youtube.com/c/name"},
		{"https://www.youtube.com/user/Name", "youtube.com/user/name"},
		{"https://www.youtube.com/user/name/videos", "youtube.com/user/name"},

		// reddit.com
		{"https://www.reddit.com/r/golang", "reddit.com/r/golang"},
		{"https://www.reddit.com/r/Golang/comments/abc/x/", "reddit.com/r/golang"},
		{"https://www.reddit.com/r/golang/.rss", "reddit.com/r/golang"},
		{"https://www.reddit.com/r/golang.rss", "reddit.com/r/golang"},
		{"https://www.reddit.com/user/spez", "reddit.com/user/spez"},
		{"https://www.reddit.com/user/spez/.rss", "reddit.com/user/spez"},
		{"https://www.reddit.com/u/spez", "reddit.com/user/spez"},

		// github.com
		{"https://github.com/Golang/Go", "github.com/golang/go"},
		{"https://github.com/golang/go/releases.atom", "github.com/golang/go"},
		{"https://github.com/golang/go/commits/master.atom", "github.com/golang/go"},
		{"https://github.com/golang/go/tags.atom", "github.com/golang/go"},
		{"https://github.com/rsc", "github.com/rsc"},
		{"https://github.com/rsc.atom", "github.com/rsc"},

		// bsky.app
		{"https://bsky.app/profile/Alice.bsky.social", "bsky.app/profile/alice.bsky.social"},
		{"https://bsky.app/profile/alice.bsky.social/post/3k", "bsky.app/profile/alice.bsky.social"},
		{"https://bsky.app/profile/alice.bsky.social/rss", "bsky.app/profile/alice.bsky.social"},
		{"https://bsky.app/@alice", "bsky.app"}, // a host with its own rule uses only that rule
	}
	for _, tt := range tests {
		if got := SiteKey(mustParse(t, tt.in)); got != tt.want {
			t.Errorf("SiteKey(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestSiteRoot(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://www.Example.com:443/a?x=1", "https://www.example.com/"},
		{"http://example.com", "http://example.com/"},
		{"https://medium.com/@alice/a-post", "https://medium.com/@alice"},
		{"https://medium.com/feed/@alice", "https://medium.com/@alice"},
		{"https://medium.com/@alice/", "https://medium.com/@alice"},
		{"https://mastodon.social/@bob.rss", "https://mastodon.social/@bob"},
		{"https://www.youtube.com/feeds/videos.xml?channel_id=UCabc", "https://www.youtube.com/channel/UCabc"},
		{"https://www.reddit.com/r/golang/.rss", "https://www.reddit.com/r/golang"},
		{"https://github.com/golang/go/releases.atom", "https://github.com/golang/go"},
		{"https://bsky.app/profile/alice.bsky.social/rss", "https://bsky.app/profile/alice.bsky.social"},
	}
	for _, tt := range tests {
		if got := SiteRoot(mustParse(t, tt.in)).String(); got != tt.want {
			t.Errorf("SiteRoot(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsHomePage(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"https://example.com/", true},
		{"https://example.com", true},
		{"https://www.example.com/", true},
		{"https://example.com/?ref=x", false},
		{"https://example.com/about", false},
		{"https://example.com/feed.xml", false},
		{"https://medium.com/@alice", true},
		{"https://medium.com/@alice/", true},
		{"https://www.medium.com/@Alice", true},
		{"https://medium.com/@alice?source=rss", false},
		{"https://medium.com/@alice/a-post", false},
		{"https://medium.com/feed/@alice", false},
		{"https://mastodon.social/@bob.rss", false},
		{"https://www.youtube.com/feeds/videos.xml?channel_id=UCabc", false},
		{"https://www.youtube.com/channel/UCabc", true},
	}
	for _, tt := range tests {
		if got := IsHomePage(mustParse(t, tt.in)); got != tt.want {
			t.Errorf("IsHomePage(%q) = %v; want %v", tt.in, got, tt.want)
		}
	}
}

func TestKeyHost(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "example.com"},
		{"medium.com/@alice", "medium.com"},
		{"youtube.com/channel/UCabc", "youtube.com"},
	}
	for _, tt := range tests {
		if got := KeyHost(tt.in); got != tt.want {
			t.Errorf("KeyHost(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestFeedSiteKey(t *testing.T) {
	tests := []struct {
		name, siteURL, feedURL, key, root string
	}{
		{"from the link", "https://medium.com/@alice?source=rss----abc", "https://medium.com/feed/@alice", "medium.com/@alice", "https://medium.com/@alice"},
		{"from the URL without a link", "", "https://medium.com/feed/@alice", "medium.com/@alice", "https://medium.com/@alice"},
		{"an ordinary feed", "https://www.example.com/blog/", "https://www.example.com/feed.xml", "example.com", "https://www.example.com/"},
		{"a link that isn't an address", "not a link", "https://example.com/feed.xml", "example.com", "https://example.com/"},
		{"a link to an invalid host", "http://www.newsletters/", "https://example.com/feed.xml", "example.com", "https://example.com/"},
		{"neither", "", "", "", ""},
	}
	for _, tt := range tests {
		key, root := FeedSiteKey(tt.siteURL, tt.feedURL)
		if key != tt.key || root != tt.root {
			t.Errorf("%s: FeedSiteKey(%q, %q) = %q, %q; want %q, %q", tt.name, tt.siteURL, tt.feedURL, key, root, tt.key, tt.root)
		}
	}
}
