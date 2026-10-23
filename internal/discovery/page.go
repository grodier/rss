package discovery

import (
	"bytes"
	"net/url"
	"strconv"
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

// Icon is an icon a page declares with <link rel="icon">, "shortcut icon",
// "apple-touch-icon" or "apple-touch-icon-precomposed".
type Icon struct {
	URL   *url.URL // absolute, http or https
	Apple bool     // an apple-touch-icon (or -precomposed)
	Type  string   // lowercase MIME type from the link, e.g. "image/png"; "" if none
	Size  int      // the largest square size in sizes ("32x32 192x192" → 192); 0 if missing, "any" or unparseable
}

// PageInfo is what ParsePage extracts from an HTML page.
type PageInfo struct {
	Title       string      // og:site_name, else <title>, trimmed and whitespace-collapsed; may be ""
	PageTitle   string      // og:title, else <title>; trimmed and whitespace-collapsed; may be ""
	Description string      // <meta name="description">, else og:description; trimmed
	Image       string      // absolute http(s) URL of og:image (or og:image:url, og:image:secure_url), else twitter:image (or twitter:image:src); "" if none
	Feeds       []Candidate // in document order, deduplicated by URL string
	Icons       []Icon      // icon links in document order, deduplicated by URL string
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

// ogImageNames and twitterImageNames are the <meta> names whose content is
// a page's image, Open Graph first.
var (
	ogImageNames      = []string{"og:image", "og:image:url", "og:image:secure_url"}
	twitterImageNames = []string{"twitter:image", "twitter:image:src"}
)

// feedLink is a feed <link> whose href hasn't been resolved yet: the base URL
// may only be known once the whole document has been read.
type feedLink struct {
	href, title, typ string
}

// iconLink is an icon <link> whose href hasn't been resolved yet.
type iconLink struct {
	href  string
	apple bool
	typ   string
	size  int
}

// ParsePage extracts page info from an HTML document fetched from pageURL.
// Malformed or truncated HTML is not an error: parsing stops at the end of
// body and returns what it found up to there.
func ParsePage(pageURL *url.URL, body []byte) PageInfo {
	var (
		links                         []feedLink
		icons                         []iconLink
		baseHref                      string
		haveBase                      bool
		siteName, title, desc, ogDesc string
		ogTitle, ogImage, twImage     string
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
				PageTitle:   collapseSpace(ogTitle),
				Description: desc,
			}
			if info.Title == "" {
				info.Title = collapseSpace(title)
			}
			if info.PageTitle == "" {
				info.PageTitle = collapseSpace(title)
			}
			if info.Description == "" {
				info.Description = ogDesc
			}
			base := baseURL(pageURL, baseHref, haveBase)
			if u := resolveHref(base, ogImage); u != nil {
				info.Image = u.String()
			} else if u := resolveHref(base, twImage); u != nil {
				info.Image = u.String()
			}
			info.Feeds = resolveFeeds(base, links)
			info.Icons = resolveIcons(base, icons)
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
				rel := attrs["rel"]
				if hasToken(rel, "alternate") && feedTypes[typ] {
					links = append(links, feedLink{
						href:  attrs["href"],
						title: strings.TrimSpace(attrs["title"]),
						typ:   typ,
					})
				}
				apple := hasToken(rel, "apple-touch-icon") || hasToken(rel, "apple-touch-icon-precomposed")
				if apple || hasToken(rel, "icon") {
					icons = append(icons, iconLink{
						href:  attrs["href"],
						apple: apple,
						typ:   typ,
						size:  largestSize(attrs["sizes"]),
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
				case metaIs(attrs, "og:title") && ogTitle == "":
					ogTitle = content
				case metaIs(attrs, ogImageNames...) && ogImage == "":
					ogImage = content
				case metaIs(attrs, twitterImageNames...) && twImage == "":
					twImage = content
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

// metaIs reports whether a <meta>'s property or name is one of names,
// ignoring case.
func metaIs(attrs map[string]string, names ...string) bool {
	for _, n := range names {
		if strings.EqualFold(attrs["property"], n) || strings.EqualFold(attrs["name"], n) {
			return true
		}
	}
	return false
}

// largestSize returns the largest size in a <link sizes> list, taking the
// smaller side of a non-square size; 0 if there is none it can parse.
func largestSize(sizes string) int {
	largest := 0
	for _, f := range strings.Fields(sizes) {
		w, h, ok := strings.Cut(strings.ToLower(f), "x")
		if !ok {
			continue
		}
		wi, err1 := strconv.Atoi(w)
		hi, err2 := strconv.Atoi(h)
		if err1 != nil || err2 != nil {
			continue
		}
		if s := min(wi, hi); s > largest {
			largest = s
		}
	}
	return largest
}

// baseURL returns the document's base URL: the <base href> resolved against
// pageURL if the document has one, else pageURL.
func baseURL(pageURL *url.URL, baseHref string, haveBase bool) *url.URL {
	if haveBase {
		if u, err := url.Parse(strings.TrimSpace(baseHref)); err == nil {
			return pageURL.ResolveReference(u)
		}
	}
	return pageURL
}

// resolveHref resolves href against base. It returns nil for an empty or
// unparseable href and for a result that isn't an absolute http(s) URL.
func resolveHref(base *url.URL, href string) *url.URL {
	href = strings.TrimSpace(href)
	if href == "" {
		return nil
	}
	ref, err := url.Parse(href)
	if err != nil {
		return nil
	}
	u := base.ResolveReference(ref)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return u
}

// resolveFeeds resolves the links' hrefs against the document's base URL,
// dropping empty hrefs, non-http(s) results and duplicates.
func resolveFeeds(base *url.URL, links []feedLink) []Candidate {
	var feeds []Candidate
	seen := make(map[string]bool)
	for _, l := range links {
		u := resolveHref(base, l.href)
		if u == nil {
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

// resolveIcons resolves the icon links' hrefs against the document's base
// URL, dropping empty hrefs, non-http(s) results and duplicates.
func resolveIcons(base *url.URL, links []iconLink) []Icon {
	var icons []Icon
	seen := make(map[string]bool)
	for _, l := range links {
		u := resolveHref(base, l.href)
		if u == nil {
			continue
		}
		key := u.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		icons = append(icons, Icon{URL: u, Apple: l.apple, Type: l.typ, Size: l.size})
	}
	return icons
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
