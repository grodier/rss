package discovery

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Candidate is a feed advertised by a page with <link rel="alternate">.
type Candidate struct {
	URL   *url.URL // absolute, http or https
	Title string   // the <link title="…">, trimmed; may be ""
	Type  string   // lowercase MIME type from the link, e.g. "application/rss+xml"
}

// PageInfo is what ParsePage extracts from an HTML page.
type PageInfo struct {
	Title       string      // og:site_name, else <title>, trimmed and whitespace-collapsed; may be ""
	Description string      // <meta name="description">, else og:description; trimmed
	Feeds       []Candidate // in document order, deduplicated by URL string
}

// CommonFeedPaths are tried, in order, against the site origin when a page
// advertises no feeds.
var CommonFeedPaths = []string{
	"/feed", "/rss", "/feed.xml", "/rss.xml", "/atom.xml", "/index.xml",
	"/feed.json", "/feeds/posts/default", "/?feed=rss2",
}

// feedTypes are the <link type> values (lowercased, without parameters) that
// identify a feed.
var feedTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/rdf+xml":   true,
	"application/feed+json": true,
}

// feedLink is a feed <link> whose href hasn't been resolved yet: the base URL
// may only be known once the whole document has been read.
type feedLink struct {
	href, title, typ string
}

// ParsePage extracts page info from an HTML document fetched from pageURL.
// Malformed or truncated HTML is not an error: parsing stops at the end of
// body and returns what it found up to there.
func ParsePage(pageURL *url.URL, body []byte) PageInfo {
	var (
		links                         []feedLink
		baseHref                      string
		haveBase                      bool
		siteName, title, desc, ogDesc string
		inTitle, titleDone            bool
	)

	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			// The end of the document. Reading from memory, z.Err() can
			// only be io.EOF.
			info := PageInfo{
				Title:       collapseSpace(siteName),
				Description: desc,
			}
			if info.Title == "" {
				info.Title = collapseSpace(title)
			}
			if info.Description == "" {
				info.Description = ogDesc
			}
			info.Feeds = resolveFeeds(pageURL, baseHref, haveBase, links)
			return info

		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}

		case html.EndTagToken:
			if name, _ := z.TagName(); atom.Lookup(name) == atom.Title && inTitle {
				inTitle, titleDone = false, true
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			a := atom.Lookup(name)
			if a == atom.Title {
				if !titleDone && tt == html.StartTagToken {
					inTitle = true
				}
				continue
			}
			if !hasAttr || (a != atom.Link && a != atom.Meta && a != atom.Base) {
				continue
			}
			attrs := readAttrs(z)
			switch a {
			case atom.Base:
				if href, ok := attrs["href"]; ok && !haveBase {
					baseHref, haveBase = href, true
				}
			case atom.Link:
				typ, _, _ := strings.Cut(attrs["type"], ";")
				typ = strings.ToLower(strings.TrimSpace(typ))
				if hasToken(attrs["rel"], "alternate") && feedTypes[typ] {
					links = append(links, feedLink{
						href:  attrs["href"],
						title: strings.TrimSpace(attrs["title"]),
						typ:   typ,
					})
				}
			case atom.Meta:
				content := strings.TrimSpace(attrs["content"])
				if content == "" {
					continue
				}
				switch {
				case strings.EqualFold(attrs["property"], "og:site_name") && siteName == "":
					siteName = content
				case strings.EqualFold(attrs["property"], "og:description") && ogDesc == "":
					ogDesc = content
				case strings.EqualFold(attrs["name"], "description") && desc == "":
					desc = content
				}
			}
		}
	}
}

// readAttrs returns the current tag's attributes; the first occurrence of a
// name wins. The tokenizer lowercases names and unescapes values.
func readAttrs(z *html.Tokenizer) map[string]string {
	attrs := make(map[string]string)
	for {
		key, val, more := z.TagAttr()
		if _, seen := attrs[string(key)]; !seen {
			attrs[string(key)] = string(val)
		}
		if !more {
			return attrs
		}
	}
}

// resolveFeeds resolves the links' hrefs against the document's base URL,
// dropping empty hrefs, non-http(s) results and duplicates.
func resolveFeeds(pageURL *url.URL, baseHref string, haveBase bool, links []feedLink) []Candidate {
	base := pageURL
	if haveBase {
		if u, err := url.Parse(strings.TrimSpace(baseHref)); err == nil {
			base = pageURL.ResolveReference(u)
		}
	}

	var feeds []Candidate
	seen := make(map[string]bool)
	for _, l := range links {
		href := strings.TrimSpace(l.href)
		if href == "" {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		u := base.ResolveReference(ref)
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		key := u.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		feeds = append(feeds, Candidate{URL: u, Title: l.title, Type: l.typ})
	}
	return feeds
}

// hasToken reports whether the space-separated list s contains tok, ignoring
// case.
func hasToken(s, tok string) bool {
	for _, f := range strings.Fields(s) {
		if strings.EqualFold(f, tok) {
			return true
		}
	}
	return false
}

// collapseSpace trims s and replaces each run of whitespace with one space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
