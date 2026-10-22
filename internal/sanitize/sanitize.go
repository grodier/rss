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
	return p
}

// HTML returns raw as HTML that is safe to render: relative URLs in href and
// src are resolved against base (left as is if base is nil), each <iframe>
// with an http(s) src is replaced by <p><a href="SRC">Open embedded
// content</a></p>, plain text is split into paragraphs (see reflow), bare
// http(s) URLs become links (see linkify), and everything bluemonday's policy
// doesn't allow is removed.
//
// It is the one place the app vouches for untrusted HTML: article HTML is
// never converted to template.HTML anywhere else.
func HTML(raw string, base *url.URL) template.HTML {
	return template.HTML(policy.Sanitize(prepare(raw, base)))
}

// prepare parses raw as a fragment of a <div>, rewrites it (see rewrite),
// reflows it (see reflow), links its bare URLs (see linkify) and renders it
// back. If raw can't be parsed or rendered it is returned as is;
// the policy still makes it safe, it just misses the rewrites.
func prepare(raw string, base *url.URL) string {
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw), root)
	if err != nil {
		return raw
	}
	for _, n := range nodes {
		// ParseFragment returns detached nodes; attach them so rewrite can
		// replace or remove top-level iframes too.
		root.AppendChild(n)
	}
	rewrite(root, base)
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

// rewrite walks the children of n: it resolves href and src against base,
// replaces iframes with links (or drops them), removes srcset and sizes, and
// makes images load lazily without a referrer.
func rewrite(n *html.Node, base *url.URL) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			resolveURLs(c, base)
			removeAttrs(c, "srcset", "sizes")
			switch c.DataAtom {
			case atom.Iframe:
				if link := embedLink(c); link != nil {
					n.InsertBefore(link, c)
				}
				n.RemoveChild(c)
				c = next
				continue
			case atom.Img:
				setAttr(c, "loading", "lazy")
				setAttr(c, "referrerpolicy", "no-referrer")
			}
			rewrite(c, base)
		}
		c = next
	}
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
// into the article.
func resolveURLs(n *html.Node, base *url.URL) {
	if base == nil {
		return
	}
	for i, a := range n.Attr {
		if a.Namespace != "" || (a.Key != "href" && a.Key != "src") {
			continue
		}
		val := strings.TrimSpace(a.Val)
		if strings.HasPrefix(val, "#") {
			continue
		}
		u, err := url.Parse(val)
		if err != nil {
			continue
		}
		n.Attr[i].Val = base.ResolveReference(u).String()
	}
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

func removeAttrs(n *html.Node, keys ...string) {
	attrs := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Namespace != "" || !slices.Contains(keys, a.Key) {
			attrs = append(attrs, a)
		}
	}
	n.Attr = attrs
}
