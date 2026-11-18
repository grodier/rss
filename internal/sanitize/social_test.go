package sanitize

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestXPost(t *testing.T) {
	tests := []struct {
		name string
		raw  string // the embed's markup; its first element is read
		ok   bool
		want socialPost
		text string // post.Text rendered, if any
	}{
		{
			name: "full embed",
			raw:  xEmbed,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1745101186790326273", Name: "Jane Doe", Handle: "jane", Time: time.Date(2024, 1, 10, 15, 12, 0, 0, time.UTC), Date: "January 10, 2024", Media: true},
			text: `<p lang="en" dir="ltr">Hello <a href="https://twitter.com/bob?ref_src=twsrc%5Etfw">@bob</a> see <a href="https://t.co/xyz">example.com/page</a></p>`,
		},
		{
			name: "video embed without a picture",
			raw:  `<blockquote class="twitter-video"><p>Watch</p>&mdash; Jane (@jane) <a href="https://twitter.com/jane/status/1">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1", Name: "Jane", Handle: "jane", Date: "May 2, 2025", Video: true},
			text: `<p>Watch</p>`,
		},
		{
			name: "picture only",
			raw:  `<blockquote class="twitter-tweet"><p><a href="https://t.co/a">pic.twitter.com/a</a></p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1", Name: "Jane", Handle: "jane", Date: "May 2, 2025", Media: true},
		},
		{
			name: "lazy embed",
			raw:  `<blockquote class="twitter-tweet" data-tweet-id="1745159068185526275"><a href="https://x.com/jane/status/1745159068185526275"></a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1745159068185526275", Handle: "jane", Time: time.Date(2024, 1, 10, 19, 2, 0, 0, time.UTC)},
		},
		{
			name: "lazy embed whose link text is the URL",
			raw:  `<blockquote class="twitter-tweet"><a href="https://mobile.twitter.com/jane/statuses/123/">https://twitter.com/jane/status/123</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/123", Handle: "jane"},
		},
		{
			name: "web status link",
			raw:  `<blockquote class="twitter-tweet"><a href="https://x.com/i/web/status/123">x</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/i/web/status/123"},
		},
		{
			name: "byline that doesn't match",
			raw:  `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane Doe <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1", Handle: "jane", Date: "May 2, 2025"},
			text: `<p>Hi</p>`,
		},
		{
			name: "name with parentheses",
			raw:  `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane (work) Doe (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "x", URL: "https://x.com/jane/status/1", Name: "Jane (work) Doe", Handle: "jane", Date: "May 2, 2025"},
			text: `<p>Hi</p>`,
		},
		{name: "no status link", raw: `<blockquote class="twitter-tweet"><a href="https://x.com/jane">x</a></blockquote>`},
		{name: "bad handle", raw: `<blockquote class="twitter-tweet"><a href="https://x.com/ja-ne/status/1">x</a></blockquote>`},
		{name: "16-character handle", raw: `<blockquote class="twitter-tweet"><a href="https://x.com/abcdefghijklmnop/status/1">x</a></blockquote>`},
		{name: "non-numeric ID", raw: `<blockquote class="twitter-tweet"><a href="https://x.com/jane/status/12a">x</a></blockquote>`},
		{name: "another host", raw: `<blockquote class="twitter-tweet"><a href="https://evil.example/jane/status/1">x</a></blockquote>`},
		{name: "relative link", raw: `<blockquote class="twitter-tweet"><a href="/jane/status/1">x</a></blockquote>`},
		{name: "no class", raw: `<blockquote><a href="https://x.com/jane/status/1">x</a></blockquote>`},
		{name: "not a blockquote", raw: `<div class="twitter-tweet"><a href="https://x.com/jane/status/1">x</a></div>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			post, ok := xPost(root.FirstChild)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			text := renderNode(t, post.Text)
			post.Text = nil
			if !post.Time.Equal(tt.want.Time) {
				t.Errorf("Time = %v, want %v", post.Time, tt.want.Time)
			}
			post.Time, tt.want.Time = time.Time{}, time.Time{}
			if post != tt.want {
				t.Errorf("post = %+v\nwant   %+v", post, tt.want)
			}
			if text != tt.text {
				t.Errorf("Text = %s\nwant   %s", text, tt.text)
			}
		})
	}
}

func TestXFramePost(t *testing.T) {
	tests := []struct {
		src  string
		ok   bool
		want string
	}{
		{"https://platform.twitter.com/embed/Tweet.html?id=123&dnt=true", true, "https://x.com/i/web/status/123"},
		{"https://platform.twitter.com/embed/Tweet.html?id=1745159068185526275", true, "https://x.com/i/web/status/1745159068185526275"},
		{"https://platform.twitter.com/embed/Tweet.html?id=12a", false, ""},
		{"https://platform.twitter.com/embed/Tweet.html", false, ""},
		{"https://platform.twitter.com/embed/Timeline.html?id=123", false, ""},
		{"https://evil.example/embed/Tweet.html?id=123", false, ""},
		{"javascript:alert(1)", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			root, err := parse(`<iframe src="` + tt.src + `"></iframe>`)
			if err != nil {
				t.Fatal(err)
			}
			post, ok := xFramePost(root.FirstChild)
			if ok != tt.ok || post.URL != tt.want {
				t.Errorf("xFramePost = %+v, %v; want URL %q, %v", post, ok, tt.want, tt.ok)
			}
			if ok && (post.Network != "x" || post.Text != nil || post.Handle != "" || post.Name != "" || post.Date != "" || post.Media || post.Video) {
				t.Errorf("xFramePost = %+v; want only Network, URL and Time", post)
			}
			if want := xTime(strings.TrimPrefix(tt.want, "https://x.com/i/web/status/")); ok && !post.Time.Equal(want) {
				t.Errorf("xFramePost Time = %v; want %v, the ID's", post.Time, want)
			}
		})
	}
}

func TestXTime(t *testing.T) {
	tests := []struct {
		id   string
		want time.Time
	}{
		{"1745101186790326273", time.Date(2024, 1, 10, 15, 12, 0, 0, time.UTC)},
		// The last post before snowflakes, and the first of them.
		{"29700859247", time.Time{}},
		{"29700859248", time.UnixMilli((29700859248 >> 22) + xEpoch).UTC()},
		{"20", time.Time{}},
		{"0", time.Time{}},
		// More than 2^64.
		{"99999999999999999999", time.Time{}},
		{"12a", time.Time{}},
		{"", time.Time{}},
		// A day and a minute from now.
		{strconv.FormatUint(uint64(time.Now().Add(24*time.Hour+time.Minute).UnixMilli()-xEpoch)<<22, 10), time.Time{}},
	}
	for _, tt := range tests {
		if got := xTime(tt.id); !got.Equal(tt.want) {
			t.Errorf("xTime(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestBlueskyPost(t *testing.T) {
	jan1 := time.Date(2025, 1, 1, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		ok   bool
		want socialPost
		text string
	}{
		{
			name: "full embed",
			raw:  blueskyEmbed,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227", Name: "Jane Doe", Handle: "jane.bsky.social", Time: jan1, Date: "January 1, 2025 at 3:00 PM", Media: true},
			text: `<p lang="en">The post&#39;s text</p>`,
		},
		{
			name: "media only",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p lang="en"><a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227">[image or embed]</a></p></blockquote>`,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227", Time: jan1, Media: true},
		},
		{
			name: "did:web and a key that isn't a TID",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:web:example.com/app.bsky.feed.post/self"><p>Hi</p>&mdash; Example (<a href="https://bsky.app/profile/example.com">@example.com</a>) <a href="https://bsky.app/profile/did:web:example.com/post/self">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/did:web:example.com/post/self", Name: "Example", Handle: "example.com", Date: "May 2, 2025"},
			text: `<p>Hi</p>`,
		},
		{
			name: "found by its post link, with a handle",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="nonsense"><p>Hi</p>&mdash; Jane (<a href="https://bsky.app/profile/jane.example.com">@jane.example.com</a>) <a href="https://bsky.app/profile/jane.example.com/post/3leotaf7z2227?ref_src=embed">January 1, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/jane.example.com/post/3leotaf7z2227", Name: "Jane", Handle: "jane.example.com", Time: jan1, Date: "January 1, 2025"},
			text: `<p>Hi</p>`,
		},
		{
			name: "date linking to another post is no date",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p>Hi</p>&mdash; Jane (<a href="https://bsky.app/profile/did:plc:abc123">@jane.bsky.social</a>) <a href="https://bsky.app/profile/did:plc:abc123/post/3leqrb7wpk22b">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227", Name: "Jane", Handle: "jane.bsky.social", Time: jan1},
			text: `<p>Hi</p>`,
		},
		{
			name: "a DID as the handle is no handle",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p>Hi</p>&mdash; Jane (<a href="https://bsky.app/profile/did:plc:abc123">@did:plc:abc123</a>) <a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227">May 2, 2025</a></blockquote>`,
			ok:   true,
			want: socialPost{Network: "bluesky", URL: "https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227", Name: "Jane", Time: jan1, Date: "May 2, 2025"},
			text: `<p>Hi</p>`,
		},
		{name: "another record type", raw: `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.like/3leotaf7z2227"><p>Hi</p></blockquote>`},
		{name: "a DID method we don't know", raw: `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:key:abc/app.bsky.feed.post/3leotaf7z2227"><p>Hi</p></blockquote>`},
		{name: "a post link on another host", raw: `<blockquote class="bluesky-embed"><a href="https://evil.example/profile/jane/post/3leotaf7z2227">x</a></blockquote>`},
		{name: "a relative post link", raw: `<blockquote class="bluesky-embed"><a href="/profile/jane/post/3leotaf7z2227">x</a></blockquote>`},
		{name: "no class", raw: `<blockquote data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p>Hi</p></blockquote>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			post, ok := blueskyPost(root.FirstChild)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			text := renderNode(t, post.Text)
			post.Text = nil
			if !post.Time.Equal(tt.want.Time) {
				t.Errorf("Time = %v, want %v", post.Time, tt.want.Time)
			}
			post.Time, tt.want.Time = time.Time{}, time.Time{}
			if post != tt.want {
				t.Errorf("post = %+v\nwant   %+v", post, tt.want)
			}
			if text != tt.text {
				t.Errorf("Text = %s\nwant   %s", text, tt.text)
			}
		})
	}
}

func TestBlueskyFramePost(t *testing.T) {
	tests := []struct {
		src  string
		ok   bool
		want string
	}{
		{"https://embed.bsky.app/embed/did:plc:abc123/app.bsky.feed.post/3leotaf7z2227", true, "https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227"},
		{"https://embed.bsky.app/embed/did:web:example.com/app.bsky.feed.post/self?id=1", true, "https://bsky.app/profile/did:web:example.com/post/self"},
		{"https://embed.bsky.app/embed/jane.bsky.social/app.bsky.feed.post/3leotaf7z2227", false, ""},
		{"https://embed.bsky.app/embed/did:plc:abc123/app.bsky.feed.like/3leotaf7z2227", false, ""},
		{"https://evil.example/embed/did:plc:abc123/app.bsky.feed.post/3leotaf7z2227", false, ""},
		{"javascript:alert(1)", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			root, err := parse(`<iframe src="` + tt.src + `"></iframe>`)
			if err != nil {
				t.Fatal(err)
			}
			post, ok := blueskyFramePost(root.FirstChild)
			if ok != tt.ok || post.URL != tt.want {
				t.Errorf("blueskyFramePost = %+v, %v; want URL %q, %v", post, ok, tt.want, tt.ok)
			}
			if ok && (post.Network != "bluesky" || post.Text != nil || post.Handle != "" || post.Name != "" || post.Date != "" || post.Media || post.Video) {
				t.Errorf("blueskyFramePost = %+v; want only Network, URL and Time", post)
			}
			if want := tidTime(tt.want[strings.LastIndex(tt.want, "/")+1:]); ok && !post.Time.Equal(want) {
				t.Errorf("blueskyFramePost Time = %v; want %v, the key's", post.Time, want)
			}
		})
	}
}

func TestTIDTime(t *testing.T) {
	tests := []struct {
		key  string
		want time.Time
	}{
		{"3leotaf7z2227", time.Date(2025, 1, 1, 15, 0, 0, 0, time.UTC)},
		// The example in the AT Protocol's spec.
		{"3jzfcijpj2z2a", time.Date(2023, 6, 30, 15, 3, 1, 887007000, time.UTC)},
		{"3leotaf7z222", time.Time{}},
		{"3leotaf7z22277", time.Time{}},
		// The first character would set the top bit.
		{"kleotaf7z2227", time.Time{}},
		// Characters outside the alphabet.
		{"3leotaf7z2221", time.Time{}},
		{"3LEOTAF7Z2227", time.Time{}},
		{"self", time.Time{}},
		{"", time.Time{}},
		// The largest TID is in the year 2570.
		{"jzzzzzzzzzzzz", time.Time{}},
	}
	for _, tt := range tests {
		if got := tidTime(tt.key); !got.Equal(tt.want) {
			t.Errorf("tidTime(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func renderNode(t *testing.T, n *html.Node) string {
	t.Helper()
	if n == nil {
		return ""
	}
	var b strings.Builder
	if err := html.Render(&b, n); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
