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
// content</a></p>, and everything bluemonday's policy doesn't allow is
// removed.
//
// It is the one place the app vouches for untrusted HTML: article HTML is
// never converted to template.HTML anywhere else.
func HTML(raw string, base *url.URL) template.HTML {
	return template.HTML(policy.Sanitize(prepare(raw, base)))
}

// prepare parses raw as a fragment of a <div>, rewrites it (see rewrite) and
// renders it back. If raw can't be parsed or rendered it is returned as is;
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
