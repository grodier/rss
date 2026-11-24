package sanitize

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/grodier/rss/internal/textcut"
)

const (
	// headingTextMax caps a Heading's text, in bytes.
	headingTextMax = 100
	// slugMax caps the slug of an id outline makes, in bytes, before the
	// "body-" prefix and any "-2" that makes it unique.
	slugMax = 60
)

// outline gives each <h2> and <h3> under root an id when it has none and
// returns them in document order, for the article's contents list. It runs
// after localAnchors, so every id already has its "body-" prefix and a
// heading that had one keeps it.
//
// An id it makes is "body-" and the heading's slug (see slug), with "-2",
// "-3"… when that id is taken, or "body-section-N" when the slug is empty,
// N being the heading's place in the outline. Headings inside a
// <blockquote> or a post card (see socialCard) are quoted, not the
// article's, headings the policy removes with what holds them (see
// droppedWithContent) aren't shown, and headings with no text have nothing
// to list: all are skipped and get no id.
func outline(root *html.Node) []Heading {
	taken := make(map[string]bool)
	collectIDs(root, taken)
	var headings []Heading
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || isQuoted(c) || droppedWithContent[c.Data] {
				continue
			}
			level := 0
			switch c.DataAtom {
			case atom.H2:
				level = 2
			case atom.H3:
				level = 3
			default:
				walk(c)
				continue
			}
			text := collapseSpace(textContent(c))
			if text == "" {
				continue
			}
			id, ok := attr(c, "id")
			if !ok {
				id = uniqueID(slug(text), len(headings)+1, taken)
				setAttr(c, "id", id)
			}
			headings = append(headings, Heading{
				ID:    id,
				Text:  headingText(text),
				Level: level,
			})
		}
	}
	walk(root)
	return headings
}

// headingText returns text, a heading's collapsed text, cut to at most
// headingTextMax bytes (textcut.Bytes), ending in "…" when it was cut.
func headingText(text string) string {
	if len(text) <= headingTextMax {
		return textcut.Bytes(text, headingTextMax)
	}
	return strings.TrimRight(textcut.Bytes(text, headingTextMax-len("…")), " ") + "…"
}

// isQuoted reports whether n holds someone else's words: a <blockquote> or
// a post card.
func isQuoted(n *html.Node) bool {
	if n.DataAtom == atom.Blockquote {
		return true
	}
	class, _ := attr(n, "class")
	return n.DataAtom == atom.Figure && class == "social-post"
}

// droppedWithContent are the elements bluemonday's policy removes with
// everything in them (bluemonday's addDefaultSkipElementContent), so a
// heading inside one never reaches the page.
var droppedWithContent = map[string]bool{
	"frame": true, "frameset": true, "iframe": true, "noembed": true,
	"noframes": true, "noscript": true, "nostyle": true, "object": true,
	"script": true, "style": true, "title": true,
}

// collectIDs adds the id of every element under n to ids.
func collectIDs(n *html.Node, ids map[string]bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if id, ok := attr(c, "id"); ok {
			ids[id] = true
		}
		collectIDs(c, ids)
	}
}

// uniqueID returns "body-" and s, with "-2", "-3"… when that id is in taken,
// or "body-section-N" (N being place) when s is empty, and adds it to taken.
func uniqueID(s string, place int, taken map[string]bool) string {
	if s == "" {
		s = "section-" + strconv.Itoa(place)
	}
	id := idPrefix + s
	for i := 2; taken[id]; i++ {
		id = idPrefix + s + "-" + strconv.Itoa(i)
	}
	taken[id] = true
	return id
}

// slug returns text as lowercase ASCII letters and digits, each run of
// anything else as one "-", without a leading or trailing "-", cut to at
// most slugMax bytes.
func slug(text string) string {
	var b strings.Builder
	dash := false
	for _, r := range text {
		switch {
		case 'a' <= r && r <= 'z', '0' <= r && r <= '9':
		case 'A' <= r && r <= 'Z':
			r += 'a' - 'A'
		default:
			dash = b.Len() > 0
			continue
		}
		if dash {
			b.WriteByte('-')
			dash = false
		}
		b.WriteRune(r)
	}
	s := b.String()
	if len(s) > slugMax {
		s = strings.TrimRight(s[:slugMax], "-")
	}
	return s
}
