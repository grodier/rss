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
			raw:    `<img src=https://example.com/x onerror=alert(1)>`,
			has:    []string{`<img src="https://example.com/x"`},
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
			name:   "http image loads over https",
			raw:    `<img src="http://assets1.example.com/2017/picard.jpg"><a href="http://example.com/p">x</a>`,
			has:    []string{`src="https://assets1.example.com/2017/picard.jpg"`, `href="http://example.com/p"`},
			hasNot: []string{`src="http:`},
		},
		{
			name:   "relative image under an http base loads over https",
			raw:    `<img src="i.png">`,
			base:   &url.URL{Scheme: "http", Host: "example.com", Path: "/posts/1"},
			has:    []string{`src="https://example.com/posts/i.png"`},
			hasNot: []string{`src="http:`},
		},
		{
			name:   "nil base leaves URLs relative",
			raw:    `<a href="/p">x</a>`,
			has:    []string{`href="/p"`},
			hasNot: []string{"example.com"},
		},
		{
			// It can't load from the app's own address.
			name: "relative image without a base removed",
			raw:  `<p>a</p><p><img src="i.png"></p>`,
			want: ptr(`<p>a</p>`),
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
			name: "X embed becomes a post card",
			raw:  xEmbed,
			want: ptr(`<figure class="social-post" data-social="x"><figcaption><span class="social-name">Jane Doe</span> <span class="social-handle">@jane</span> <span class="social-network">on X</span></figcaption>` +
				`<blockquote cite="https://x.com/jane/status/1745101186790326273"><p lang="en" dir="ltr">Hello <a href="https://twitter.com/bob?ref_src=twsrc%5Etfw" rel="nofollow noreferrer noopener" target="_blank">@bob</a>` +
				` see <a href="https://t.co/xyz" rel="nofollow noreferrer noopener" target="_blank">example.com/page</a></p></blockquote>` +
				`<p class="social-foot"><time data-local="" datetime="2024-01-10T15:12:00Z">10 Jan 2024 15:12 UTC</time>` +
				` <a href="https://x.com/jane/status/1745101186790326273" rel="nofollow noreferrer noopener" target="_blank">See the full post on X ↗</a></p></figure> `),
			hasNot: []string{"pic.twitter.com", "twitter-tweet", "widgets.js", "<script", "January 10, 2024"},
		},
		{
			name:   "X embed on x.com with extra classes and attributes",
			raw:    `<blockquote class="twitter-tweet tw-align-center" data-dnt="true" data-theme="dark"><p>Hi</p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1745101186790326273?ref_src=twsrc%5Etfw">January 10, 2024</a></blockquote>`,
			has:    []string{`<figure class="social-post" data-social="x">`, `<p>Hi</p>`, `href="https://x.com/jane/status/1745101186790326273"`, "Read on X ↗"},
			hasNot: []string{"ref_src", "data-dnt", "data-theme", "tw-align-center"},
		},
		{
			name: "X video embed links to watch it",
			raw:  `<blockquote class="twitter-video"><p>Watch <a href="https://t.co/v">pic.twitter.com/v</a></p>&mdash; Jane (@jane) <a href="https://twitter.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:  []string{`data-social="x"`, `<p>Watch</p>`, "Watch on X ↗"},
		},
		{
			name:   "X picture on pic.x.com links to the full post",
			raw:    `<blockquote class="twitter-tweet"><p>Look <a href="https://t.co/a">pic.x.com/a</a></p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:    []string{`<p>Look</p>`, "See the full post on X ↗"},
			hasNot: []string{"pic.x.com"},
		},
		{
			name:   "X post with an ID older than snowflakes shows its date as published",
			raw:    `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1">May 2, 2010</a></blockquote>`,
			has:    []string{`<p class="social-foot"><span>May 2, 2010</span> <a href="https://x.com/jane/status/1" rel="nofollow noreferrer noopener" target="_blank">Read on X ↗</a></p>`},
			hasNot: []string{"<time"},
		},
		{
			name: "lazy X embed becomes a card without text",
			raw:  `<blockquote class="twitter-tweet" data-tweet-id="1745159068185526275"><a href="https://x.com/jane/status/1745159068185526275"></a></blockquote>`,
			want: ptr(`<figure class="social-post" data-social="x"><figcaption><span class="social-handle">@jane</span> <span class="social-network">on X</span></figcaption>` +
				`<p class="social-missing">This post&#39;s text isn&#39;t in the article.</p>` +
				`<p class="social-foot"><time data-local="" datetime="2024-01-10T19:02:00Z">10 Jan 2024 19:02 UTC</time>` +
				` <a href="https://x.com/jane/status/1745159068185526275" rel="nofollow noreferrer noopener" target="_blank">Read on X ↗</a></p></figure>`),
		},
		{
			name:   "lazy X embed whose link text is the URL",
			raw:    `<blockquote class="twitter-tweet"><a href="https://twitter.com/jane/status/123">https://twitter.com/jane/status/123</a></blockquote>`,
			has:    []string{"social-missing", "Read on X ↗"},
			hasNot: []string{"<blockquote", ">https://twitter.com"},
		},
		{
			name:   "X /i/web/status link has no handle",
			raw:    `<blockquote class="twitter-tweet"><a href="https://x.com/i/web/status/123"></a></blockquote>`,
			has:    []string{`href="https://x.com/i/web/status/123"`, `<figcaption><span class="social-network">A post on X</span></figcaption>`},
			hasNot: []string{"social-handle"},
		},
		{
			name: "X embed in a WordPress wrapper",
			raw:  `<figure class="wp-block-embed is-type-rich is-provider-twitter wp-block-embed-twitter"><div class="wp-block-embed__wrapper">` + xEmbed + `</div></figure>`,
			has:  []string{`<figure><div><figure class="social-post" data-social="x">`, `datetime="2024-01-10T15:12:00Z"`},
		},
		{
			name:   "X embed iframe becomes a card without text",
			raw:    `<iframe src="https://platform.twitter.com/embed/Tweet.html?id=1745159068185526275&dnt=true"></iframe>`,
			has:    []string{`<figure class="social-post" data-social="x"><figcaption><span class="social-network">A post on X</span></figcaption>`, "social-missing", `datetime="2024-01-10T19:02:00Z"`, `href="https://x.com/i/web/status/1745159068185526275"`},
			hasNot: []string{"Open embedded content"},
		},
		{
			name:   "X embed with an unmatched byline takes the handle from the link",
			raw:    `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane Doe <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:    []string{`<span class="social-handle">@jane</span>`, "<span>May 2, 2025</span>"},
			hasNot: []string{"social-name"},
		},
		{
			name: "X author name with parentheses",
			raw:  `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane (work) Doe (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:  []string{`<span class="social-name">Jane (work) Doe</span>`},
		},
		{
			name:   "X author name with markup characters stays text",
			raw:    `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; &lt;b&gt;Jane&lt;/b&gt; (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:    []string{`<span class="social-name">&lt;b&gt;Jane&lt;/b&gt;</span>`},
			hasNot: []string{"<b>"},
		},
		{
			name: "X embed without a status link stays a blockquote",
			raw:  `<blockquote class="twitter-tweet"><p>Hi</p>&mdash; Jane (@jane) <a href="https://x.com/jane">May 2, 2025</a></blockquote>`,
			want: ptr(`<blockquote><p>Hi</p>— Jane (@jane) <a href="https://x.com/jane" rel="nofollow noreferrer noopener" target="_blank">May 2, 2025</a></blockquote>`),
		},
		{
			name:   "X status link with a bad handle stays a blockquote",
			raw:    `<blockquote class="twitter-tweet"><a href="https://x.com/ja-ne/status/1">x</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "X status link with a 16-character handle stays a blockquote",
			raw:    `<blockquote class="twitter-tweet"><a href="https://x.com/abcdefghijklmnop/status/1">x</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "X status link with a non-numeric ID stays a blockquote",
			raw:    `<blockquote class="twitter-tweet"><a href="https://x.com/jane/status/12a">x</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "status link on another host stays a blockquote",
			raw:    `<blockquote class="twitter-tweet"><a href="https://evil.example/jane/status/1">x</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "blockquote without the class stays a blockquote",
			raw:    `<blockquote><p>Hi</p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "javascript link inside an X embed removed",
			raw:    `<blockquote class="twitter-tweet"><p>Hi <a href="javascript:alert(1)">x</a></p>&mdash; Jane (@jane) <a href="https://x.com/jane/status/1">May 2, 2025</a></blockquote>`,
			has:    []string{"social-post"},
			hasNot: []string{"javascript"},
		},
		{
			name: "Bluesky embed becomes a post card",
			raw:  blueskyEmbed,
			want: ptr(`<figure class="social-post" data-social="bluesky"><figcaption><span class="social-name">Jane Doe</span> <span class="social-handle">@jane<wbr/>.bsky<wbr/>.social</span> <span class="social-network">on Bluesky</span></figcaption>` +
				`<blockquote cite="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227"><p lang="en">The post&#39;s text</p></blockquote>` +
				`<p class="social-foot"><time data-local="" datetime="2025-01-01T15:00:00Z">1 Jan 2025 15:00 UTC</time>` +
				` <a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227" rel="nofollow noreferrer noopener" target="_blank">See the full post on Bluesky ↗</a></p></figure>`),
			hasNot: []string{"[image or embed]", "ref_src", "bluesky-embed", "embed.js", "<script", "<br"},
		},
		{
			name:   "Bluesky post without media keeps its line breaks",
			raw:    `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p>One<br><br>Two</p>&mdash; Jane (<a href="https://bsky.app/profile/did:plc:abc123">@jane.bsky.social</a>) <a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227">January 1, 2025 at 3:00 PM</a></blockquote>`,
			has:    []string{"<p>One<br/><br/>Two</p>", "Read on Bluesky ↗"},
			hasNot: []string{"Watch on"},
		},
		{
			name: "Bluesky post whose key isn't a TID shows its date as published",
			raw:  `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/self"><p>Hi</p>&mdash; Jane (<a href="https://bsky.app/profile/did:plc:abc123">@jane.bsky.social</a>) <a href="https://bsky.app/profile/did:plc:abc123/post/self">January 1, 2025 at 3:00 PM</a></blockquote>`,
			has:  []string{`<span>January 1, 2025 at 3:00 PM</span>`},
		},
		{
			name: "Bluesky embed found by its post link",
			raw:  `<blockquote class="bluesky-embed"><p>Hi</p>&mdash; Jane (<a href="https://bsky.app/profile/jane.example.com">@jane.example.com</a>) <a href="https://bsky.app/profile/jane.example.com/post/3leotaf7z2227?ref_src=embed">January 1, 2025</a></blockquote>`,
			has:  []string{`data-social="bluesky"`, `<span class="social-handle">@jane<wbr/>.example<wbr/>.com</span>`, `href="https://bsky.app/profile/jane.example.com/post/3leotaf7z2227"`},
		},
		{
			name:   "Bluesky embed iframe becomes a card without text",
			raw:    `<iframe src="https://embed.bsky.app/embed/did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"></iframe>`,
			has:    []string{`data-social="bluesky"`, "social-missing", `datetime="2025-01-01T15:00:00Z"`, `href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227"`},
			hasNot: []string{"Open embedded content"},
		},
		{
			name:   "Bluesky byline that doesn't match shows no DID",
			raw:    `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227"><p>Hi</p><a href="https://bsky.app/profile/did:plc:abc123">did:plc:abc123</a></blockquote>`,
			has:    []string{`<figcaption><span class="social-network">A post on Bluesky</span></figcaption>`},
			hasNot: []string{"social-name", "social-handle", ">did:plc"},
		},
		{
			name:   "Bluesky embed naming another record type stays a blockquote",
			raw:    `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.like/3leotaf7z2227"><p>Hi</p></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name:   "Bluesky post link on another host stays a blockquote",
			raw:    `<blockquote class="bluesky-embed"><p>Hi</p><a href="https://evil.example/profile/jane/post/3leotaf7z2227">x</a></blockquote>`,
			hasNot: []string{"social-post"},
		},
		{
			name: "feed-written Bluesky post card keeps data-social",
			raw:  `<figure class="social-post" data-social="bluesky">x</figure>`,
			want: ptr(`<figure class="social-post" data-social="bluesky">x</figure>`),
		},
		{
			name: "feed-written post card with an unknown network loses data-social",
			raw:  `<figure class="social-post" data-social="evil">x</figure>`,
			want: ptr(`<figure class="social-post">x</figure>`),
		},
		{
			name: "feed-written post card classes and data-local with a value are dropped",
			raw:  `<span class="social-name evil">x</span><p class="social-foot">y</p><time data-local="date" datetime="2024-01-01">z</time>`,
			want: ptr(`<span>x</span><p class="social-foot">y</p><time datetime="2024-01-01">z</time>`),
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
			got := string(HTML(tt.raw, tt.base, ""))
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

func TestHTMLLazyImages(t *testing.T) {
	base, err := url.Parse("https://example.com/posts/1")
	if err != nil {
		t.Fatal(err)
	}
	const (
		svg   = `data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22/%3E`
		attrs = ` loading="lazy" referrerpolicy="no-referrer"/>`
	)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "data: placeholder and data-src",
			raw:  `<p><img class="lazyload" src="` + svg + `" data-src="https://cdn.example.com/a.jpg" alt="A"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg" alt="A"` + attrs + `</p>`,
		},
		{
			name: "no src, data-original",
			raw:  `<p><img data-original="https://cdn.example.com/a.jpg"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "data-lazy-src alone",
			raw:  `<p><img data-lazy-src="https://cdn.example.com/a.jpg"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "empty src, data-src and data-original",
			raw:  `<p><img src="" data-original="https://cdn.example.com/b.jpg" data-src="https://cdn.example.com/a.jpg"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "real src wins over data-src",
			raw:  `<p><img src="https://cdn.example.com/a.jpg" data-src="https://cdn.example.com/b.jpg"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "relative data-src resolved",
			raw:  `<p><img src="` + svg + `" data-src="/a.jpg"></p>`,
			want: `<p><img src="https://example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "http data-src loads over https",
			raw:  `<p><img data-src="http://cdn.example.com/a.jpg"></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `</p>`,
		},
		{
			name: "placeholder followed by a noscript image",
			raw:  `<p><img class="lazyload" src="` + svg + `"> <noscript><img src="https://cdn.example.com/a.jpg" alt="A"></noscript></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg" loading="lazy" referrerpolicy="no-referrer" alt="A"/> </p>`,
		},
		{
			name: "placeholder's alt kept",
			raw:  `<p><img src="" alt="B"><noscript><img src="https://cdn.example.com/a.jpg" alt="A"></noscript></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg" alt="B"` + attrs + `</p>`,
		},
		{
			name: "noscript image alone in a paragraph",
			raw:  `<p><noscript><img src="/a.jpg" alt="A"></noscript></p>`,
			want: `<p><img src="https://example.com/a.jpg" alt="A"` + attrs + `</p>`,
		},
		{
			name: "noscript image after an image with a source",
			raw:  `<p><img src="https://cdn.example.com/a.jpg"><noscript><img src="https://cdn.example.com/b.jpg"></noscript></p>`,
			want: `<p><img src="https://cdn.example.com/a.jpg"` + attrs + `<img src="https://cdn.example.com/b.jpg"` + attrs + `</p>`,
		},
		{
			name: "noscript with anything else removed",
			raw:  `<p>a</p><noscript><p>Turn on JavaScript</p></noscript>`,
			want: `<p>a</p>`,
		},
		{
			name: "noscript with an image without a source removed",
			raw:  `<p>a</p><noscript><img src="data:image/gif;base64,AAAA"></noscript>`,
			want: `<p>a</p>`,
		},
		{
			name: "placeholder alone in a paragraph removed with it",
			raw:  `<p>a</p><p><img src="` + svg + `"></p>`,
			want: `<p>a</p>`,
		},
		{
			name: "placeholder in a paragraph with text removed alone",
			raw:  `<p><img src="` + svg + `">text</p>`,
			want: `<p>text</p>`,
		},
		{
			name: "placeholder in a link in a figure removed with them",
			raw:  `<p>a</p><figure><a href="/x"><img src="` + svg + `"></a><figcaption>c</figcaption></figure>`,
			want: `<p>a</p>`,
		},
		{
			name: "empty anchor kept",
			raw:  `<p><a id="top"></a>a<img></p>`,
			want: `<p><a id="body-top"></a>a</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(HTML(tt.raw, base, "")); got != tt.want {
				t.Errorf("HTML(%q)\n got: %s\nwant: %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestHTMLLocalAnchors(t *testing.T) {
	base, err := url.Parse("https://example.com/2026/post/")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  string
		base *url.URL
		want string
	}{
		{
			name: "Kramdown footnote",
			raw:  `<p>a<sup id="fnref:1"><a href="#fn:1">1</a></sup></p><ol><li id="fn:1"><p>note <a href="#fnref:1">↩</a></p></li></ol>`,
			base: base,
			want: `<p>a<sup id="body-fnref:1"><a href="#body-fn:1" rel="nofollow">1</a></sup></p><ol><li id="body-fn:1"><p>note <a href="#body-fnref:1" rel="nofollow">↩</a></p></li></ol>`,
		},
		{
			name: "Substack footnote",
			raw:  `<p>a<a class="footnote-anchor" id="footnote-anchor-1" href="https://example.com/2026/post/#footnote-1">1</a></p><div class="footnote"><a id="footnote-1" href="https://example.com/2026/post/#footnote-anchor-1">1</a><p>note</p></div>`,
			base: base,
			want: `<p>a<a href="#body-footnote-1" id="body-footnote-anchor-1" rel="nofollow">1</a></p><div><a href="#body-footnote-anchor-1" id="body-footnote-1" rel="nofollow">1</a><p>note</p></div>`,
		},
		{
			name: "full link without the trailing slash, host in capitals",
			raw:  `<a href="https://EXAMPLE.com/2026/post#note">1</a><p id="note">n</p>`,
			base: base,
			want: `<a href="#body-note" rel="nofollow">1</a><p id="body-note">n</p>`,
		},
		{
			name: "relative link to the article",
			raw:  `<a href="/2026/post/#note">1</a><p id="note">n</p>`,
			base: base,
			want: `<a href="#body-note" rel="nofollow">1</a><p id="body-note">n</p>`,
		},
		{
			name: "full link to the article with a fragment not in it",
			raw:  `<a href="https://example.com/2026/post/#comments">c</a>`,
			base: base,
			want: `<a href="https://example.com/2026/post/#comments" rel="nofollow noreferrer noopener" target="_blank">c</a>`,
		},
		{
			name: "full link to the article with another query",
			raw:  `<a href="https://example.com/2026/post/?page=2#note">1</a><p id="note">n</p>`,
			base: base,
			want: `<a href="https://example.com/2026/post/?page=2#note" rel="nofollow noreferrer noopener" target="_blank">1</a><p id="body-note">n</p>`,
		},
		{
			name: "fragment not in the article opens the original",
			raw:  `<a href="#top">top</a>`,
			base: base,
			want: `<a href="https://example.com/2026/post/#top" rel="nofollow noreferrer noopener" target="_blank">top</a>`,
		},
		{
			name: "fragment not in the article without a base",
			raw:  `<a href="#top">top</a>`,
			want: `top`,
		},
		{
			name: "fragment in the article without a base",
			raw:  `<a href="#fn1">1</a><p id="fn1">n</p>`,
			want: `<a href="#body-fn1" rel="nofollow">1</a><p id="body-fn1">n</p>`,
		},
		{
			name: "link to another page with a fragment",
			raw:  `<a href="https://example.com/2026/other/#note">1</a><p id="note">n</p>`,
			base: base,
			want: `<a href="https://example.com/2026/other/#note" rel="nofollow noreferrer noopener" target="_blank">1</a><p id="body-note">n</p>`,
		},
		{
			name: "old-style anchor",
			raw:  `<a href="#fn1">1</a><p><a name="fn1"></a>note</p>`,
			base: base,
			want: `<a href="#body-fn1" rel="nofollow">1</a><p><a id="body-fn1"></a>note</p>`,
		},
		{
			name: "old-style anchor with an id keeps its id",
			raw:  `<a href="#fn1">1</a><p><a name="fn1" id="note1"></a>note</p>`,
			base: base,
			want: `<a href="https://example.com/2026/post/#fn1" rel="nofollow noreferrer noopener" target="_blank">1</a><p><a id="body-note1"></a>note</p>`,
		},
		{
			name: "id with whitespace removed",
			raw:  `<p id="Hello World">a</p><p id="">b</p>`,
			base: base,
			want: `<p>a</p><p>b</p>`,
		},
		{
			name: "repeated id removed from the later element",
			raw:  `<p id="x">a</p><p id="x">b</p>`,
			base: base,
			want: `<p id="body-x">a</p><p>b</p>`,
		},
		{
			name: "id that is the page's own",
			raw:  `<a href="#debug">d</a><p id="debug">d</p>`,
			base: base,
			want: `<a href="#body-debug" rel="nofollow">d</a><p id="body-debug">d</p>`,
		},
		{
			name: "percent-encoded fragment",
			raw:  `<a href="#caf%C3%A9">1</a><p id="café">n</p>`,
			base: base,
			want: `<a href="#body-caf%C3%A9" rel="nofollow">1</a><p id="body-café">n</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(HTML(tt.raw, tt.base, "")); got != tt.want {
				t.Errorf("HTML(%q)\n got: %s\nwant: %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestHTMLDemotesH1(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{
			name: "attributes and content kept",
			raw:  `<h1 id="a">A <em>b</em></h1>`,
			want: `<h2 id="body-a">A <em>b</em></h2>`,
		},
		{
			name: "every h1",
			raw:  `<h1>One</h1><p>a</p><div><h1>Two</h1></div>`,
			want: `<h2>One</h2><p>a</p><div><h2>Two</h2></div>`,
		},
		{
			name: "other levels unchanged",
			raw:  `<h2>2</h2><h3>3</h3><h4>4</h4><h5>5</h5><h6>6</h6>`,
			want: `<h2>2</h2><h3>3</h3><h4>4</h4><h5>5</h5><h6>6</h6>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(HTML(tt.raw, nil, "")); got != tt.want {
				t.Errorf("HTML(%q)\n got: %s\nwant: %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestHTMLDropsRepeatedTitle(t *testing.T) {
	const title = "The Big Story"
	tests := []struct {
		name, raw, title, want string
	}{
		{
			name:  "h1 repeating the title",
			raw:   `<h1>The Big Story</h1><p>Text</p>`,
			title: title,
			want:  `<p>Text</p>`,
		},
		{
			name:  "case, spaces and a permalink mark",
			raw:   `<h2>  the big  story ¶</h2><p>Text</p>`,
			title: title,
			want:  `<p>Text</p>`,
		},
		{
			name:  "other permalink marks",
			raw:   `<h3>The Big Story <a href="#x">#</a></h3><p>Text</p>`,
			title: title,
			want:  `<p>Text</p>`,
		},
		{
			name:  "linked heading",
			raw:   `<h2><a href="https://example.com/post">The Big Story</a></h2><p>Text</p>`,
			title: title,
			want:  `<p>Text</p>`,
		},
		{
			name:  "inside a single div wrapper",
			raw:   `<div> <h2>The Big Story</h2> <p>Text</p> </div>`,
			title: title,
			want:  `<div>  <p>Text</p> </div>`,
		},
		{
			name:  "a title ending in a mark",
			raw:   `<h2>Learning C#</h2><p>Text</p>`,
			title: "Learning C#",
			want:  `<p>Text</p>`,
		},
		{
			name:  "after an empty anchor",
			raw:   `<a id="top"></a><h2>The Big Story</h2><p>Text</p>`,
			title: title,
			want:  `<a id="body-top"></a><p>Text</p>`,
		},
		{
			name:  "only block after an empty paragraph kept",
			raw:   `<p>&nbsp;</p><h2>The Big Story</h2>`,
			title: title,
			want:  "<p>\u00a0</p><h2>The Big Story</h2>",
		},
		{
			name:  "not first kept",
			raw:   `<p>Intro</p><h2>The Big Story</h2>`,
			title: title,
			want:  `<p>Intro</p><h2>The Big Story</h2>`,
		},
		{
			name:  "another heading kept",
			raw:   `<h2>Another heading</h2><p>Text</p>`,
			title: title,
			want:  `<h2>Another heading</h2><p>Text</p>`,
		},
		{
			name:  "a paragraph repeating the title kept",
			raw:   `<p>The Big Story</p><p>Text</p>`,
			title: title,
			want:  `<p>The Big Story</p><p>Text</p>`,
		},
		{
			name:  "only block kept",
			raw:   `<h1>The Big Story</h1>`,
			title: title,
			want:  `<h2>The Big Story</h2>`,
		},
		{
			name:  "no title",
			raw:   `<h2>The Big Story</h2><p>Text</p>`,
			title: "",
			want:  `<h2>The Big Story</h2><p>Text</p>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(HTML(tt.raw, nil, tt.title)); got != tt.want {
				t.Errorf("HTML(%q, %q)\n got: %s\nwant: %s", tt.raw, tt.title, got, tt.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// blueskyEmbed is a post embedded from Bluesky, as embed.bsky.app writes it,
// with a picture.
const blueskyEmbed = `<blockquote class="bluesky-embed" data-bluesky-uri="at://did:plc:abc123/app.bsky.feed.post/3leotaf7z2227" data-bluesky-cid="bafyreiabc" data-bluesky-embed-color-mode="system"><p lang="en">The post's text<br><br><a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227?ref_src=embed">[image or embed]</a></p>&mdash; Jane Doe (<a href="https://bsky.app/profile/did:plc:abc123?ref_src=embed">@jane.bsky.social</a>) <a href="https://bsky.app/profile/did:plc:abc123/post/3leotaf7z2227?ref_src=embed">January 1, 2025 at 3:00 PM</a></blockquote><script async src="https://embed.bsky.app/static/embed.js" charset="utf-8"></script>`

// xEmbed is a post embedded from X, as X's publish tool writes it, with a
// picture.
const xEmbed = `<blockquote class="twitter-tweet"><p lang="en" dir="ltr">Hello <a href="https://twitter.com/bob?ref_src=twsrc%5Etfw">@bob</a> see <a href="https://t.co/xyz">example.com/page</a> <a href="https://t.co/abc">pic.twitter.com/abc</a></p>&mdash; Jane Doe (@jane) <a href="https://twitter.com/jane/status/1745101186790326273?ref_src=twsrc%5Etfw">January 10, 2024</a></blockquote> <script async src="https://platform.twitter.com/widgets.js" charset="utf-8"></script>`

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
		{name: "empty anchor before a leading image", raw: `<a id="top"></a><img src="a.jpg"><p>t</p>`, imageURL: other, want: true},
		{name: "nbsp before a leading image", raw: `&nbsp; <img src="a.jpg"><p>t</p>`, imageURL: other, want: true},
		{name: "line break before a leading image", raw: `<br><img src="a.jpg"><p>t</p>`, imageURL: other, want: true},
		{name: "empty paragraph before a leading image", raw: `<p>&nbsp;</p><figure><img src="a.jpg"></figure><p>t</p>`, imageURL: other, want: true},
		{name: "empty anchor before a wrapper", raw: `<a id="top"></a><div><img src="a.jpg"><p>t</p></div>`, imageURL: other, want: true},
		{name: "empty blocks inside a wrapper", raw: `<div><span></span><br><img src="a.jpg"><p>t</p></div>`, imageURL: other, want: true},
		{name: "paragraph with an embed isn't empty", raw: `<p><iframe src="https://example.com/e"></iframe></p><p>intro</p><img src="a.jpg">`, imageURL: other, want: false},
		{name: "only one wrapper level", raw: `<div><div><img src="a.jpg"></div><p>t</p></div>`, imageURL: other, want: false},
		{name: "image with text in a paragraph", raw: `<p><img src="a.jpg"> text</p><p>t</p>`, imageURL: other, want: false},

		// The block after the first paragraph is an image.
		{name: "image after first paragraph", raw: `<p>intro</p><figure><img src="b.jpg"></figure><p>more</p>`, imageURL: other, want: true},
		{name: "image after first paragraph after a heading", raw: `<h2>h</h2><p>intro</p><img src="b.jpg">`, imageURL: other, want: true},
		{name: "image later on", raw: `<p>one</p><p>two</p><img src="b.jpg">`, imageURL: other, want: false},

		// The first paragraph can be loose text, outside a <p>.
		{name: "image after loose opening text", raw: `text <p><img src="b.jpg"></p>`, imageURL: other, want: true},
		{name: "image after loose text with inline elements", raw: `Some <em>text</em> and <a href="x">a link</a><br>more <img src="b.jpg">`, imageURL: other, want: true},
		{name: "image after loose text in a wrapper", raw: `<div>text <figure><img src="b.jpg"></figure><p>t</p></div>`, imageURL: other, want: true},
		{name: "heading, loose text, image", raw: `<h2>h</h2>text <img src="b.jpg">`, imageURL: other, want: true},
		{name: "loose text then a paragraph then an image", raw: `text <p>more</p><img src="b.jpg">`, imageURL: other, want: false},
		{name: "paragraph then loose text then an image", raw: `<p>intro</p>more <img src="b.jpg">`, imageURL: other, want: false},
		{name: "image inside loose text", raw: `text <span>with <img src="b.jpg"> in it</span><p>t</p>`, imageURL: other, want: false},
		{name: "image after an inline element holding a block", raw: `<span><p>card</p></span><img src="b.jpg">`, imageURL: other, want: false},
		{name: "image after a video", raw: `<video src="v.mp4"></video><img src="b.jpg"><p>t</p>`, imageURL: other, want: false},
		{name: "loose text only", raw: `just text`, imageURL: other, want: false},
		{name: "linked image after loose text", raw: `text <a href="x"><img src="b.jpg"></a><p>t</p>`, imageURL: other, want: true},
		{name: "image after loose text ending in a line break", raw: `text<br><img src="b.jpg">`, imageURL: other, want: true},
		{name: "image after loose paragraphs split by line breaks", raw: `One.<br><br>Two.<br><br><img src="b.jpg">`, imageURL: other, want: false},
		{name: "image after loose paragraphs split by a blank line", raw: "One.\n\nTwo. <img src=\"b.jpg\">", imageURL: other, want: false},
		{name: "image after loose text ending in a blank line", raw: "One.\n\n<img src=\"b.jpg\">", imageURL: other, want: true},
		{name: "image after loose text with one line break", raw: `One.<br>Two.<br><img src="b.jpg">`, imageURL: other, want: true},
		{name: "image inside the opening line", raw: `Great news <img src="b.jpg"> for everyone.<p>Second</p>`, imageURL: other, want: false},
		{name: "image after a paragraph, then text", raw: `<p>intro</p><img src="b.jpg"> more text`, imageURL: other, want: false},
		{name: "image after loose text, then a line break and text", raw: `text<br><img src="b.jpg"><br>more`, imageURL: other, want: true},
		{name: "tracking pixel after loose text", raw: `Summary of the post.<img src="http://feeds.feedburner.com/~r/X/~4/abc" height="1" width="1" alt=""/>`, imageURL: other, want: false},
		{name: "tracking pixel first", raw: `<img src="https://example.com/p.gif" width="0" height="0"><p>t</p>`, imageURL: other, want: false},
		{name: "emoji after loose text", raw: `Thanks! <img class="wp-smiley emoji" src="https://s.w.org/images/core/emoji/smile.png" alt="🙂"><p>Second</p>`, imageURL: other, want: false},
		{name: "sized image after loose text", raw: `text <img src="b.jpg" width="600" height="400">`, imageURL: other, want: true},
		{name: "empty paragraph before the first paragraph", raw: `<p>&nbsp;</p><p>intro</p><img src="b.jpg">`, imageURL: other, want: true},
		{name: "empty paragraph, then two paragraphs, then an image", raw: `<p>&nbsp;</p><p>intro</p><p>two</p><img src="b.jpg">`, imageURL: other, want: false},
		{name: "empty anchor before the first paragraph", raw: `<a id="top"></a><p>intro</p><img src="b.jpg">`, imageURL: other, want: true},
		{name: "nbsp before the first paragraph", raw: `&nbsp;<p>intro</p><img src="b.jpg">`, imageURL: other, want: true},
		{
			name:     "real case: loose text, then a resized copy of the card image",
			raw:      `You don&#39;t have to leave your precious PS5 behind when flying, but you will need to take some precautions or opt for a long-distance streaming relationship. <p><img src="https://www.engadget.com/img/gallery/what-is-the-best-way-to-secure-your-playstation-5-for-a-flight/intro-1794602643.jpg" /></p>`,
			imageURL: "https://www.engadget.com/img/gallery/what-is-the-best-way-to-secure-your-playstation-5-for-a-flight/l-intro-1794602643.jpg",
			want:     true,
		},

		// The same image anywhere.
		{name: "same URL", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg">`, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "different query", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg?w=600">`, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "http and https", raw: `<p>one</p><p>two</p><img src="https://example.com/photo.jpg">`, imageURL: "http://example.com/photo.jpg", want: true},
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
		{name: "empty imageURL, image after loose text", raw: `text <p><img src="b.jpg"></p>`, want: true},
		{name: "empty imageURL, img with empty src later on", raw: `<p>one</p><p>two</p><img src="">`, want: false},

		// Lazy-loaded images.
		{name: "leading lazy img", raw: `<p><img src="data:image/gif;base64,AAAA" data-src="a.jpg"></p><p>t</p>`, imageURL: other, want: true},
		{name: "leading placeholder without a lazy attribute", raw: `<p><img src="data:image/gif;base64,AAAA"></p><p>t</p>`, want: false},
		{name: "leading placeholder, imageURL elsewhere", raw: `<p><img src="data:image/gif;base64,AAAA"></p><p>t</p>`, imageURL: other, want: false},
		{name: "data-src same as imageURL", raw: `<p>one</p><p>two</p><img src="data:image/gif;base64,AAAA" data-src="/photo.jpg">`, base: base, imageURL: "https://example.com/photo.jpg", want: true},
		{name: "real src wins over data-src for the same image", raw: `<p>one</p><p>two</p><img src="/other.jpg" data-src="/photo.jpg">`, base: base, imageURL: "https://example.com/photo.jpg", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShowsImage(tt.raw, tt.base, tt.imageURL); got != tt.want {
				t.Errorf("ShowsImage(%q, %v, %q) = %v, want %v", tt.raw, tt.base, tt.imageURL, got, tt.want)
			}
		})
	}
}
