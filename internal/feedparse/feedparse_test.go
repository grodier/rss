package feedparse

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestParse(t *testing.T) {
	tests := []struct {
		file    string
		feedURL string
		want    Meta
	}{
		{
			file:    "rss2.xml",
			feedURL: "https://example.com/feed.xml",
			want: Meta{
				Format:      "rss",
				Title:       "Example Blog",
				Description: "Posts about examples.",
				SiteURL:     "https://example.com/",
			},
		},
		{
			// The atom:link rel="self" must not become the SiteURL.
			file:    "rss2-self-only.xml",
			feedURL: "https://example.com/feed.xml",
			want: Meta{
				Format:      "rss",
				Title:       "No site link",
				Description: "Only a self link.",
			},
		},
		{
			file:    "rss1-rdf.xml",
			feedURL: "https://example.org/index.rdf",
			want: Meta{
				Format:      "rdf",
				Title:       "RDF Site",
				Description: "An RSS 1.0 feed.",
				SiteURL:     "https://example.org/",
			},
		},
		{
			// Relative rel="alternate" href, resolved against the feed URL;
			// rel="self" comes first and is ignored.
			file:    "atom.xml",
			feedURL: "https://example.net/blog/atom.xml",
			want: Meta{
				Format:      "atom",
				Title:       "Atom Example",
				Description: "A subtitle.",
				SiteURL:     "https://example.net/blog/",
			},
		},
		{
			// <title type="html"> and a <link> without rel. An HTML title
			// is kept as HTML source (entities not decoded, tags not
			// stripped); templates escape it.
			file:    "atom-no-rel.xml",
			feedURL: "https://example.net/atom.xml",
			want: Meta{
				Format:  "atom",
				Title:   "Tom &amp; Jerry's <b>Blog</b>",
				SiteURL: "https://example.net/",
			},
		},
		{
			file:    "jsonfeed.json",
			feedURL: "https://example.com/feed.json",
			want: Meta{
				Format:      "json",
				Title:       "JSON Example",
				Description: "A JSON Feed.",
				SiteURL:     "https://example.com/",
			},
		},
		{
			// Leading BOM and whitespace, CDATA title, relative <link>.
			file:    "rss-bom-cdata.xml",
			feedURL: "http://example.com/feeds/main.xml",
			want: Meta{
				Format:      "rss",
				Title:       "CDATA <Title>",
				Description: "BOM feed",
				SiteURL:     "http://example.com/home",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse(mustParseURL(t, tt.feedURL), readTestdata(t, tt.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got != tt.want {
				t.Errorf("Parse:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseSiteURL(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/feed.xml")
	tests := []struct {
		link string
		want string
	}{
		{"https://example.com/", "https://example.com/"},
		{"  https://example.com/a  ", "https://example.com/a"},
		{"/blog", "https://example.com/blog"},
		{"//cdn.example.net/x", "https://cdn.example.net/x"},
		{"javascript:alert(1)", ""},
		{"mailto:me@example.com", ""},
		{"ftp://example.com/", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			body := `<rss version="2.0"><channel><title>T</title><link>` + tt.link + `</link></channel></rss>`
			got, err := Parse(feedURL, []byte(body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.SiteURL != tt.want {
				t.Errorf("SiteURL = %q, want %q", got.SiteURL, tt.want)
			}
		})
	}
}

func TestParseNotFeed(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/feed")
	tests := []struct {
		name string
		body []byte
	}{
		{"page.html", readTestdata(t, "page.html")},
		{"html without doctype", []byte("<html><body>hi</body></html>")},
		{"empty", nil},
		{"whitespace", []byte(" \n\t ")},
		{"plain text", []byte("hello rss")},
		{"json object", []byte(`{"foo":1}`)},
		{"json without feed version", []byte(`{"version":"1.0","title":"x","items":[]}`)},
		{"other xml", []byte(`<?xml version="1.0"?><sitemap/>`)},
		{"malformed rss", []byte(`<rss version="2.0"><channel><title>x</channel>`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(feedURL, tt.body)
			if !errors.Is(err, ErrNotFeed) {
				t.Errorf("Parse error = %v, want ErrNotFeed", err)
			}
		})
	}
}

func TestSniff(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        bool
	}{
		{"rss content type", "application/rss+xml", nil, true},
		{"atom content type with charset", "application/atom+xml; charset=utf-8", nil, true},
		{"json feed content type", "application/feed+json", nil, true},
		{"text/xml", "text/xml", nil, true},
		{"application/xml", "application/xml", nil, true},
		{"html", "text/html", readTestdata(t, "page.html"), false},
		{"text/plain rss body", "text/plain", []byte("<rss version=\"2.0\">"), true},
		{"bom prolog comment rss", "", []byte("\xef\xbb\xbf\n<?xml version=\"1.0\"?>\n<!-- hi -->\n<?xml-stylesheet href=\"s.xsl\"?>\n<rss>"), true},
		{"atom body", "application/octet-stream", readTestdata(t, "atom.xml"), true},
		{"rdf body", "", readTestdata(t, "rss1-rdf.xml"), true},
		{"json feed body", "application/json", readTestdata(t, "jsonfeed.json"), true},
		{"json object", "application/json", []byte(`{"foo":1}`), false},
		{"rss-like element name", "", []byte("<rssfoo>"), false},
		{"empty", "", nil, false},
		{"bad content type", "%%%", []byte("hello"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sniff(tt.contentType, tt.body); got != tt.want {
				t.Errorf("Sniff(%q, %q) = %v, want %v", tt.contentType, tt.body, got, tt.want)
			}
		})
	}
}
