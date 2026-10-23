package sanitize

import (
	stdhtml "html"
	"net/url"
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	base, err := url.Parse("https://example.com/posts/1")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		raw    string
		base   *url.URL
		want   *string  // exact output, if set
		has    []string // substrings the output must contain
		hasNot []string // substrings the output must not contain
	}{
		{
			name: "script removed",
			raw:  `<p>hi</p><script>alert(1)</script>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name:   "onerror removed",
			raw:    `<img src=x onerror=alert(1)>`,
			has:    []string{`<img src="x"`},
			hasNot: []string{"onerror", "alert"},
		},
		{
			name:   "javascript href removed",
			raw:    `<a href="javascript:alert(1)">x</a>`,
			hasNot: []string{"href", "javascript"},
		},
		{
			name:   "data URLs removed",
			raw:    `<img src="data:image/png;base64,AAAA" alt="a"><a href="data:text/html,<script>alert(1)</script>">x</a>`,
			hasNot: []string{"data:", "src=", "href=", "script"},
		},
		{
			name: "style element removed",
			raw:  `<style>body{display:none}</style><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "style attribute removed",
			raw:  `<p style="position:fixed">hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name:   "form and input removed",
			raw:    `<form action="https://evil.example/login"><input name="password" type="password"></form>`,
			hasNot: []string{"<form", "<input", "evil"},
		},
		{
			name: "meta refresh removed",
			raw:  `<meta http-equiv="refresh" content="0;url=https://evil.example/"><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "base removed",
			raw:  `<base href="https://evil.example/"><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "object removed",
			raw:  `<object data="https://evil.example/x.swf"><param name="a" value="b"></object><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "embed removed",
			raw:  `<embed src="https://evil.example/x.swf"><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "svg removed",
			raw:  `<svg onload="alert(1)"><a xlink:href="javascript:alert(1)"><circle r="5"></circle></a></svg><p>hi</p>`,
			want: ptr(`<p>hi</p>`),
		},
		{
			name: "relative link resolved",
			raw:  `<a href="/p">x</a>`,
			base: base,
			has:  []string{`href="https://example.com/p"`},
		},
		{
			name: "relative image resolved",
			raw:  `<img src="i.png">`,
			base: base,
			has:  []string{`src="https://example.com/posts/i.png"`},
		},
		{
			name:   "fragment link kept in the article",
			raw:    `<a href="#fn1">1</a>`,
			base:   base,
			has:    []string{`href="#fn1"`},
			hasNot: []string{"example.com", "_blank"},
		},
		{
			name:   "nil base leaves URLs relative",
			raw:    `<a href="/p">x</a><img src="i.png">`,
			has:    []string{`href="/p"`, `src="i.png"`},
			hasNot: []string{"example.com"},
		},
		{
			name: "image loads lazily without a referrer, srcset removed",
			raw:  `<img src="https://example.com/i.png" srcset="i-2x.png 2x" sizes="100vw" loading="eager" referrerpolicy="unsafe-url">`,
			want: ptr(`<img src="https://example.com/i.png" loading="lazy" referrerpolicy="no-referrer"/>`),
		},
		{
			name:   "external link opens in a new tab",
			raw:    `<a href="https://other.example/" target="_self" rel="opener">x</a>`,
			has:    []string{`target="_blank"`, "noopener", "noreferrer"},
			hasNot: []string{"_self", `"opener`, " opener"},
		},
		{
			name: "iframe becomes a link",
			raw:  `<p>before</p><iframe src="https://open.spotify.com/embed/x" width="560"></iframe><p>after</p>`,
			has: []string{
				`<p>before</p><p><a href="https://open.spotify.com/embed/x"`,
				`>Open embedded content</a></p><p>after</p>`,
			},
			hasNot: []string{"iframe", "560"},
		},
		{
			name: "YouTube iframe becomes a preview",
			raw:  `<p>before</p><iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ" width="560"></iframe><p>after</p>`,
			has: []string{
				`<p>before</p><figure class="embed" data-embed="youtube" data-embed-id="dQw4w9WgXcQ">` +
					`<a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`,
				`<img src="https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg" alt="" loading="lazy" referrerpolicy="no-referrer"/>` +
					`<span>Play on YouTube</span></a></figure><p>after</p>`,
				`target="_blank"`,
			},
			hasNot: []string{"iframe", "560", "Open embedded content"},
		},
		{
			name: "YouTube host without www",
			raw:  `<iframe src="https://youtube.com/embed/dQw4w9WgXcQ"></iframe>`,
			has:  []string{`data-embed="youtube" data-embed-id="dQw4w9WgXcQ"`, `href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`},
		},
		{
			name: "YouTube mobile host",
			raw:  `<iframe src="https://m.youtube.com/embed/dQw4w9WgXcQ"></iframe>`,
			has:  []string{`data-embed="youtube" data-embed-id="dQw4w9WgXcQ"`, `href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`},
		},
		{
			name: "YouTube nocookie host",
			raw:  `<iframe src="https://youtube-nocookie.com/embed/a_b-c_d-e_f"></iframe>`,
			has:  []string{`data-embed="youtube" data-embed-id="a_b-c_d-e_f"`, `src="https://i.ytimg.com/vi/a_b-c_d-e_f/hqdefault.jpg"`},
		},
		{
			name: "YouTube nocookie host with www",
			raw:  `<iframe src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"></iframe>`,
			has:  []string{`data-embed="youtube" data-embed-id="dQw4w9WgXcQ"`},
		},
		{
			name:   "YouTube query string ignored",
			raw:    `<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ?start=30&rel=0"></iframe>`,
			has:    []string{`data-embed-id="dQw4w9WgXcQ"`, `href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`},
			hasNot: []string{"start", "rel=0"},
		},
		{
			name: "Vimeo iframe becomes a preview without an image",
			raw:  `<iframe src="https://player.vimeo.com/video/76979871?h=8272103f6e"></iframe>`,
			has: []string{
				`<figure class="embed" data-embed="vimeo" data-embed-id="76979871"><a href="https://vimeo.com/76979871"`,
				`<span>Play on Vimeo</span></a></figure>`,
			},
			hasNot: []string{"<img", "8272103f6e"},
		},
		{
			name:   "YouTube ID too short stays a link",
			raw:    `<iframe src="https://www.youtube.com/embed/dQw4w9WgXc"></iframe>`,
			has:    []string{"Open embedded content"},
			hasNot: []string{"<figure"},
		},
		{
			name:   "YouTube ID with a slash stays a link",
			raw:    `<iframe src="https://www.youtube.com/embed/dQw4w9/WgXcQ"></iframe>`,
			has:    []string{"Open embedded content"},
			hasNot: []string{"<figure"},
		},
		{
			name:   "YouTube watch URL in an iframe stays a link",
			raw:    `<iframe src="https://www.youtube.com/watch?v=dQw4w9WgXcQ"></iframe>`,
			has:    []string{"Open embedded content"},
			hasNot: []string{"<figure"},
		},
		{
			name:   "Vimeo page URL in an iframe stays a link",
			raw:    `<iframe src="https://vimeo.com/123"></iframe>`,
			has:    []string{"Open embedded content"},
			hasNot: []string{"<figure"},
		},
		{
			name:   "other host stays a link",
			raw:    `<iframe src="https://evil.example/embed/dQw4w9WgXcQ"></iframe>`,
			has:    []string{"Open embedded content"},
			hasNot: []string{"<figure"},
		},
		{
			name: "feed-written embed with an unknown type loses data-embed",
			raw:  `<figure class="embed" data-embed="evil" data-embed-id="x">x</figure>`,
			want: ptr(`<figure class="embed" data-embed-id="x">x</figure>`),
		},
		{
			name: "feed-written embed ID with a space dropped",
			raw:  `<figure class="embed" data-embed="youtube" data-embed-id="a b">x</figure>`,
			want: ptr(`<figure class="embed" data-embed="youtube">x</figure>`),
		},
		{
			name: "nested relative iframe resolved and replaced",
			raw:  `<div><iframe src="/embed/x"></iframe></div>`,
			base: base,
			has:  []string{`<a href="https://example.com/embed/x"`, "Open embedded content"},
		},
		{
			name: "javascript iframe dropped",
			raw:  `<p>before</p><iframe src="javascript:alert(1)">fallback</iframe><p>after</p>`,
			want: ptr(`<p>before</p><p>after</p>`),
		},
		{
			name: "iframe without src dropped",
			raw:  `<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
			want: ptr(``),
		},
		{
			name: "formatting kept",
			raw: `<h2>Title</h2><p>Some <strong>bold</strong> and <em>italic</em> text.</p>` +
				`<ul><li>one</li></ul><ol><li>two</li></ol>` +
				`<blockquote>quote</blockquote><pre><code>x := 1</code></pre>` +
				`<table><thead><tr><th>h</th></tr></thead><tbody><tr><td>d</td></tr></tbody></table>`,
			want: ptr(`<h2>Title</h2><p>Some <strong>bold</strong> and <em>italic</em> text.</p>` +
				`<ul><li>one</li></ul><ol><li>two</li></ol>` +
				`<blockquote>quote</blockquote><pre><code>x := 1</code></pre>` +
				`<table><thead><tr><th>h</th></tr></thead><tbody><tr><td>d</td></tr></tbody></table>`),
		},
		{
			name: "figure and image alt kept",
			raw:  `<figure><img src="https://example.com/i.png" alt="A cat"><figcaption>A cat</figcaption></figure>`,
			want: ptr(`<figure><img src="https://example.com/i.png" alt="A cat" loading="lazy" referrerpolicy="no-referrer"/><figcaption>A cat</figcaption></figure>`),
		},
		{
			name: "plain text split into paragraphs",
			raw:  "one\n\ntwo",
			want: ptr(`<p>one</p><p>two</p>`),
		},
		{
			name: "single line breaks become br",
			raw:  "one\ntwo\n\n\nthree",
			want: ptr(`<p>one<br/>two</p><p>three</p>`),
		},
		{
			name: "CRLF line breaks",
			raw:  "one\r\n\r\ntwo",
			want: ptr(`<p>one</p><p>two</p>`),
		},
		{
			name: "blank line with spaces and tabs",
			raw:  "  one \n \t\ntwo\n",
			want: ptr(`<p>one</p><p>two</p>`),
		},
		{
			name: "inline elements stay in their paragraph",
			raw:  "a <em>b</em>\n\nc",
			want: ptr(`<p>a <em>b</em></p><p>c</p>`),
		},
		{
			name: "single p wrapper reflowed",
			raw:  "<p>one\n\ntwo</p>",
			want: ptr(`<p>one</p><p>two</p>`),
		},
		{
			name: "single div wrapper reflowed",
			raw:  "\n<div>one\n\n<strong>two</strong></div>\n",
			want: ptr(`<p>one</p><p><strong>two</strong></p>`),
		},
		{
			name: "paragraphs left as they are",
			raw:  "<p>one</p>\n\n<p>two</p>",
			want: ptr("<p>one</p>\n\n<p>two</p>"),
		},
		{
			name: "text with br left as it is",
			raw:  "one<br>two\n\nthree",
			want: ptr("one<br/>two\n\nthree"),
		},
		{
			name: "wrapper containing a block left as it is",
			raw:  "<div>one\n\n<p>two</p></div>",
			want: ptr("<div>one\n\n<p>two</p></div>"),
		},
		{
			name: "text without line breaks left as it is",
			raw:  "just text",
			want: ptr("just text"),
		},
		{
			name: "whitespace-only paragraphs dropped",
			raw:  "one\n\n \n\ntwo",
			want: ptr(`<p>one</p><p>two</p>`),
		},
		{
			name: "escaped JSON Feed text shows tags as text",
			raw:  stdhtml.EscapeString("<b>not bold</b>\n\nnext"),
			want: ptr(`<p>&lt;b&gt;not bold&lt;/b&gt;</p><p>next</p>`),
		},
		{
			name: "bare URL linked without the trailing period",
			raw:  "see https://example.com/a.",
			want: ptr(`see <a href="https://example.com/a" rel="nofollow noreferrer noopener" target="_blank">https://example.com/a</a>.`),
		},
		{
			name: "bare URL keeps its matching parenthesis",
			raw:  "(https://en.wikipedia.org/wiki/Go_(language))",
			want: ptr(`(<a href="https://en.wikipedia.org/wiki/Go_(language)" rel="nofollow noreferrer noopener" target="_blank">https://en.wikipedia.org/wiki/Go_(language)</a>)`),
		},
		{
			name: "bare URLs linked in plain text paragraphs",
			raw:  "one http://example.com/x?a=1&b=2, two\n\nhttps://example.com/y!",
			want: ptr(`<p>one <a href="http://example.com/x?a=1&amp;b=2" rel="nofollow noreferrer noopener" target="_blank">http://example.com/x?a=1&amp;b=2</a>, two</p>` +
				`<p><a href="https://example.com/y" rel="nofollow noreferrer noopener" target="_blank">https://example.com/y</a>!</p>`),
		},
		{
			name: "bare URL linked inside HTML",
			raw:  "<p>Read <em>https://example.com/a</em></p>",
			has:  []string{`<em><a href="https://example.com/a"`},
		},
		{
			name: "URL in a link not wrapped again",
			raw:  `<a href="https://example.com/a">https://example.com/a</a>`,
			want: ptr(`<a href="https://example.com/a" rel="nofollow noreferrer noopener" target="_blank">https://example.com/a</a>`),
		},
		{
			name: "URL in pre and code not linked",
			raw:  `<pre>https://example.com/a</pre><p><code>https://example.com/b</code></p>`,
			want: ptr(`<pre>https://example.com/a</pre><p><code>https://example.com/b</code></p>`),
		},
		{
			name: "other schemes not linked",
			raw:  "ftp://x javascript:alert(1) https://",
			want: ptr("ftp://x javascript:alert(1) https://"),
		},
		{
			name: "empty input",
			raw:  ``,
			want: ptr(``),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(HTML(tt.raw, tt.base))
			if tt.want != nil && got != *tt.want {
				t.Errorf("HTML(%q)\n got: %s\nwant: %s", tt.raw, got, *tt.want)
			}
			for _, s := range tt.has {
				if !strings.Contains(got, s) {
					t.Errorf("HTML(%q) = %s; want it to contain %q", tt.raw, got, s)
				}
			}
			for _, s := range tt.hasNot {
				if strings.Contains(got, s) {
					t.Errorf("HTML(%q) = %s; want it not to contain %q", tt.raw, got, s)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestShowsImage(t *testing.T) {
	base, err := url.Parse("https://example.com/posts/1")
	if err != nil {
		t.Fatal(err)
	}
	const other = "https://cdn.example.org/other.jpg"

	tests := []struct {
		name     string
		raw      string
		base     *url.URL
		imageURL string
		want     bool
	}{
		// The content starts with an image.
		{name: "leading img", raw: `<img src="a.jpg"><p>text</p>`, imageURL: other, want: true},
		{name: "leading figure with caption", raw: `<figure><img src="a.jpg"><figcaption>c</figcaption></figure><p>t</p>`, imageURL: other, want: true},
		{name: "leading linked image in a paragraph", raw: `<p><a href="x"><img src="a.jpg"></a></p><p>t</p>`, imageURL: other, want: true},
		{name: "leading picture", raw: "<!-- c -->\n<picture><img src=\"a.jpg\"></picture><p>t</p>", imageURL: other, want: true},
		{name: "leading image in one wrapper", raw: `<div><img src="a.jpg"><p>t</p></div>`, imageURL: other, want: true},
		{name: "only one wrapper level", raw: `<div><div><img src="a.jpg"></div><p>t</p></div>`, imageURL: other, want: false},
		{name: "image with text in a paragraph", raw: `<p><img src="a.jpg"> text</p><p>t</p>`, imageURL: other, want: false},

		// The block after the first paragraph is an image.
		{name: "image after first paragraph", raw: `<p>intro</p><figure><img src="b.jpg"></figure><p>more</p>`, imageURL: other, want: true},
		{name: "image after first paragraph after a heading", raw: `<h2>h</h2><p>intro</p><img src="b.jpg">`, imageURL: other, want: true},
		{name: "image later on", raw: `<p>one</p><p>two</p><img src="b.jpg">`, imageURL: other, want: false},

		// The same image anywhere.
		{name: "same URL", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg">`, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "different query", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg?w=600">`, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "www and host case", raw: `<p>one</p><p>two</p><img src="https://WWW.Example.com/photo.jpg">`, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "relative src", raw: `<p>one</p><p>two</p><img src="/photo.jpg">`, base: base, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "WordPress size suffix", raw: `<p>one</p><p>two</p><img src="https://example.com/wp/photo-1024x768.jpg">`, imageURL: "https://example.com/wp/photo.jpg", want: true},
		{name: "nested in a table", raw: `<p>one</p><table><tr><td><img src="https://example.com/photo.jpg"></td></tr></table>`, imageURL: "https://example.com/photo.jpg", want: true},

		// No match.
		{name: "different file", raw: `<p>one</p><p>two</p><img src="https://example.com/photo2.jpg">`, imageURL: "https://example.com/photo.jpg", want: false},
		{name: "relative src without base", raw: `<p>one</p><p>two</p><img src="/photo.jpg">`, imageURL: "https://example.com/photo.jpg", want: false},
		{name: "unparseable imageURL", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg">`, imageURL: "https://example.com/%zz", want: false},
		{name: "empty content", raw: ``, imageURL: "https://example.com/photo.jpg", want: false},
		{name: "text only", raw: `<p>one</p><p>two</p>`, imageURL: "https://example.com/photo.jpg", want: false},

		// An empty imageURL only checks the position rules.
		{name: "empty imageURL, leading image", raw: `<img src="a.jpg"><p>t</p>`, want: true},
		{name: "empty imageURL, image after first paragraph", raw: `<p>intro</p><img src="a.jpg">`, want: true},
		{name: "empty imageURL, image later on", raw: `<p>one</p><p>two</p><img src="a.jpg">`, want: false},
		{name: "empty imageURL, img with empty src later on", raw: `<p>one</p><p>two</p><img src="">`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShowsImage(tt.raw, tt.base, tt.imageURL); got != tt.want {
				t.Errorf("ShowsImage(%q, %v, %q) = %v, want %v", tt.raw, tt.base, tt.imageURL, got, tt.want)
			}
		})
	}
}
