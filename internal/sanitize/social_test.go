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
