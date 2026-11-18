package sanitize

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// socialPost is a post from a social network embedded in an article, read
// from the markup its embed code leaves (see xPost). Nothing in it is
// fetched: a network's script would fill in the rest, and we never run it.
type socialPost struct {
	Network string     // "x"; data-social, and the network's name in labels
	URL     string     // the post's address, built from validated parts
	Name    string     // the author's display name as published; may be empty
	Handle  string     // without "@"; may be empty
	Text    *html.Node // the embed's first <p>, with its children; nil for a link-only embed
	Date    string     // the date as published (the status link's text); may be empty
	Media   bool       // the post had a picture or video the embed's markup left out
}

// networkNames are the networks' names in a card's labels.
var networkNames = map[string]string{"x": "X"}

var (
	xHosts      = []string{"twitter.com", "www.twitter.com", "mobile.twitter.com", "x.com", "www.x.com"}
	xStatusPath = regexp.MustCompile(`^/([A-Za-z0-9_]{1,15})/status(?:es)?/([0-9]{1,20})/?$`)
	xWebPath    = regexp.MustCompile(`^/i/web/status/([0-9]{1,20})/?$`)
	xFrameID    = regexp.MustCompile(`^[0-9]{1,20}$`)
	xByline     = regexp.MustCompile(`^\s*[—–-]\s*(.*?)\s*\(@([A-Za-z0-9_]{1,15})\)\s*$`)
)

// xPost reads a post from an X embed: a <blockquote> whose class has the
// token twitter-tweet or twitter-video, as X's publish tool writes it:
//
//	<blockquote class="twitter-tweet"><p lang="en" dir="ltr">TEXT</p>&mdash;
//	NAME (@HANDLE) <a href="https://twitter.com/HANDLE/status/ID">DATE</a></blockquote>
//
// or with only the status link, for the script to fill in. ok is false if n
// isn't one or has no valid status link. It removes the post's picture links
// (pic.twitter.com/…) from its text, setting Media.
func xPost(n *html.Node) (post socialPost, ok bool) {
	if n.Type != html.ElementNode || n.DataAtom != atom.Blockquote ||
		!(hasClass(n, "twitter-tweet") || hasClass(n, "twitter-video")) {
		return socialPost{}, false
	}
	link, handle, id := xStatusLink(n)
	if link == nil {
		return socialPost{}, false
	}
	post = socialPost{Network: "x", Handle: handle, URL: "https://x.com/i/web/status/" + id}
	if handle != "" {
		post.URL = "https://x.com/" + handle + "/status/" + id
	}

	var p *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.P {
			p = c
			break
		}
	}
	if p != nil {
		post.Media = removeLinks(p, func(text string) bool {
			return strings.HasPrefix(text, "pic.twitter.com/") || strings.HasPrefix(text, "pic.x.com/")
		})
		if !isBlank(p) {
			post.Text = p
		}
	}

	// The byline and date are the blockquote's own children after the text:
	// "— NAME (@HANDLE) <a>DATE</a>". A link-only embed has neither.
	if p != nil && link.Parent == n && follows(link, p) {
		var byline strings.Builder
		for c := p.NextSibling; c != nil && c != link; c = c.NextSibling {
			if c.Type == html.TextNode {
				byline.WriteString(c.Data)
			}
		}
		if m := xByline.FindStringSubmatch(byline.String()); m != nil {
			post.Name, post.Handle = m[1], m[2]
		}
		if date := strings.TrimSpace(textOf(link)); !strings.HasPrefix(date, "http") && !strings.HasPrefix(date, "pic.") {
			post.Date = date
		}
	}
	return post, true
}

// xStatusLink returns the last <a> under n that links to a post on X, with
// the post's author's handle (empty for /i/web/status/ID) and ID, or nil.
func xStatusLink(n *html.Node) (link *html.Node, handle, id string) {
	links := elements(n, atom.A)
	for i := len(links) - 1; i >= 0; i-- {
		href, _ := attr(links[i], "href")
		u, err := url.Parse(strings.TrimSpace(href))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !slices.Contains(xHosts, strings.ToLower(u.Hostname())) {
			continue
		}
		if m := xStatusPath.FindStringSubmatch(u.Path); m != nil {
			return links[i], m[1], m[2]
		}
		if m := xWebPath.FindStringSubmatch(u.Path); m != nil {
			return links[i], "", m[1]
		}
	}
	return nil, "", ""
}

// xFramePost reads a post from an X embed iframe
// (https://platform.twitter.com/embed/Tweet.html?id=ID); it has only a URL.
func xFramePost(iframe *html.Node) (post socialPost, ok bool) {
	src, _ := attr(iframe, "src")
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		strings.ToLower(u.Hostname()) != "platform.twitter.com" || u.Path != "/embed/Tweet.html" {
		return socialPost{}, false
	}
	id := u.Query().Get("id")
	if !xFrameID.MatchString(id) {
		return socialPost{}, false
	}
	return socialPost{Network: "x", URL: "https://x.com/i/web/status/" + id}, true
}

// socialCard returns the markup for post:
//
//	<figure class="social-post" data-social="x">
//	  <blockquote cite="URL">
//	    <p lang="en" dir="ltr">TEXT</p>
//	    <p class="social-media"><a href="URL">View media on X</a></p>
//	  </blockquote>
//	  <figcaption>
//	    <span class="social-name">NAME</span> <span class="social-handle">@HANDLE</span>
//	    <span class="social-network">on X</span> · <a href="URL">DATE</a>
//	  </figcaption>
//	</figure>
//
// The media line is there only when Media is set; a missing name, handle or
// date is left out, the link then reading "View on X". A post with neither
// text nor media (a link-only embed) has no blockquote, and its caption is
// the handle and "View post on X".
func socialCard(post socialPost) *html.Node {
	network := networkNames[post.Network]
	figure := newElement(atom.Figure)
	figure.Attr = []html.Attribute{
		{Key: "class", Val: "social-post"},
		{Key: "data-social", Val: post.Network},
	}

	quote := post.Text != nil || post.Media
	if quote {
		bq := newElement(atom.Blockquote)
		bq.Attr = []html.Attribute{{Key: "cite", Val: post.URL}}
		if t := post.Text; t != nil {
			if t.Parent != nil {
				t.Parent.RemoveChild(t)
			}
			bq.AppendChild(t)
		}
		if post.Media {
			p := newElement(atom.P)
			p.Attr = []html.Attribute{{Key: "class", Val: "social-media"}}
			p.AppendChild(link(post.URL, "View media on "+network))
			bq.AppendChild(p)
		}
		figure.AppendChild(bq)
	}

	caption := newElement(atom.Figcaption)
	if quote && post.Name != "" {
		caption.AppendChild(span("social-name", post.Name))
		caption.AppendChild(text(" "))
	}
	if post.Handle != "" {
		caption.AppendChild(span("social-handle", "@"+post.Handle))
		caption.AppendChild(text(" "))
	}
	switch {
	case !quote:
		if post.Handle != "" {
			caption.AppendChild(text("· "))
		}
		caption.AppendChild(link(post.URL, "View post on "+network))
	case post.Date != "":
		caption.AppendChild(span("social-network", "on "+network))
		caption.AppendChild(text(" · "))
		caption.AppendChild(link(post.URL, post.Date))
	default:
		caption.AppendChild(span("social-network", "on "+network))
		caption.AppendChild(text(" · "))
		caption.AppendChild(link(post.URL, "View on "+network))
	}
	figure.AppendChild(caption)
	return figure
}

// removeLinks removes the <a> elements under n whose trimmed text matches,
// with the whitespace right before each, and reports whether it removed any.
func removeLinks(n *html.Node, match func(text string) bool) bool {
	removed := false
	for _, a := range elements(n, atom.A) {
		if !match(strings.TrimSpace(textOf(a))) {
			continue
		}
		if prev := a.PrevSibling; prev != nil && prev.Type == html.TextNode {
			prev.Data = strings.TrimRight(prev.Data, htmlSpace)
		}
		a.Parent.RemoveChild(a)
		removed = true
	}
	return removed
}

// elements returns the elements under n with atom a, in document order.
func elements(n *html.Node, a atom.Atom) []*html.Node {
	var found []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.DataAtom == a {
			found = append(found, c)
		}
		found = append(found, elements(c, a)...)
	}
	return found
}

// textOf returns the text under n.
func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return b.String()
}

// follows reports whether the sibling a comes after b.
func follows(a, b *html.Node) bool {
	for c := b.NextSibling; c != nil; c = c.NextSibling {
		if c == a {
			return true
		}
	}
	return false
}

// hasClass reports whether one of n's classes is class.
func hasClass(n *html.Node, class string) bool {
	v, _ := attr(n, "class")
	return slices.Contains(strings.Fields(v), class)
}

func text(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}

func span(class, s string) *html.Node {
	sp := newElement(atom.Span)
	sp.Attr = []html.Attribute{{Key: "class", Val: class}}
	sp.AppendChild(text(s))
	return sp
}

func link(href, s string) *html.Node {
	a := newElement(atom.A)
	a.Attr = []html.Attribute{{Key: "href", Val: href}}
	a.AppendChild(text(s))
	return a
}
