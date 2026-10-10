package feedparse

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
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
			// is converted to plain text.
			file:    "atom-no-rel.xml",
			feedURL: "https://example.net/atom.xml",
			want: Meta{
				Format:  "atom",
				Title:   "Tom & Jerry's Blog",
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
			if got.Meta != tt.want {
				t.Errorf("Parse:\n got %+v\nwant %+v", got.Meta, tt.want)
			}
		})
	}
}

func TestParseItems(t *testing.T) {
	tests := []struct {
		file    string
		feedURL string
		want    []Item
	}{
		{
			file:    "rss2.xml",
			feedURL: "https://example.com/feed.xml",
			want: []Item{
				{
					// Trimmed <guid>, entity-encoded <description> stays
					// HTML, CDATA <content:encoded>, <pubDate> in UTC.
					ID:        "https://example.com/?p=1",
					URL:       "https://example.com/first",
					Title:     "First post",
					Summary:   "<p>Hello &amp; welcome</p>",
					Content:   "<p>Full <b>text</b></p>",
					Published: time.Date(2026, 10, 5, 7, 30, 0, 0, time.UTC),
				},
				{
					// No <guid>, no <pubDate>, relative <link>.
					URL:     "https://example.com/second",
					Title:   "No guid",
					Summary: "Plain",
				},
				{
					// javascript: link.
					ID:        "bad-link",
					Title:     "Bad link",
					Published: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			// RDF items have no <guid>; rdf:about isn't read.
			file:    "rss1-rdf.xml",
			feedURL: "https://example.org/index.rdf",
			want: []Item{
				{
					URL:       "https://example.org/a",
					Title:     "A",
					Summary:   "<i>First</i>",
					Published: time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC),
				},
				{
					// mailto: link; <dc:date> with an offset.
					Title:     "B",
					Published: time.Date(2026, 10, 3, 11, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			file:    "atom.xml",
			feedURL: "https://example.net/blog/atom.xml",
			want: []Item{
				{
					// Trimmed <id>, HTML title, relative alternate link,
					// <published> preferred over <updated>.
					ID:        "urn:uuid:1225c695-cfb8-4ebb-aaaa-80da344efa6a",
					URL:       "https://example.net/posts/1",
					Title:     "Hi there",
					Summary:   "<p>Short</p>",
					Content:   "<p>Long &amp; full</p>",
					Published: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
				},
				{
					// replies and enclosure links before the alternate one;
					// text title; only <updated>.
					ID:        "tag:example.net,2026:2",
					URL:       "https://example.net/posts/2",
					Title:     "A & B",
					Published: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
				},
				{
					// Link without rel; no dates.
					ID:    "urn:uuid:1225c695-cfb8-4ebb-bbbb-80da344efa6a",
					URL:   "https://example.net/blog/entry",
					Title: "Entry",
				},
			},
		},
		{
			file:    "jsonfeed.json",
			feedURL: "https://example.com/feed.json",
			want: []Item{
				{
					ID:        "1",
					URL:       "https://example.com/1",
					Title:     "One",
					Summary:   "First item",
					Content:   "<p>Hello <b>world</b></p>",
					Published: time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC),
				},
				{
					// Trimmed id, relative url, content_text is escaped,
					// date_modified only.
					ID:        "2",
					URL:       "https://example.com/2",
					Content:   "Use &lt;b&gt; for bold",
					Published: time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse(mustParseURL(t, tt.feedURL), readTestdata(t, tt.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got.Items) != len(tt.want) {
				t.Fatalf("got %d items, want %d: %+v", len(got.Items), len(tt.want), got.Items)
			}
			for i, g := range got.Items {
				w := tt.want[i]
				if !g.Published.Equal(w.Published) || g.Published.Location() != time.UTC {
					t.Errorf("item %d: Published = %v, want %v (UTC)", i, g.Published, w.Published)
				}
				g.Published, w.Published = time.Time{}, time.Time{}
				if g != w {
					t.Errorf("item %d:\n got %+v\nwant %+v", i, g, w)
				}
			}
		})
	}
}

func TestParseItemsAtomTitleTypes(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/feed.xml")
	const atomNS = `<feed xmlns="http://www.w3.org/2005/Atom">`
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			// Each entry's own type counts, not the feed's or another
			// entry's; a nested element's <title> is ignored.
			name: "per entry",
			body: atomNS + `<title type="html">F</title>` +
				`<entry><title>&lt;b&gt;</title></entry>` +
				`<entry><source><title type="text">S</title></source><title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">A <em>B</em></div></title></entry>` +
				`<entry><title type="html">x &amp;lt; y</title></entry>` +
				`<entry></entry></feed>`,
			want: []string{"<b>", "A B", "x < y", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(feedURL, []byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got.Items) != len(tt.want) {
				t.Fatalf("got %d items, want %d", len(got.Items), len(tt.want))
			}
			for i, w := range tt.want {
				if got.Items[i].Title != w {
					t.Errorf("item %d: Title = %q, want %q", i, got.Items[i].Title, w)
				}
			}
		})
	}
}

func TestParseItemImage(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/blog/feed.xml")
	rssItem := func(ns, item string) string {
		return `<rss version="2.0"` + ns + `><channel><title>F</title><item><title>A</title>` + item + `</item></channel></rss>`
	}
	const mediaNS = ` xmlns:media="http://search.yahoo.com/mrss/"`
	const itunesNS = ` xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"`
	const contentNS = ` xmlns:content="http://purl.org/rss/1.0/modules/content/"`
	atomEntry := func(entry string) string {
		return `<feed xmlns="http://www.w3.org/2005/Atom"` + mediaNS + `><title>F</title><entry><title>A</title>` + entry + `</entry></feed>`
	}
	jsonItem := func(item string) string {
		return `{"version":"https://jsonfeed.org/version/1.1","title":"F","items":[{"id":"1",` + item + `}]}`
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "rss media:content medium image",
			body: rssItem(mediaNS, `<media:content url="https://cdn.example.com/a.jpg" medium="image"/>`),
			want: "https://cdn.example.com/a.jpg",
		},
		{
			name: "rss media:content image type",
			body: rssItem(mediaNS, `<media:content url="https://cdn.example.com/a.jpg" type="image/jpeg"/>`),
			want: "https://cdn.example.com/a.jpg",
		},
		{
			name: "rss media:content before media:thumbnail",
			body: rssItem(mediaNS, `<media:thumbnail url="https://cdn.example.com/thumb.jpg"/><media:content url="https://cdn.example.com/a.jpg" medium="image"/>`),
			want: "https://cdn.example.com/a.jpg",
		},
		{
			name: "rss media:thumbnail only",
			body: rssItem(mediaNS, `<media:thumbnail url="https://cdn.example.com/thumb.jpg"/>`),
			want: "https://cdn.example.com/thumb.jpg",
		},
		{
			name: "rss image enclosure",
			body: rssItem("", `<enclosure url="https://cdn.example.com/a.png" type="image/png" length="1"/>`),
			want: "https://cdn.example.com/a.png",
		},
		{
			name: "rss audio enclosure",
			body: rssItem("", `<enclosure url="https://cdn.example.com/a.mp3" type="audio/mpeg" length="1"/>`),
			want: "",
		},
		{
			name: "rss itunes:image",
			body: rssItem(itunesNS, `<itunes:image href="https://cdn.example.com/cover.jpg"/>`),
			want: "https://cdn.example.com/cover.jpg",
		},
		{
			name: "rss img in content only",
			body: rssItem(contentNS, `<description>&lt;img src="https://example.com/d.gif"&gt;</description><content:encoded><![CDATA[<p><img src="https://example.com/pixel.gif"></p>]]></content:encoded>`),
			want: "",
		},
		{
			name: "atom image enclosure link",
			body: atomEntry(`<link rel="enclosure" type="image/png" href="https://cdn.example.com/a.png"/>`),
			want: "https://cdn.example.com/a.png",
		},
		{
			name: "atom media:thumbnail",
			body: atomEntry(`<media:thumbnail url="https://cdn.example.com/thumb.jpg"/>`),
			want: "https://cdn.example.com/thumb.jpg",
		},
		{
			name: "json image",
			body: jsonItem(`"image":"https://cdn.example.com/a.jpg","banner_image":"https://cdn.example.com/banner.jpg"`),
			want: "https://cdn.example.com/a.jpg",
		},
		{
			name: "json banner_image only",
			body: jsonItem(`"banner_image":"https://cdn.example.com/banner.jpg"`),
			want: "https://cdn.example.com/banner.jpg",
		},
		{
			name: "relative URL resolved",
			body: rssItem(mediaNS, `<media:thumbnail url="img/a.jpg"/>`),
			want: "https://example.com/blog/img/a.jpg",
		},
		{
			name: "data URL",
			body: rssItem(mediaNS, `<media:thumbnail url="data:image/gif;base64,R0lGODlhAQABAAAAACw="/>`),
			want: "",
		},
		{
			name: "javascript URL",
			body: jsonItem(`"image":"javascript:alert(1)"`),
			want: "",
		},
		{
			name: "no image",
			body: rssItem("", `<description>text</description>`),
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(feedURL, []byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got.Items) != 1 {
				t.Fatalf("got %d items, want 1", len(got.Items))
			}
			if got.Items[0].ImageURL != tt.want {
				t.Errorf("ImageURL = %q, want %q", got.Items[0].ImageURL, tt.want)
			}
		})
	}
}

func TestParseItemDateOnly(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/feed.xml")
	rssItem := func(item string) string {
		return `<rss version="2.0"><channel><title>F</title><item><title>A</title>` + item + `</item></channel></rss>`
	}
	atomEntry := func(entry string) string {
		return `<feed xmlns="http://www.w3.org/2005/Atom"><title>F</title><entry><title>A</title>` + entry + `</entry></feed>`
	}
	jsonItem := func(item string) string {
		return `{"version":"https://jsonfeed.org/version/1.1","title":"F","items":[{"id":"1"` + item + `}]}`
	}
	midnight := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	timed := time.Date(2026, 10, 9, 14, 3, 0, 0, time.UTC)
	tests := []struct {
		name          string
		body          string
		wantPublished time.Time
		wantDateOnly  bool
	}{
		{"rss pubDate date-only", rssItem(`<pubDate>Thu, 09 Oct 2026</pubDate>`), midnight, true},
		{"rss pubDate with time", rssItem(`<pubDate>Thu, 09 Oct 2026 14:03:00 +0000</pubDate>`), timed, false},
		{"atom published date-only", atomEntry(`<published>2026-10-09</published>`), midnight, true},
		{"atom published with time", atomEntry(`<published>2026-10-09T14:03:00Z</published>`), timed, false},
		{"atom updated only, date-only", atomEntry(`<updated>2026-10-09</updated>`), midnight, true},
		{"atom date-only updated ignored when published has a time", atomEntry(`<published>2026-10-09T14:03:00Z</published><updated>2026-10-10</updated>`), timed, false},
		{"json date_published date-only", jsonItem(`,"date_published":"2026-10-09"`), midnight, true},
		{"json date_published with time", jsonItem(`,"date_published":"2026-10-09T14:03:00Z"`), timed, false},
		{"json date_modified only, date-only", jsonItem(`,"date_modified":"2026-10-09"`), midnight, true},
		{"no date", rssItem(`<description>text</description>`), time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(feedURL, []byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got.Items) != 1 {
				t.Fatalf("got %d items, want 1", len(got.Items))
			}
			item := got.Items[0]
			if !item.Published.Equal(tt.wantPublished) {
				t.Errorf("Published = %v, want %v", item.Published, tt.wantPublished)
			}
			if item.PublishedDateOnly != tt.wantDateOnly {
				t.Errorf("PublishedDateOnly = %t, want %t", item.PublishedDateOnly, tt.wantDateOnly)
			}
		})
	}
}

func TestParseText(t *testing.T) {
	feedURL := mustParseURL(t, "https://example.com/feed.xml")
	const atomNS = `<feed xmlns="http://www.w3.org/2005/Atom">`
	tests := []struct {
		name                string
		body                string
		wantTitle, wantDesc string
	}{
		{
			name:      "atom html",
			body:      atomNS + `<link href="https://example.com/"></link><title type="html">Tom &amp;amp; Jerry&#39;s &lt;b&gt;Blog&lt;/b&gt;</title><subtitle type="html">Line&lt;br&gt;two</subtitle></feed>`,
			wantTitle: "Tom & Jerry's Blog",
			wantDesc:  "Line two",
		},
		{
			name:      "atom html cdata",
			body:      atomNS + `<title type="HTML"><![CDATA[A &amp; <i>B</i>]]></title></feed>`,
			wantTitle: "A & B",
		},
		{
			name:      "atom xhtml",
			body:      atomNS + `<title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">A <em>B</em></div></title><subtitle type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>One</p><p>Two &amp; three</p></div></subtitle></feed>`,
			wantTitle: "A B",
			wantDesc:  "One Two & three",
		},
		{
			// Text is not markup: escaped tags are kept literally.
			name:      "atom text",
			body:      atomNS + `<title type="text">A &lt;b&gt; tag</title><subtitle>x &lt; y &amp;amp; z</subtitle></feed>`,
			wantTitle: "A <b> tag",
			wantDesc:  "x < y &amp; z",
		},
		{
			// Only the feed-level <title> type counts, not an entry's.
			name:      "atom entry title type",
			body:      atomNS + `<entry><title type="html">E</title></entry><title>&lt;b&gt;</title></feed>`,
			wantTitle: "<b>",
		},
		{
			// RSS descriptions are always HTML; titles are text.
			name:      "rss description",
			body:      `<rss version="2.0"><channel><title>A &lt;b&gt; tag</title><description>Tom &amp;amp; Jerry&lt;br/&gt;&lt;script&gt;x()&lt;/script&gt;&lt;em&gt;Blog&lt;/em&gt;</description></channel></rss>`,
			wantTitle: "A <b> tag",
			wantDesc:  "Tom & Jerry Blog",
		},
		{
			name:      "rss cdata description",
			body:      `<rss version="2.0"><channel><title>T</title><description><![CDATA[<p>Hello</p><p>world</p>]]></description></channel></rss>`,
			wantTitle: "T",
			wantDesc:  "Hello world",
		},
		{
			name:      "rdf description",
			body:      `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/"><channel rdf:about="https://example.org/"><title>T</title><description>&lt;b&gt;Bold&lt;/b&gt;</description></channel></rdf:RDF>`,
			wantTitle: "T",
			wantDesc:  "Bold",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(feedURL, []byte(tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantDesc)
			}
		})
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"Tom &amp; Jerry&#39;s &eacute;", "Tom & Jerry's é"},
		{"<p>A <b>bold <i>nested</i></b> word</p>", "A bold nested word"},
		{"in<b>line</b>", "inline"},
		{"one<br>two<br/>three", "one two three"},
		{"<p>one</p><p>two</p><div>three</div><ul><li>a</li><li>b</li></ul>", "one two three a b"},
		{"a<script>alert('<b>x</b>')</script>b<style>p{color:red}</style>c", "abc"},
		{"<xhtml:p>one</xhtml:p><xhtml:p>two</xhtml:p>", "one two"},
		{"x < y", "x < y"},
		{"  lots \n\t of   space  ", "lots of space"},
		{"a<!-- comment -->b", "ab"},
	}
	for _, tt := range tests {
		if got := HTMLToText(tt.in); got != tt.want {
			t.Errorf("HTMLToText(%q) = %q, want %q", tt.in, got, tt.want)
		}
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
