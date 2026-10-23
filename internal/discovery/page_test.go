package discovery

import (
	"net/url"
	"testing"
)

func TestParsePage(t *testing.T) {
	pageURL, err := url.Parse("https://example.com/blog/")
	if err != nil {
		t.Fatal(err)
	}

	type feed struct{ url, title, typ string }
	tests := []struct {
		name      string
		html      string
		wantTitle string
		wantDesc  string
		wantFeeds []feed
	}{
		{
			name: "rss and atom, relative and absolute",
			html: `<html><head>
				<link rel="alternate" type="application/rss+xml" title=" Posts " href="/feed.xml">
				<link rel="alternate" type="application/atom+xml" href="atom.xml">
				<link rel="alternate" type="application/feed+json" href="https://feeds.example.org/f.json"/>
				<link rel="alternate" type="application/rdf+xml" href="//example.com/index.rdf">
				</head></html>`,
			wantFeeds: []feed{
				{"https://example.com/feed.xml", "Posts", "application/rss+xml"},
				{"https://example.com/blog/atom.xml", "", "application/atom+xml"},
				{"https://feeds.example.org/f.json", "", "application/feed+json"},
				{"https://example.com/index.rdf", "", "application/rdf+xml"},
			},
		},
		{
			name: "base href changes resolution",
			html: `<head>
				<base href="https://cdn.example.net/x/">
				<base href="https://ignored.example/">
				<link rel="alternate" type="application/rss+xml" href="feed.xml">
				<link rel="alternate" type="application/atom+xml" href="/atom.xml">
				</head>`,
			wantFeeds: []feed{
				{"https://cdn.example.net/x/feed.xml", "", "application/rss+xml"},
				{"https://cdn.example.net/atom.xml", "", "application/atom+xml"},
			},
		},
		{
			name: "relative base href resolved against page",
			html: `<base href="../static/"><link rel="alternate" type="application/rss+xml" href="rss">`,
			wantFeeds: []feed{
				{"https://example.com/static/rss", "", "application/rss+xml"},
			},
		},
		{
			name: "base after links still applies",
			html: `<link rel="alternate" type="application/rss+xml" href="rss"><base href="https://cdn.example.net/">`,
			wantFeeds: []feed{
				{"https://cdn.example.net/rss", "", "application/rss+xml"},
			},
		},
		{
			name: "rel and type matching",
			html: `<head>
				<LINK REL="Alternate feed" TYPE="Application/RSS+XML" HREF="/a">
				<link rel="stylesheet" type="application/rss+xml" href="/b">
				<link rel="alternate" type="application/json" href="/c">
				<link rel="alternate" type="text/html" hreflang="fr" href="/fr/">
				<link rel="alternate" href="/d">
				<link rel="alternate" type="application/rss+xml; charset=utf-8" href="/e">
				</head>`,
			wantFeeds: []feed{
				{"https://example.com/a", "", "application/rss+xml"},
				{"https://example.com/e", "", "application/rss+xml"},
			},
		},
		{
			name: "duplicates and non-http hrefs",
			html: `<head>
				<link rel="alternate" type="application/rss+xml" title="first" href="/feed">
				<link rel="alternate" type="application/atom+xml" title="second" href="https://example.com/feed">
				<link rel="alternate" type="application/rss+xml" href="javascript:alert(1)">
				<link rel="alternate" type="application/rss+xml" href="mailto:me@example.com">
				<link rel="alternate" type="application/rss+xml" href="">
				<link rel="alternate" type="application/rss+xml" href="  ">
				<link rel="alternate" type="application/rss+xml">
				<link rel="alternate" type="application/rss+xml" href="ftp://example.com/feed">
				</head>`,
			wantFeeds: []feed{
				{"https://example.com/feed", "first", "application/rss+xml"},
			},
		},
		{
			name: "feed link in body",
			html: `<html><head><title>T</title></head><body>
				<p>hi</p><link rel="alternate" type="application/rss+xml" href="/body.xml">
				</body></html>`,
			wantTitle: "T",
			wantFeeds: []feed{
				{"https://example.com/body.xml", "", "application/rss+xml"},
			},
		},
		{
			name: "og:site_name beats title",
			html: `<head><title>Page title</title>
				<meta property="og:site_name" content="  My
				  Site ">
				</head>`,
			wantTitle: "My Site",
		},
		{
			name:      "title whitespace collapsed and unescaped",
			html:      "<head><title>  My\n  Blog &amp; Co </title><title>Second</title></head>",
			wantTitle: "My Blog & Co",
		},
		{
			name:      "empty og:site_name falls back to title",
			html:      `<meta property="og:site_name" content=" "><title>Blog</title>`,
			wantTitle: "Blog",
		},
		{
			name: "description beats og:description",
			html: `<head>
				<meta property="og:description" content="OG desc">
				<META NAME="Description" CONTENT="  Meta desc ">
				<meta name="description" content="second">
				</head>`,
			wantDesc: "Meta desc",
		},
		{
			name:     "og:description fallback",
			html:     `<meta property="og:description" content=" OG desc ">`,
			wantDesc: "OG desc",
		},
		{
			name:      "no feeds",
			html:      `<html><head><title>Nothing</title></head><body><a href="/feed">feed</a></body></html>`,
			wantTitle: "Nothing",
		},
		{
			name: "empty document",
			html: ``,
		},
		{
			name:      "truncated mid-tag keeps what came before",
			html:      `<html><head><title>Blog</title><meta name="description" content="Posts."><link rel="alternate" type="application/rss+xml" href="/feed"><li`,
			wantTitle: "Blog",
			wantDesc:  "Posts.",
			wantFeeds: []feed{
				{"https://example.com/feed", "", "application/rss+xml"},
			},
		},
		{
			name:      "truncated inside title",
			html:      `<html><head><title>Half a ti`,
			wantTitle: "Half a ti",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParsePage(pageURL, []byte(tt.html))
			if info.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", info.Title, tt.wantTitle)
			}
			if info.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", info.Description, tt.wantDesc)
			}
			if len(info.Feeds) != len(tt.wantFeeds) {
				t.Fatalf("got %d feeds %+v, want %d", len(info.Feeds), info.Feeds, len(tt.wantFeeds))
			}
			for i, want := range tt.wantFeeds {
				got := info.Feeds[i]
				if got.URL.String() != want.url || got.Title != want.title || got.Type != want.typ {
					t.Errorf("Feeds[%d] = {%s %q %q}, want {%s %q %q}",
						i, got.URL, got.Title, got.Type, want.url, want.title, want.typ)
				}
			}
		})
	}
}

func TestParsePageDoesNotModifyPageURL(t *testing.T) {
	pageURL, _ := url.Parse("https://example.com/blog/")
	ParsePage(pageURL, []byte(`<base href="https://other.example/"><link rel="alternate" type="application/rss+xml" href="f">`))
	if got := pageURL.String(); got != "https://example.com/blog/" {
		t.Errorf("pageURL changed to %q", got)
	}
}

func TestParsePagePageTitle(t *testing.T) {
	pageURL, _ := url.Parse("https://example.com/blog/post")
	tests := []struct {
		name          string
		html          string
		wantPageTitle string
		wantTitle     string
	}{
		{
			name:          "from og:title",
			html:          `<head><title>Post | Blog</title><meta property="og:title" content=" Post "></head>`,
			wantPageTitle: "Post",
			wantTitle:     "Post | Blog",
		},
		{
			name:          "og:title as name",
			html:          `<meta name="og:title" content="Post">`,
			wantPageTitle: "Post",
		},
		{
			name:          "falls back to title",
			html:          `<head><meta property="og:title" content=" "><title>Post | Blog</title></head>`,
			wantPageTitle: "Post | Blog",
			wantTitle:     "Post | Blog",
		},
		{
			name:          "whitespace collapsed",
			html:          "<meta property=\"og:title\" content=\"A\n   long\t post\">",
			wantPageTitle: "A long post",
		},
		{
			name: "title is still og:site_name",
			html: `<head><title>Post | Blog</title>
				<meta property="og:site_name" content="Blog">
				<meta property="og:title" content="Post">
				</head>`,
			wantPageTitle: "Post",
			wantTitle:     "Blog",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParsePage(pageURL, []byte(tt.html))
			if info.PageTitle != tt.wantPageTitle {
				t.Errorf("PageTitle = %q, want %q", info.PageTitle, tt.wantPageTitle)
			}
			if info.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", info.Title, tt.wantTitle)
			}
		})
	}
}

func TestParsePageImage(t *testing.T) {
	pageURL, _ := url.Parse("https://example.com/blog/post")
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "og:image relative to the page",
			html: `<meta property="og:image" content="img/cover.jpg">`,
			want: "https://example.com/blog/img/cover.jpg",
		},
		{
			name: "og:image relative to base href",
			html: `<base href="https://cdn.example.net/assets/"><meta property="og:image" content="cover.jpg">`,
			want: "https://cdn.example.net/assets/cover.jpg",
		},
		{
			name: "og:image:secure_url alone",
			html: `<meta property="og:image:secure_url" content="https://example.com/s.jpg">`,
			want: "https://example.com/s.jpg",
		},
		{
			name: "twitter:image only",
			html: `<meta name="twitter:image" content="/t.jpg">`,
			want: "https://example.com/t.jpg",
		},
		{
			name: "twitter:image:src only",
			html: `<meta name="twitter:image:src" content="/t.jpg">`,
			want: "https://example.com/t.jpg",
		},
		{
			name: "og:image beats twitter:image",
			html: `<meta name="twitter:image" content="/t.jpg"><meta property="og:image" content="/og.jpg">`,
			want: "https://example.com/og.jpg",
		},
		{
			name: "data: is dropped",
			html: `<meta property="og:image" content="data:image/png;base64,AAAA">`,
			want: "",
		},
		{
			name: "javascript: is dropped",
			html: `<meta property="og:image" content="javascript:alert(1)">`,
			want: "",
		},
		{
			name: "empty content skipped for the next tag",
			html: `<meta property="og:image" content=" "><meta property="og:image" content="/second.jpg">`,
			want: "https://example.com/second.jpg",
		},
		{
			name: "none",
			html: `<head><title>Post</title></head>`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParsePage(pageURL, []byte(tt.html))
			if info.Image != tt.want {
				t.Errorf("Image = %q, want %q", info.Image, tt.want)
			}
		})
	}
}

func TestParsePageIcons(t *testing.T) {
	pageURL, _ := url.Parse("https://example.com/blog/")
	type icon struct {
		url   string
		apple bool
		typ   string
		size  int
	}
	tests := []struct {
		name string
		html string
		want []icon
	}{
		{
			name: "icon with sizes and type",
			html: `<link rel="icon" href="/favicon.png" sizes="32x32" type="image/png">`,
			want: []icon{{"https://example.com/favicon.png", false, "image/png", 32}},
		},
		{
			name: "shortcut icon",
			html: `<LINK REL="Shortcut Icon" HREF="favicon.ico">`,
			want: []icon{{"https://example.com/blog/favicon.ico", false, "", 0}},
		},
		{
			name: "apple-touch-icon without sizes",
			html: `<link rel="apple-touch-icon" href="/apple.png">`,
			want: []icon{{"https://example.com/apple.png", true, "", 0}},
		},
		{
			name: "apple-touch-icon-precomposed",
			html: `<link rel="apple-touch-icon-precomposed" href="/apple-pre.png" sizes="180x180">`,
			want: []icon{{"https://example.com/apple-pre.png", true, "", 180}},
		},
		{
			name: "largest of several sizes",
			html: `<link rel="icon" href="/i.png" sizes="16x16 192x192">`,
			want: []icon{{"https://example.com/i.png", false, "", 192}},
		},
		{
			name: "sizes any",
			html: `<link rel="icon" href="/i.svg" sizes="any" type="image/svg+xml">`,
			want: []icon{{"https://example.com/i.svg", false, "image/svg+xml", 0}},
		},
		{
			name: "non-square size uses the smaller side",
			html: `<link rel="icon" href="/i.png" sizes="16x32">`,
			want: []icon{{"https://example.com/i.png", false, "", 16}},
		},
		{
			name: "mask-icon and fluid-icon ignored",
			html: `<link rel="mask-icon" href="/mask.svg"><link rel="fluid-icon" href="/fluid.png">`,
		},
		{
			name: "data: and empty hrefs dropped",
			html: `<link rel="icon" href="data:image/png;base64,AAAA"><link rel="icon" href=""><link rel="icon">`,
		},
		{
			name: "duplicates dropped, document order kept",
			html: `<head>
				<link rel="apple-touch-icon" href="/apple.png">
				<link rel="icon" href="/favicon.ico">
				<link rel="icon" href="https://example.com/apple.png">
				<link rel="icon" href="/favicon-32.png" sizes="32x32">
				</head>`,
			want: []icon{
				{"https://example.com/apple.png", true, "", 0},
				{"https://example.com/favicon.ico", false, "", 0},
				{"https://example.com/favicon-32.png", false, "", 32},
			},
		},
		{
			name: "resolved against base href",
			html: `<base href="https://cdn.example.net/"><link rel="icon" href="icon.png">`,
			want: []icon{{"https://cdn.example.net/icon.png", false, "", 0}},
		},
		{
			name: "truncated document keeps icons before the cut",
			html: `<html><head><link rel="icon" href="/a.png"><link rel="apple-touch-icon" href="/b.png"><link rel="icon" hr`,
			want: []icon{
				{"https://example.com/a.png", false, "", 0},
				{"https://example.com/b.png", true, "", 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ParsePage(pageURL, []byte(tt.html))
			if len(info.Icons) != len(tt.want) {
				t.Fatalf("got %d icons %+v, want %d", len(info.Icons), info.Icons, len(tt.want))
			}
			for i, want := range tt.want {
				got := info.Icons[i]
				if got.URL.String() != want.url || got.Apple != want.apple || got.Type != want.typ || got.Size != want.size {
					t.Errorf("Icons[%d] = {%s %v %q %d}, want {%s %v %q %d}",
						i, got.URL, got.Apple, got.Type, got.Size, want.url, want.apple, want.typ, want.size)
				}
			}
		})
	}
}
