package discovery

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// PageTag is a <title>, <base>, <meta> or <link> tag of an HTML page.
type PageTag struct {
	Tag   string // "title", "base", "meta" or "link"
	Key   string // meta: its property, else name, else itemprop, else http-equiv, as written ("charset" for <meta charset>); link: its rel; "" for title and base
	Value string // meta: content; link: href resolved against the page; title: its text; base: href. At most 300 runes
	Extra string // link: its type and sizes ("image/png 192x192"), "" if neither; "" for other tags
	Used  bool   // ParsePage reads this key (see usedPageKeys); a later duplicate is still marked
}

// PageInspection is what InspectPage found in an HTML page.
type PageInspection struct {
	Info        PageInfo  // what ParsePage returns
	Tags        []PageTag // in document order
	JSONLD      []string  // each <script type="application/ld+json">'s text, indented if it parses as JSON; each at most 20 000 bytes
	JSONLDTypes []string  // the "@type" values at the top level of each block and in its "@graph", in order, deduplicated
}

const (
	// tagValueMax is how many runes of a PageTag's value InspectPage keeps.
	tagValueMax = 300
	// jsonLDMax is how many bytes of a JSON-LD block InspectPage keeps.
	jsonLDMax = 20000
)

// usedPageKeys are the tags ParsePage reads: "title", "base", "meta " plus a
// lowercase meta key, and "link " plus a lowercase rel token. "shortcut
// icon" is read through its "icon" token; "link alternate" only counts with
// a feed type from feedTypes. Keep it in step with ParsePage.
var usedPageKeys = map[string]bool{
	"title": true,
	"base":  true,

	"meta og:site_name":        true,
	"meta og:description":      true,
	"meta description":         true,
	"meta og:title":            true,
	"meta og:image":            true,
	"meta og:image:url":        true,
	"meta og:image:secure_url": true,
	"meta twitter:image":       true,
	"meta twitter:image:src":   true,

	"link alternate":                    true,
	"link icon":                         true,
	"link apple-touch-icon":             true,
	"link apple-touch-icon-precomposed": true,
}

// rawTag is a PageTag whose link href hasn't been resolved yet: the base URL
// may only be known once the whole document has been read.
type rawTag struct {
	PageTag
	href string // a link's href, as written
}

// InspectPage reads pageURL's HTML like ParsePage and also lists its
// metadata tags.
func InspectPage(pageURL *url.URL, body []byte) PageInspection {
	insp := PageInspection{Info: ParsePage(pageURL, body)}

	var (
		tags      []rawTag
		baseHref  string
		haveBase  bool
		title     *rawTag // the <title> being read
		jsonLD    *strings.Builder
		seenTypes = map[string]bool{}
	)

	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if title != nil {
				title.Value = collapseSpace(title.Value)
				tags = append(tags, *title)
			}
			if jsonLD != nil {
				insp.addJSONLD(jsonLD.String(), seenTypes)
			}
			base := baseURL(pageURL, baseHref, haveBase)
			for _, t := range tags {
				if t.Tag == "link" {
					t.Value = t.href
					if u := resolveHref(base, t.href); u != nil {
						t.Value = u.String()
					}
				}
				t.Value = cutRunes(t.Value, tagValueMax)
				insp.Tags = append(insp.Tags, t.PageTag)
			}
			return insp

		case html.TextToken:
			switch {
			case title != nil:
				title.Value += string(z.Text())
			case jsonLD != nil:
				jsonLD.Write(z.Text())
			}

		case html.EndTagToken:
			name, _ := z.TagName()
			switch a := atom.Lookup(name); {
			case a == atom.Title && title != nil:
				title.Value = collapseSpace(title.Value)
				tags = append(tags, *title)
				title = nil
			case a == atom.Script && jsonLD != nil:
				insp.addJSONLD(jsonLD.String(), seenTypes)
				jsonLD = nil
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			a := atom.Lookup(name)
			if a == atom.Title {
				if tt == html.StartTagToken {
					title = &rawTag{PageTag: PageTag{Tag: "title", Used: usedPageKeys["title"]}}
				}
				continue
			}
			if !hasAttr {
				continue
			}
			switch a {
			case atom.Script:
				attrs := readAttrs(z)
				typ, _, _ := strings.Cut(attrs["type"], ";")
				if tt == html.StartTagToken && strings.EqualFold(strings.TrimSpace(typ), "application/ld+json") {
					jsonLD = &strings.Builder{}
				}
			case atom.Base:
				attrs := readAttrs(z)
				href, ok := attrs["href"]
				if ok && !haveBase {
					baseHref, haveBase = href, true
				}
				tags = append(tags, rawTag{PageTag: PageTag{Tag: "base", Value: href, Used: usedPageKeys["base"]}})
			case atom.Meta:
				attrs := readAttrs(z)
				key := metaKey(attrs)
				value := attrs["content"]
				if key == "" {
					if cs, ok := attrs["charset"]; ok {
						key, value = "charset", cs
					}
				}
				tags = append(tags, rawTag{PageTag: PageTag{
					Tag:   "meta",
					Key:   key,
					Value: value,
					Used:  usedPageKeys["meta "+strings.ToLower(key)],
				}})
			case atom.Link:
				attrs := readAttrs(z)
				typ := strings.TrimSpace(attrs["type"])
				tags = append(tags, rawTag{
					PageTag: PageTag{
						Tag:   "link",
						Key:   attrs["rel"],
						Extra: strings.TrimSpace(typ + " " + strings.TrimSpace(attrs["sizes"])),
						Used:  linkUsed(attrs["rel"], typ),
					},
					href: attrs["href"],
				})
			}
		}
	}
}

// metaKey is a <meta>'s property, else name, else itemprop, else
// http-equiv, as written; "" if it has none of them.
func metaKey(attrs map[string]string) string {
	for _, k := range []string{"property", "name", "itemprop", "http-equiv"} {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

// linkUsed reports whether ParsePage reads a <link> with rel and type: one
// of its rel tokens is in usedPageKeys, an alternate only with a feed type.
func linkUsed(rel, typ string) bool {
	mt, _, _ := strings.Cut(typ, ";")
	for _, tok := range strings.Fields(strings.ToLower(rel)) {
		if !usedPageKeys["link "+tok] {
			continue
		}
		if tok != "alternate" || feedTypes[strings.ToLower(strings.TrimSpace(mt))] {
			return true
		}
	}
	return false
}

// addJSONLD adds a JSON-LD block's text, indented if it parses as JSON, and
// the @type values it declares that aren't in seen yet.
func (insp *PageInspection) addJSONLD(text string, seen map[string]bool) {
	text = strings.TrimSpace(text)
	var v any
	if err := json.Unmarshal([]byte(text), &v); err == nil {
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(text), "", "  ") == nil {
			text = buf.String()
		}
		for _, t := range jsonLDTypes(v) {
			if !seen[t] {
				seen[t] = true
				insp.JSONLDTypes = append(insp.JSONLDTypes, t)
			}
		}
	}
	insp.JSONLD = append(insp.JSONLD, cutBytes(text, jsonLDMax))
}

// jsonLDTypes returns the @type values of a JSON-LD block's top-level
// objects (the block, or each element of a top-level array) and of the
// objects in their @graph, in order.
func jsonLDTypes(v any) []string {
	var tops []any
	if arr, ok := v.([]any); ok {
		tops = arr
	} else {
		tops = []any{v}
	}
	var types []string
	for _, top := range tops {
		obj, ok := top.(map[string]any)
		if !ok {
			continue
		}
		types = append(types, typesOf(obj)...)
		graph, _ := obj["@graph"].([]any)
		for _, g := range graph {
			if o, ok := g.(map[string]any); ok {
				types = append(types, typesOf(o)...)
			}
		}
	}
	return types
}

// typesOf returns an object's @type: a string, or the strings of an array.
func typesOf(obj map[string]any) []string {
	switch t := obj["@type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// cutRunes returns s cut at max runes, with invalid UTF-8 replaced.
func cutRunes(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// cutBytes returns s cut at max bytes on a UTF-8 boundary, with invalid
// UTF-8 replaced.
func cutBytes(s string, max int) string {
	if len(s) > max {
		i := max
		for i > 0 && !utf8.RuneStart(s[i]) {
			i--
		}
		s = s[:i]
	}
	return strings.ToValidUTF8(s, "�")
}
