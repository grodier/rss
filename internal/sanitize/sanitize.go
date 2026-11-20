// Package sanitize makes untrusted article HTML safe to render.
package sanitize

import (
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// policy is the allowlist applied after the pre-pass. A bluemonday policy is
// safe for concurrent use once built.
var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^lazy$`)).OnElements("img")
	p.AllowAttrs("referrerpolicy").Matching(regexp.MustCompile(`^no-referrer$`)).OnElements("img")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^(embed|social-post)$`)).OnElements("figure")
	p.AllowAttrs("data-social").Matching(regexp.MustCompile(`^(x|bluesky)$`)).OnElements("figure")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^social-(foot|missing)$`)).OnElements("p")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^social-(name|handle|network)$`)).OnElements("span")
	p.AllowAttrs("data-local").Matching(regexp.MustCompile(`^$`)).OnElements("time")
	p.AllowAttrs("data-embed").Matching(regexp.MustCompile(`^(youtube|vimeo)$`)).OnElements("figure")
	p.AllowAttrs("data-lang").Matching(languageNames).OnElements("pre")
	p.AllowAttrs("data-embed-id").Matching(regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)).OnElements("figure")
	return p
}

// HTML returns raw as HTML that is safe to render: relative URLs in href and
// src are resolved against base (left as is if base is nil), http images
// load over https (see httpsImage), posts embedded from X and Bluesky become
// post cards (see xPost, blueskyPost and socialCard), YouTube and
// Vimeo iframes become click-to-play previews (see videoEmbed), code blocks
// name their language in data-lang (see labelCode), every other
// <iframe> with an http(s) src is replaced by <p><a href="SRC">Open embedded
// content</a></p>, plain text is split into paragraphs (see reflow), bare
// http(s) URLs become links (see linkify), images a script would have
// lazy-loaded show (see imageSource and noscriptImage), images left without an
// http(s) source are removed (see dropEmptyImages), and everything bluemonday's policy
// doesn't allow is removed.
//
// It is the one place the app vouches for untrusted HTML: article HTML is
// never converted to template.HTML anywhere else.
func HTML(raw string, base *url.URL) template.HTML {
	return template.HTML(policy.Sanitize(prepare(raw, base)))
}

// prepare parses raw as a fragment of a <div>, rewrites it (see rewrite),
// drops images without a source (see dropEmptyImages), reflows it (see reflow), links its bare URLs (see linkify) and renders it
// back. If raw can't be parsed or rendered it is returned as is;
// the policy still makes it safe, it just misses the rewrites.
func prepare(raw string, base *url.URL) string {
	root, err := parse(raw)
	if err != nil {
		return raw
	}
	rewrite(root, base)
	dropEmptyImages(root)
	reflow(root)
	linkify(root)
	var b strings.Builder
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if err := html.Render(&b, n); err != nil {
			return raw
		}
	}
	return b.String()
}

// parse parses raw as the children of a <div>, which it returns.
func parse(raw string) (*html.Node, error) {
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw), root)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		// ParseFragment returns detached nodes; attach them so rewrite can
		// replace or remove top-level iframes too.
		root.AppendChild(n)
	}
	return root, nil
}

// ShowsImage reports whether the article HTML raw already shows an image
// where a lead image would go, so the page shouldn't add imageURL above it:
// its first block is an image, or the block right after its first paragraph
// is one, or any <img> in it is the same image as imageURL. Relative URLs
// resolve against base (nil leaves them as they are). An empty imageURL only
// checks the first two.
//
// Blocks are root's children, or the children of its only element if that
// is a <div>, <article> or <section> wrapper (see blocks).
func ShowsImage(raw string, base *url.URL, imageURL string) bool {
	root, err := parse(raw)
	if err != nil {
		return false
	}

	bs := blocks(root)
	if len(bs) == 1 {
		switch bs[0].DataAtom {
		case atom.Div, atom.Article, atom.Section:
			bs = blocks(bs[0])
		}
	}
	if len(bs) > 0 && isImageBlock(bs[0]) {
		return true
	}
	for i, b := range bs {
		if b.DataAtom == atom.P && !isImageBlock(b) {
			if i+1 < len(bs) && isImageBlock(bs[i+1]) {
				return true
			}
			break
		}
	}

	if imageURL == "" {
		return false
	}
	want, ok := imageKey(imageURL, base)
	if !ok {
		return false
	}
	return hasImage(root, base, want)
}

// blocks returns the children of n that aren't comments or whitespace-only
// text.
func blocks(n *html.Node) []*html.Node {
	var bs []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.CommentNode:
		case c.Type == html.TextNode && strings.Trim(c.Data, htmlSpace) == "":
		default:
			bs = append(bs, c)
		}
	}
	return bs
}

// isImageBlock reports whether n is an <img> with a source (see
// imageSource) or a <picture>, or a <figure>,
// <p>, <a> or <span> whose only content (besides whitespace, comments and,
// in a <figure>, a <figcaption>) is an image block.
func isImageBlock(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.DataAtom {
	case atom.Img:
		_, ok := imageSource(n, nil)
		return ok
	case atom.Picture:
		return true
	case atom.Figure, atom.P, atom.A, atom.Span:
	default:
		return false
	}
	var content []*html.Node
	for _, c := range blocks(n) {
		if n.DataAtom == atom.Figure && c.DataAtom == atom.Figcaption {
			continue
		}
		content = append(content, c)
	}
	return len(content) == 1 && isImageBlock(content[0])
}

// hasImage reports whether an <img> under n shows an image (see
// imageSource) with the image key want.
func hasImage(n *html.Node, base *url.URL, want string) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.DataAtom == atom.Img {
			if src, ok := imageSource(c, base); ok {
				if key, ok := imageKey(src, base); ok && key == want {
					return true
				}
			}
		}
		if hasImage(c, base, want) {
			return true
		}
	}
	return false
}

// sizeSuffix matches the size WordPress adds to the resized copies of an
// image, before the extension: photo-1024x768.jpg is a copy of photo.jpg.
var sizeSuffix = regexp.MustCompile(`-\d+x\d+(\.[^./]+)$`)

// imageKey returns rawURL, resolved against base, in a form that is the same
// for copies of one image: https for http (see httpsImage), the host
// lowercased and without "www.", no query or fragment, and no WordPress size
// suffix. ok is false if rawURL doesn't parse.
func imageKey(rawURL string, base *url.URL) (key string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	u.Host = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	u.Path = sizeSuffix.ReplaceAllString(u.Path, "$1")
	u.RawPath = ""
	return u.String(), true
}

// httpsImage returns the image URL rawURL with https for http, since the
// CSP's img-src allows only https: images: a host that serves both loads, one
// that serves only http was blocked anyway. Any other URL is returned as is.
// rss.Article.CardImageURL does the same for card images.
func httpsImage(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "http" {
		return rawURL
	}
	u.Scheme = "https"
	return u.String()
}

// rewrite walks the children of n: it resolves href and src against base,
// replaces embedded posts with post cards, replaces iframes with post cards,
// video previews or links (or drops them), removes srcset and sizes, and
// makes images show the image their lazy-loading attributes or <noscript>
// name and load it over https, lazily and without a referrer.
func rewrite(n *html.Node, base *url.URL) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			resolveURLs(c, base)
			removeAttrs(c, "srcset", "sizes")
			switch c.DataAtom {
			case atom.Blockquote:
				// Rewritten first, so the post's links are resolved and its
				// images are lazy like any other.
				rewrite(c, base)
				if post, ok := xPost(c); ok {
					n.InsertBefore(socialCard(post), c)
					n.RemoveChild(c)
				} else if post, ok := blueskyPost(c); ok {
					n.InsertBefore(socialCard(post), c)
					n.RemoveChild(c)
				}
				c = next
				continue
			case atom.Iframe:
				if post, ok := xFramePost(c); ok {
					n.InsertBefore(socialCard(post), c)
				} else if post, ok := blueskyFramePost(c); ok {
					n.InsertBefore(socialCard(post), c)
				} else if video := videoEmbed(c); video != nil {
					n.InsertBefore(video, c)
				} else if link := embedLink(c); link != nil {
					n.InsertBefore(link, c)
				}
				n.RemoveChild(c)
				c = next
				continue
			case atom.Img:
				rewriteImage(c, base)
			case atom.Pre:
				labelCode(c)
			case atom.Noscript:
				if img := noscriptImage(c, base); img != nil {
					if prev := previousElement(c); prev != nil && prev.DataAtom == atom.Img && !hasSource(prev) {
						// The script would have swapped the placeholder
						// for the image; do it here.
						src, _ := attr(img, "src")
						replaceAttr(prev, "src", src)
						if _, ok := attr(prev, "alt"); !ok {
							if alt, ok := attr(img, "alt"); ok {
								setAttr(prev, "alt", alt)
							}
						}
					} else {
						n.InsertBefore(img, c)
					}
					n.RemoveChild(c)
				}
				c = next
				continue
			}
			rewrite(c, base)
		}
		c = next
	}
}

// rewriteImage makes img show the image its lazy-loading attributes name
// (see imageSource), over https, lazily and without a referrer. An img left
// without an http(s) source is removed later, by dropEmptyImages.
func rewriteImage(img *html.Node, base *url.URL) {
	if src, ok := imageSource(img, base); ok {
		replaceAttr(img, "src", httpsImage(src))
	}
	setAttr(img, "loading", "lazy")
	setAttr(img, "referrerpolicy", "no-referrer")
}

// lazyAttrs are the attributes lazy-loading scripts read an image's URL from,
// in the order imageSource tries them.
var lazyAttrs = []string{"data-src", "data-original", "data-lazy-src"}

// imageSource returns the URL img shows: its src when that is set and not
// a data: URL, else the first of data-src, data-original and
// data-lazy-src that is set, resolved against base (nil leaves it as is).
// ok is false when there is none.
func imageSource(img *html.Node, base *url.URL) (src string, ok bool) {
	src, _ = attr(img, "src")
	src = strings.TrimSpace(src)
	if src == "" || strings.HasPrefix(strings.ToLower(src), "data:") {
		src = ""
		for _, key := range lazyAttrs {
			if v, _ := attr(img, key); strings.TrimSpace(v) != "" {
				src = strings.TrimSpace(v)
				break
			}
		}
		if src == "" {
			return "", false
		}
	}
	if base != nil {
		if u, err := url.Parse(src); err == nil {
			src = base.ResolveReference(u).String()
		}
	}
	return src, true
}

// hasSource reports whether img has an http(s) src.
func hasSource(img *html.Node) bool {
	src, ok := attr(img, "src")
	if !ok {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(src))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}

// noscriptImage returns the <img> the <noscript> n holds, rewritten (see
// rewriteImage), when that is its only content and it has an http(s) src, or
// nil. The parser keeps a <noscript>'s content as text, so it is parsed here.
func noscriptImage(n *html.Node, base *url.URL) *html.Node {
	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.TextNode {
			return nil
		}
		text.WriteString(c.Data)
	}
	root, err := parse(text.String())
	if err != nil {
		return nil
	}
	bs := blocks(root)
	if len(bs) != 1 || bs[0].Type != html.ElementNode || bs[0].DataAtom != atom.Img {
		return nil
	}
	img := bs[0]
	resolveURLs(img, base)
	removeAttrs(img, "srcset", "sizes")
	rewriteImage(img, base)
	if !hasSource(img) {
		return nil
	}
	root.RemoveChild(img)
	return img
}

// previousElement returns the sibling element before n when only whitespace
// text is between them, or nil.
func previousElement(n *html.Node) *html.Node {
	for c := n.PrevSibling; c != nil; c = c.PrevSibling {
		switch {
		case c.Type == html.ElementNode:
			return c
		case c.Type == html.TextNode && strings.Trim(c.Data, htmlSpace) == "":
		default:
			return nil
		}
	}
	return nil
}

// dropEmptyImages removes each <img> under n without an http(s) src, and
// the <p>, <a> or <figure> whose only content (besides whitespace, comments
// and, in a <figure>, a <figcaption>) it was, as image-fallback.ts does for
// images that fail to load. It reports whether it removed anything.
func dropEmptyImages(n *html.Node) bool {
	removed := false
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			switch {
			case c.DataAtom == atom.Img:
				if !hasSource(c) {
					n.RemoveChild(c)
					removed = true
				}
			case dropEmptyImages(c):
				removed = true
				if (c.DataAtom == atom.P || c.DataAtom == atom.A || c.DataAtom == atom.Figure) && isEmptyFrame(c) {
					n.RemoveChild(c)
				}
			}
		}
		c = next
	}
	return removed
}

// isEmptyFrame reports whether n has no content besides whitespace, comments
// and, in a <figure>, a <figcaption>.
func isEmptyFrame(n *html.Node) bool {
	for _, c := range blocks(n) {
		if n.DataAtom == atom.Figure && c.DataAtom == atom.Figcaption {
			continue
		}
		return false
	}
	return true
}

// blockAtoms are the elements that give text its own layout. Text containing
// any of them is HTML whose paragraphs reflow leaves alone.
var blockAtoms = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Br: true, atom.Ul: true, atom.Ol: true,
	atom.Li: true, atom.Dl: true, atom.Blockquote: true, atom.Pre: true,
	atom.Table: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true,
	atom.H5: true, atom.H6: true, atom.Figure: true, atom.Section: true,
	atom.Article: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Hr: true,
}

// blankLine separates paragraphs in plain text. The parser has already turned
// \r\n into \n.
var blankLine = regexp.MustCompile(`\n[ \t]*\n`)

// reflow turns plain text into paragraphs, so text sent with line breaks
// doesn't render as one block. It applies when root contains no block
// element, or when its only element is a single <p> or <div> containing none
// (which it replaces). Its text is split into <p>s at blank lines, the
// remaining line breaks become <br>, inline elements stay in their paragraph,
// and paragraphs are trimmed, empty ones dropped. Text without a line break is
// left as is.
func reflow(root *html.Node) {
	container := root
	if hasBlock(root) {
		container = wrapper(root)
		if container == nil || hasBlock(container) {
			return
		}
	}
	if !hasNewline(container) {
		return
	}

	var paras []*html.Node
	p := newElement(atom.P)
	for c := container.FirstChild; c != nil; {
		next := c.NextSibling
		container.RemoveChild(c)
		if c.Type != html.TextNode {
			p.AppendChild(c)
			c = next
			continue
		}
		for i, part := range blankLine.Split(c.Data, -1) {
			if i > 0 {
				paras = append(paras, p)
				p = newElement(atom.P)
			}
			p.AppendChild(&html.Node{Type: html.TextNode, Data: part})
		}
		c = next
	}
	paras = append(paras, p)

	for c := root.FirstChild; c != nil; c = root.FirstChild {
		root.RemoveChild(c)
	}
	for _, p := range paras {
		trimParagraph(p)
		if isBlank(p) {
			continue
		}
		breakLines(p)
		root.AppendChild(p)
	}
}

// hasBlock reports whether n contains a block element.
func hasBlock(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (blockAtoms[c.DataAtom] || hasBlock(c)) {
			return true
		}
	}
	return false
}

// wrapper returns root's only child element if it is a <p> or <div> and
// root's other children are whitespace, or nil.
func wrapper(root *html.Node) *html.Node {
	var w *html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && strings.Trim(c.Data, htmlSpace) == "":
		case c.Type == html.ElementNode && w == nil && (c.DataAtom == atom.P || c.DataAtom == atom.Div):
			w = c
		default:
			return nil
		}
	}
	return w
}

// hasNewline reports whether a text child of n contains a line break.
func hasNewline(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.Contains(c.Data, "\n") {
			return true
		}
	}
	return false
}

// htmlSpace is the whitespace HTML collapses (not &nbsp;).
const htmlSpace = " \t\n\f\r"

// trimParagraph removes the leading and trailing whitespace of p's text,
// dropping text children that end up empty.
func trimParagraph(p *html.Node) {
	for c := p.FirstChild; c != nil && c.Type == html.TextNode; c = p.FirstChild {
		if c.Data = strings.TrimLeft(c.Data, htmlSpace); c.Data != "" {
			break
		}
		p.RemoveChild(c)
	}
	for c := p.LastChild; c != nil && c.Type == html.TextNode; c = p.LastChild {
		if c.Data = strings.TrimRight(c.Data, htmlSpace); c.Data != "" {
			break
		}
		p.RemoveChild(c)
	}
}

// isBlank reports whether p has no elements and no text but whitespace.
func isBlank(p *html.Node) bool {
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode || (c.Type == html.TextNode && strings.Trim(c.Data, htmlSpace) != "") {
			return false
		}
	}
	return true
}

// breakLines replaces each line break in p's text children with a <br>.
func breakLines(p *html.Node) {
	for c := p.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.TextNode && strings.Contains(c.Data, "\n") {
			for i, line := range strings.Split(c.Data, "\n") {
				if i > 0 {
					p.InsertBefore(newElement(atom.Br), c)
				}
				if line != "" {
					p.InsertBefore(&html.Node{Type: html.TextNode, Data: line}, c)
				}
			}
			p.RemoveChild(c)
		}
		c = next
	}
}

// bareURL matches an http(s) URL in text, up to the next whitespace, <, > or
// ". linkify trims the punctuation that usually ends a sentence.
var bareURL = regexp.MustCompile(`https?://[^\s<>"]+`)

// linkify walks the children of n and turns each bare http(s) URL in its text
// into <a href="URL">URL</a>, except in text inside <a>, <pre>, <code>,
// <script> or <style>. Trailing . , ; : ! ? ' and " are left out of the link,
// and so is a trailing ) unless the URL contains a matching (. The policy
// still checks the href.
func linkify(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.ElementNode:
			switch c.DataAtom {
			case atom.A, atom.Pre, atom.Code, atom.Script, atom.Style:
			default:
				linkify(c)
			}
		case html.TextNode:
			linkText(n, c)
		}
		c = next
	}
}

// linkText replaces the text node t, a child of parent, with text and links
// if it contains bare URLs.
func linkText(parent, t *html.Node) {
	matches := bareURL.FindAllStringIndex(t.Data, -1)
	if matches == nil {
		return
	}
	text := t.Data
	last := 0
	for _, m := range matches {
		start, end := m[0], m[0]+len(trimURL(text[m[0]:m[1]]))
		u, err := url.Parse(text[start:end])
		if err != nil || u.Host == "" {
			continue
		}
		if start > last {
			parent.InsertBefore(&html.Node{Type: html.TextNode, Data: text[last:start]}, t)
		}
		a := newElement(atom.A)
		a.Attr = []html.Attribute{{Key: "href", Val: text[start:end]}}
		a.AppendChild(&html.Node{Type: html.TextNode, Data: text[start:end]})
		parent.InsertBefore(a, t)
		last = end
	}
	if last == 0 {
		return
	}
	if last < len(text) {
		parent.InsertBefore(&html.Node{Type: html.TextNode, Data: text[last:]}, t)
	}
	parent.RemoveChild(t)
}

// trimURL removes the trailing punctuation that is more likely to end the
// sentence than the URL.
func trimURL(s string) string {
	for s != "" {
		switch last := s[len(s)-1]; {
		case strings.IndexByte(".,;:!?'\"", last) >= 0:
		case last == ')' && strings.Count(s, "(") < strings.Count(s, ")"):
		default:
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

func newElement(a atom.Atom) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: a.String(), DataAtom: a}
}

// resolveURLs resolves the href and src attributes of n against base. Values
// that aren't parseable URLs are left for the policy to remove, and
// fragment-only references (footnotes, "#top") are kept so they still point
// into the article. An empty src is left empty.
func resolveURLs(n *html.Node, base *url.URL) {
	if base == nil {
		return
	}
	for i, a := range n.Attr {
		if a.Namespace != "" || (a.Key != "href" && a.Key != "src") {
			continue
		}
		val := strings.TrimSpace(a.Val)
		if strings.HasPrefix(val, "#") || (a.Key == "src" && val == "") {
			// An empty src is no image, not the page itself (see
			// imageSource).
			continue
		}
		u, err := url.Parse(val)
		if err != nil {
			continue
		}
		n.Attr[i].Val = base.ResolveReference(u).String()
	}
}

var (
	youtubeHosts = []string{"youtube.com", "www.youtube.com", "m.youtube.com", "youtube-nocookie.com", "www.youtube-nocookie.com"}
	youtubePath  = regexp.MustCompile(`^/embed/([A-Za-z0-9_-]{11})$`)
	vimeoPath    = regexp.MustCompile(`^/video/([0-9]{1,12})$`)
)

// videoEmbed returns a click-to-play preview for a YouTube or Vimeo player
// iframe, or nil for any other iframe. The query string is ignored. For
// YouTube it is:
//
//	<figure class="embed" data-embed="youtube" data-embed-id="ID">
//	  <a href="https://www.youtube.com/watch?v=ID">
//	    <img src="https://i.ytimg.com/vi/ID/hqdefault.jpg" alt="" loading="lazy" referrerpolicy="no-referrer">
//	    <span>Play on YouTube</span>
//	  </a>
//	</figure>
//
// Vimeo's links to https://vimeo.com/ID, reads "Play on Vimeo" and has no
// <img> (its thumbnails need an API call). video-embeds.ts swaps the link for the
// player, built from data-embed-id.
func videoEmbed(iframe *html.Node) *html.Node {
	src, ok := attr(iframe, "src")
	if !ok {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	host := strings.ToLower(u.Hostname())

	var kind, id, href, label, thumb string
	switch {
	case slices.Contains(youtubeHosts, host):
		m := youtubePath.FindStringSubmatch(u.Path)
		if m == nil {
			return nil
		}
		kind, id, label = "youtube", m[1], "Play on YouTube"
		href = "https://www.youtube.com/watch?v=" + id
		thumb = "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
	case host == "player.vimeo.com":
		m := vimeoPath.FindStringSubmatch(u.Path)
		if m == nil {
			return nil
		}
		kind, id, label = "vimeo", m[1], "Play on Vimeo"
		href = "https://vimeo.com/" + id
	default:
		return nil
	}

	figure := newElement(atom.Figure)
	figure.Attr = []html.Attribute{
		{Key: "class", Val: "embed"},
		{Key: "data-embed", Val: kind},
		{Key: "data-embed-id", Val: id},
	}
	a := newElement(atom.A)
	a.Attr = []html.Attribute{{Key: "href", Val: href}}
	if thumb != "" {
		img := newElement(atom.Img)
		img.Attr = []html.Attribute{
			{Key: "src", Val: thumb},
			{Key: "alt", Val: ""},
			{Key: "loading", Val: "lazy"},
			{Key: "referrerpolicy", Val: "no-referrer"},
		}
		a.AppendChild(img)
	}
	span := newElement(atom.Span)
	span.AppendChild(&html.Node{Type: html.TextNode, Data: label})
	a.AppendChild(span)
	figure.AppendChild(a)
	return figure
}

// embedLink returns <p><a href="SRC">Open embedded content</a></p> for an
// iframe whose src is an http(s) URL, or nil.
func embedLink(iframe *html.Node) *html.Node {
	src, ok := attr(iframe, "src")
	if !ok {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
	a := &html.Node{
		Type:     html.ElementNode,
		Data:     "a",
		DataAtom: atom.A,
		Attr:     []html.Attribute{{Key: "href", Val: u.String()}},
	}
	a.AppendChild(&html.Node{Type: html.TextNode, Data: "Open embedded content"})
	p.AppendChild(a)
	return p
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *html.Node, key, val string) {
	removeAttrs(n, key)
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// replaceAttr sets n's key attribute to val where it is, or adds it.
func replaceAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttrs(n *html.Node, keys ...string) {
	attrs := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Namespace != "" || !slices.Contains(keys, a.Key) {
			attrs = append(attrs, a)
		}
	}
	n.Attr = attrs
}
