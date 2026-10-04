package discovery

import (
	"net/url"
	"strings"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePage(pageURL, strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("ParsePage: %v", err)
			}
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
	_, err := ParsePage(pageURL, strings.NewReader(`<base href="https://other.example/"><link rel="alternate" type="application/rss+xml" href="f">`))
	if err != nil {
		t.Fatal(err)
	}
	if got := pageURL.String(); got != "https://example.com/blog/" {
		t.Errorf("pageURL changed to %q", got)
	}
}
