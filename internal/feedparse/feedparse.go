// Package feedparse recognizes RSS, Atom and JSON Feed documents and reads
// their metadata. Parsing is done by github.com/mmcdole/gofeed.
package feedparse

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
)

// ErrNotFeed is returned by Parse when the body is not a recognizable feed.
var ErrNotFeed = errors.New("feedparse: not a feed")

// Meta is the feed-level metadata of a parsed feed.
type Meta struct {
	Format      string // "rss", "atom", "rdf" or "json"
	Title       string // whitespace-trimmed and collapsed; may be ""
	Description string // RSS <description>, Atom <subtitle>, JSON "description"; trimmed
	SiteURL     string // absolute; RSS channel <link>, Atom <link rel="alternate"> (or a <link> without rel), JSON "home_page_url"; "" if missing
}

// feedContentTypes are the media types Sniff accepts without looking at the
// body.
var feedContentTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/rdf+xml":   true,
	"application/feed+json": true,
	"application/xml":       true,
	"text/xml":              true,
}

// jsonFeedVersionRX matches the version member of a JSON Feed.
var jsonFeedVersionRX = regexp.MustCompile(`"version"\s*:\s*"https?://jsonfeed\.org/version/`)

var utf8BOM = []byte("\xef\xbb\xbf")

// Sniff is a cheap check (no full parse) used before downloading/parsing
// many candidates: true if contentType is an RSS/Atom/JSON Feed/XML type,
// or the first non-whitespace bytes (after an optional UTF-8 BOM and
// <?xml …?> prolog / comments) start with <rss, <feed, <rdf:RDF, or the
// body is JSON containing "version":"https://jsonfeed.org/version/.
func Sniff(contentType string, body []byte) bool {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil && feedContentTypes[mt] {
		return true
	}
	if isJSONFeed(body) {
		return true
	}
	switch rootElement(body) {
	case "rss", "feed", "rdf:RDF":
		return true
	}
	return false
}

// Parse parses body as a feed. feedURL is the URL the feed was fetched from
// and is used to resolve a relative SiteURL. Returns ErrNotFeed (wrapped is
// fine) if body isn't a recognizable feed, including HTML pages.
func Parse(feedURL *url.URL, body []byte) (Meta, error) {
	var format string
	switch root := rootElement(body); {
	case root == "rss":
		format = "rss"
	case root == "rdf:RDF":
		format = "rdf"
	case root == "feed":
		format = "atom"
	case root == "" && isJSONFeed(body):
		format = "json"
	default:
		return Meta{}, ErrNotFeed
	}

	p := gofeed.NewParser()
	p.KeepOriginalFeed = true
	f, err := p.Parse(bytes.NewReader(body))
	if err != nil {
		return Meta{}, fmt.Errorf("%w: %w", ErrNotFeed, err)
	}

	link := f.Link
	if a, ok := f.OriginalFeed().(*atom.Feed); ok {
		link = atomSiteLink(a.Links)
	}

	return Meta{
		Format:      format,
		Title:       collapseSpace(f.Title),
		Description: collapseSpace(f.Description),
		SiteURL:     resolveHTTP(feedURL, link),
	}, nil
}

// atomSiteLink returns the href of the first rel="alternate" link, or else
// of the first link without a rel. rel="self" is the feed itself, not the
// site.
func atomSiteLink(links []*atom.Link) string {
	noRel := ""
	for _, l := range links {
		switch rel := strings.ToLower(strings.TrimSpace(l.Rel)); rel {
		case "alternate":
			return l.Href
		case "":
			if noRel == "" {
				noRel = l.Href
			}
		}
	}
	return noRel
}

// resolveHTTP resolves ref against base and returns it if the result is an
// absolute http(s) URL, else "".
func resolveHTTP(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// collapseSpace trims s and replaces internal runs of whitespace with a
// single space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// isJSONFeed reports whether body looks like a JSON object with a JSON Feed
// version.
func isJSONFeed(body []byte) bool {
	body = bytes.TrimSpace(bytes.TrimPrefix(body, utf8BOM))
	return len(body) > 0 && body[0] == '{' && jsonFeedVersionRX.Match(body)
}

// rootElement returns the name of the first element in an XML document
// (e.g. "rss", "rdf:RDF"), skipping a UTF-8 BOM, whitespace, processing
// instructions such as the <?xml …?> prolog, comments and a doctype. It
// returns "" if body doesn't start like an XML document.
func rootElement(body []byte) string {
	b := bytes.TrimPrefix(body, utf8BOM)
	for {
		b = bytes.TrimLeft(b, " \t\r\n")
		var end []byte
		switch {
		case bytes.HasPrefix(b, []byte("<?")):
			end = []byte("?>")
		case bytes.HasPrefix(b, []byte("<!--")):
			end = []byte("-->")
		case bytes.HasPrefix(b, []byte("<!")):
			end = []byte(">")
		case bytes.HasPrefix(b, []byte("<")):
			b = b[1:]
			i := bytes.IndexAny(b, " \t\r\n/>")
			if i < 0 {
				return ""
			}
			return string(b[:i])
		default:
			return ""
		}
		i := bytes.Index(b, end)
		if i < 0 {
			return ""
		}
		b = b[i+len(end):]
	}
}
