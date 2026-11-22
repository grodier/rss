package discovery

import "testing"

func TestOwnName(t *testing.T) {
	// Titles from the platform's page.
	tests := []struct{ key, title, want string }{
		{"medium.com/@alice", "Alice – Medium", "Alice"},
		{"medium.com/@alice", "Alice - Medium", "Alice"},
		{"medium.com/@alice", "Stories by Alice on Medium", "Alice"},
		{"mastodon.social/@bob", "Bob (@bob@mastodon.social) - Mastodon", "Bob"},
		{"bsky.app/profile/cy.bsky.social", "Cy (@cy.bsky.social) — Bluesky", "Cy"},
		{"youtube.com/@chan", "Channel name - YouTube", "Channel name"},
		{"github.com/owner/repo", "owner/repo: a tool - fast - GitHub", "owner/repo"},
		{"github.com/owner/repo", "GitHub - owner/repo: a tool", "owner/repo"},
		{"github.com/owner", "owner (Owner Name) · GitHub", "owner (Owner Name)"},
		// An owner's page has no description to cut.
		{"github.com/acme", "acme (Acme: Tools) · GitHub", "acme (Acme: Tools)"},
		{"medium.com/@alice", "  Alice – Medium\n", "Alice"},
		{"reddit.com/r/golang", "r/golang | Pipe", "r/golang"},
		// Unchanged: no suffix, nothing left, or a key without a path.
		{"medium.com/@alice", "Alice", "Alice"},
		{"medium.com/@alice", "", ""},
		{"example.com/@x", "- Medium", "- Medium"},
		{"example.com", "Notes - Medium", "Notes - Medium"},
		{"example.com", "Stories by Alice on Medium", "Stories by Alice on Medium"},
	}
	for _, tt := range tests {
		if got := OwnName(tt.key, tt.title, false); got != tt.want {
			t.Errorf("OwnName(%q, %q, page) = %q, want %q", tt.key, tt.title, got, tt.want)
		}
	}

	// Titles from a feed are the feed's own name: only Medium's goes.
	feeds := []struct{ key, title, want string }{
		{"medium.com/@alice", "Stories by Alice on Medium", "Alice"},
		{"youtube.com/channel/UCx", "Kurzgesagt – In a Nutshell", "Kurzgesagt – In a Nutshell"},
		{"mastodon.social/@jane", "Jane | Rust dev", "Jane | Rust dev"},
		{"github.com/owner/repo", "Release notes from repo: v2", "Release notes from repo: v2"},
		{"example.com", "Stories by Alice on Medium", "Stories by Alice on Medium"},
	}
	for _, tt := range feeds {
		if got := OwnName(tt.key, tt.title, true); got != tt.want {
			t.Errorf("OwnName(%q, %q, feed) = %q, want %q", tt.key, tt.title, got, tt.want)
		}
	}
}

func TestPlatform(t *testing.T) {
	tests := []struct{ key, want string }{
		{"medium.com/@alice", "Medium"},
		{"youtube.com/channel/UCx", "YouTube"},
		{"reddit.com/r/golang", "Reddit"},
		{"github.com/owner/repo", "GitHub"},
		{"bsky.app/profile/a.bsky.social", "Bluesky"},
		{"mastodon.social/@bob", "mastodon.social"},
		{"medium.com", ""},
		{"example.com", ""},
	}
	for _, tt := range tests {
		if got := Platform(tt.key); got != tt.want {
			t.Errorf("Platform(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
