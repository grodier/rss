package sanitize

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	Time    time.Time  // when it was posted, from its ID; zero if the ID doesn't say
	Date    string     // the date as published (the status link's text), for when Time is zero; may be empty
	Media   bool       // the post had a picture, video or other attachment the embed's markup left out
	Video   bool       // the embed says the post is a video
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
	post = xPostFromID(handle, id)
	post.Video = hasClass(n, "twitter-video")

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

// xPostFromID returns the post on X with the author's handle (empty if
// unknown) and ID: its URL and, from the ID, its time.
func xPostFromID(handle, id string) socialPost {
	post := socialPost{Network: "x", Handle: handle, URL: "https://x.com/i/web/status/" + id, Time: xTime(id)}
	if handle != "" {
		post.URL = "https://x.com/" + handle + "/status/" + id
	}
	return post
}

// xEpoch is the moment X's post IDs count from, in milliseconds since the
// Unix epoch, and xFirstSnowflake the first ID that does: older IDs were a
// plain sequence.
const (
	xEpoch          = 1288834974657
	xFirstSnowflake = 29700859248
)

// xTime returns when the post with ID id was made. Since November 2010 an
// ID's top bits are milliseconds since xEpoch (X's "snowflake" IDs); it
// returns the zero time for an older ID, one that doesn't parse, or one
// that would be more than a day from now.
func xTime(id string) time.Time {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n < xFirstSnowflake {
		return time.Time{}
	}
	t := time.UnixMilli(int64(n>>22) + xEpoch).UTC()
	if t.After(time.Now().Add(24 * time.Hour)) {
		return time.Time{}
	}
	return t
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
// (https://platform.twitter.com/embed/Tweet.html?id=ID); it has only a URL
// and a time.
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
	return xPostFromID("", id), true
}

// socialCard returns the markup for post, a framed card in the shape of the
// network's own embed ("Posts from other networks" in docs/decisions.md):
//
//	<figure class="social-post" data-social="x">
//	  <figcaption>
//	    <span class="social-name">NAME</span>
//	    <span class="social-handle">@HANDLE</span>
//	    <span class="social-network">on X</span>
//	  </figcaption>
//	  <blockquote cite="URL"><p lang="en" dir="ltr">TEXT</p></blockquote>
//	  <p class="social-foot"><time data-local datetime="T">T in UTC</time> <a href="URL">Read on X ↗</a></p>
//	</figure>
//
// A missing name or handle is left out. Without text, the blockquote is
// <p class="social-missing">This post's text isn't in the article.</p>.
// Without a time, the foot shows the date as published, if any. The link
// says what the card leaves out: "Watch on X" for a video, "See the full
// post on X" for a post with other media.
func socialCard(post socialPost) *html.Node {
	network := networkNames[post.Network]
	figure := newElement(atom.Figure)
	figure.Attr = []html.Attribute{
		{Key: "class", Val: "social-post"},
		{Key: "data-social", Val: post.Network},
	}

	caption := newElement(atom.Figcaption)
	if post.Name != "" {
		caption.AppendChild(span("social-name", post.Name))
		caption.AppendChild(text(" "))
	}
	if post.Handle != "" {
		caption.AppendChild(span("social-handle", "@"+post.Handle))
		caption.AppendChild(text(" "))
	}
	caption.AppendChild(span("social-network", "on "+network))
	figure.AppendChild(caption)

	if t := post.Text; t != nil {
		bq := newElement(atom.Blockquote)
		bq.Attr = []html.Attribute{{Key: "cite", Val: post.URL}}
		if t.Parent != nil {
			t.Parent.RemoveChild(t)
		}
		bq.AppendChild(t)
		figure.AppendChild(bq)
	} else {
		p := newElement(atom.P)
		p.Attr = []html.Attribute{{Key: "class", Val: "social-missing"}}
		p.AppendChild(text("This post's text isn't in the article."))
		figure.AppendChild(p)
	}

	foot := newElement(atom.P)
	foot.Attr = []html.Attribute{{Key: "class", Val: "social-foot"}}
	switch {
	case !post.Time.IsZero():
		// The local-time partial's markup, which local-times.ts shows in
		// the reader's time zone.
		tm := newElement(atom.Time)
		tm.Attr = []html.Attribute{
			{Key: "data-local", Val: ""},
			{Key: "datetime", Val: post.Time.Format(time.RFC3339)},
		}
		tm.AppendChild(text(post.Time.Format("2 Jan 2006 15:04 UTC")))
		foot.AppendChild(tm)
		foot.AppendChild(text(" "))
	case post.Date != "":
		foot.AppendChild(span("", post.Date))
		foot.AppendChild(text(" "))
	}
	label := "Read on " + network + " ↗"
	switch {
	case post.Video:
		label = "Watch on " + network + " ↗"
	case post.Media:
		label = "See the full post on " + network + " ↗"
	}
	foot.AppendChild(link(post.URL, label))
	figure.AppendChild(foot)
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

// span returns <span class="CLASS">S</span>, without the class if it's
// empty.
func span(class, s string) *html.Node {
	sp := newElement(atom.Span)
	if class != "" {
		sp.Attr = []html.Attribute{{Key: "class", Val: class}}
	}
	sp.AppendChild(text(s))
	return sp
}

func link(href, s string) *html.Node {
	a := newElement(atom.A)
	a.Attr = []html.Attribute{{Key: "href", Val: href}}
	a.AppendChild(text(s))
	return a
}
