package feedparse

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Field is an element, attribute or JSON key found in a feed document.
type Field struct {
	// Path names it: XML elements by their conventional namespace prefix
	// ("dc:creator"; "{uri}name" for an unknown namespace), children joined
	// with "/" ("media:group/media:thumbnail"), attributes after "@"
	// ("enclosure@type"); JSON keys joined with ".", arrays as "[]"
	// ("attachments[].mime_type").
	Path   string
	Count  int    // items that have it at least once; 1 for a channel field
	Used   bool   // Parse reads it (see usedChannelFields and usedItemFields)
	Sample string // its first non-empty value, whitespace-collapsed, at most 120 runes
}

// InspectedItem is one item of a feed document, seen three ways.
type InspectedItem struct {
	Raw    string // the item's source: its XML element as written, or its JSON object indented; "" if it couldn't be matched (see Inspect)
	Gofeed string // the item as gofeed parsed it (*gofeed.Item), as indented JSON
	Item   Item   // what Parse returns for it
}

// Inspection is what Inspect found in a feed document.
type Inspection struct {
	Feed       Feed            // what Parse returns
	Channel    []Field         // the channel's (or Atom feed's, or JSON feed's) own fields, items excluded, in document order
	ItemFields []Field         // every field found in any item, by Count descending, then Path
	Items      []InspectedItem // in document order, aligned with Feed.Items
}

// maxFieldDepth is how many levels below the channel or an item Inspect
// lists fields.
const maxFieldDepth = 3

// sampleLen is the most runes a Field's Sample has.
const sampleLen = 120

// namespacePrefixes are the conventional prefixes of the namespaces feeds
// commonly use, by namespace URI. Field paths use them whatever prefix the
// document declares.
var namespacePrefixes = map[string]string{
	"http://purl.org/rss/1.0/modules/content/":        "content",
	"http://purl.org/dc/elements/1.1/":                "dc",
	"http://purl.org/dc/terms/":                       "dcterms",
	"http://search.yahoo.com/mrss/":                   "media",
	"http://search.yahoo.com/mrss":                    "media",
	"http://www.itunes.com/dtds/podcast-1.0.dtd":      "itunes",
	"http://www.w3.org/2005/Atom":                     "atom",
	"http://purl.org/rss/1.0/modules/slash/":          "slash",
	"http://wellformedweb.org/CommentAPI/":            "wfw",
	"http://purl.org/rss/1.0/modules/syndication/":    "sy",
	"http://www.georss.org/georss":                    "georss",
	"https://podcastindex.org/namespace/1.0":          "podcast",
	"http://www.google.com/schemas/play-podcasts/1.0": "googleplay",
	"http://rssnamespace.org/feedburner/ext/1.0":      "feedburner",
	"http://www.youtube.com/xml/schemas/2015":         "yt",
	"http://purl.org/syndication/thread/1.0":          "thr",
	"http://www.w3.org/1999/02/22-rdf-syntax-ns#":     "rdf",
	"http://www.w3.org/XML/1998/namespace":            "xml",
}

// mediaItemFields are the Media RSS paths Parse reads in an item:
// media:thumbnail and those in a media:group for its image (itemImage),
// media:content and those in a media:group for its media type
// (itemMediaType).
var mediaItemFields = []string{
	"media:thumbnail@url", "media:group/media:thumbnail@url",
	"media:content@type", "media:content@medium",
	"media:group/media:content@type", "media:group/media:content@medium",
}

// rssItemFields are the paths Parse reads in an RSS 2.0 or RSS 1.0 item,
// directly or through gofeed's DefaultRSSTranslator, including the
// elements the translator falls back to (title: dc:title; description:
// dc:description, itunes:summary, atom:summary; content: atom:content;
// published: dc:date, atom:published; updated: dc:date, atom:updated;
// authors: see itemAuthors).
var rssItemFields = append([]string{
	"guid", "link",
	"title", "dc:title",
	"description", "dc:description", "itunes:summary", "atom:summary",
	"content:encoded", "atom:content",
	"pubDate", "dc:date", "atom:published", "atom:updated",
	"dc:creator", "author", "dc:author", "itunes:author", "atom:author/atom:name",
	"enclosure@url", "enclosure@type",
	"media:content@url", // a media:content image is gofeed's item image
	"itunes:image@href",
}, mediaItemFields...)

// rssChannelFields are the paths Parse reads in an RSS channel, including
// gofeed's fallbacks (title: dc:title; description: itunes:summary; link:
// an atom:link without rel or with rel="alternate").
var rssChannelFields = []string{
	"title", "dc:title",
	"description", "itunes:summary",
	"link", "atom:link@href", "atom:link@rel",
}

// usedChannelFields and usedItemFields are the paths Parse reads, by
// Meta.Format, then path: in the channel (or Atom or JSON feed) and in an
// item. A path not listed is also used when it holds one that is (the
// element "enclosure" holds "enclosure@url"). They are kept by hand: when
// Parse, or what it uses from gofeed, starts or stops reading a field,
// update them in the same change.
var (
	usedChannelFields = map[string]map[string]bool{
		"rss": pathSet(rssChannelFields...),
		"rdf": pathSet(rssChannelFields...),
		"atom": pathSet(
			"title", "title@type",
			"subtitle", "subtitle@type",
			"link@href", "link@rel",
		),
		"json": pathSet(
			"version", // recognizes the document as a JSON Feed
			"title", "description", "home_page_url",
		),
	}
	usedItemFields = map[string]map[string]bool{
		"rss": pathSet(rssItemFields...),
		"rdf": pathSet(rssItemFields...),
		"atom": pathSet(append([]string{
			"id",
			// rel="enclosure" links are enclosures: their href and type
			// are an image or media type.
			"link@href", "link@rel", "link@type",
			"title", "title@type",
			// gofeed decodes a summary or content by its type.
			"summary", "summary@type",
			"content", "content@type",
			"published", "updated",
			"author/name",
		}, mediaItemFields...)...),
		"json": pathSet(
			"id", "url", "external_url", "title", "summary",
			"content_html", "content_text",
			"date_published", "date_modified",
			"authors[].name", "author.name", // JSON Feed 1.0 has one author
			"image", "banner_image",
			"attachments[].url", "attachments[].mime_type",
		),
	}
)

func pathSet(paths ...string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// isUsed reports whether used has path or a path below it.
func isUsed(used map[string]bool, path string) bool {
	if used[path] {
		return true
	}
	for p := range used {
		if strings.HasPrefix(p, path+"/") || strings.HasPrefix(p, path+"@") ||
			strings.HasPrefix(p, path+".") || strings.HasPrefix(p, path+"[]") {
			return true
		}
	}
	return false
}

// Inspect parses body like Parse and also lists what the document contains.
// It returns Parse's errors. When the document's items can't be matched to
// gofeed's (a malformed document), every Raw is "".
func Inspect(feedURL *url.URL, body []byte) (Inspection, error) {
	feed, gf, indexes, err := parse(feedURL, body)
	if err != nil {
		return Inspection{}, err
	}

	var channel, items *fieldSet
	var raws []string
	if feed.Format == "json" {
		channel, items, raws = inspectJSON(body)
	} else {
		channel, items, raws = inspectXML(body)
	}
	if len(raws) != len(gf.Items) {
		raws = nil
	}

	insp := Inspection{
		Feed:       feed,
		Channel:    channel.list(usedChannelFields[feed.Format]),
		ItemFields: items.list(usedItemFields[feed.Format]),
		Items:      make([]InspectedItem, len(indexes)),
	}
	slices.SortStableFunc(insp.ItemFields, func(a, b Field) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Path, b.Path)
	})
	for i, idx := range indexes {
		it := InspectedItem{Gofeed: indentedJSON(gf.Items[idx]), Item: feed.Items[i]}
		if raws != nil {
			it.Raw = raws[idx]
		}
		insp.Items[i] = it
	}
	return insp, nil
}

// indentedJSON returns v as indented JSON, or the marshaling error.
func indentedJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "error: " + err.Error()
	}
	return string(b)
}

// fieldSet collects the fields found in the channel or in items, in the
// order they first appear.
type fieldSet struct {
	fields []*Field
	byPath map[string]*Field
	// inItem are the paths seen in the current item; nil for the channel.
	inItem map[string]bool
}

func newFieldSet(items bool) *fieldSet {
	s := &fieldSet{byPath: map[string]*Field{}}
	if items {
		s.inItem = map[string]bool{}
	}
	return s
}

// startItem starts counting the fields of a new item.
func (s *fieldSet) startItem() {
	clear(s.inItem)
}

// add records that path was found with value (which may be "").
func (s *fieldSet) add(path, value string) {
	f := s.byPath[path]
	if f == nil {
		f = &Field{Path: path}
		if s.inItem == nil {
			f.Count = 1
		}
		s.byPath[path] = f
		s.fields = append(s.fields, f)
	}
	if s.inItem != nil && !s.inItem[path] {
		s.inItem[path] = true
		f.Count++
	}
	if f.Sample == "" {
		f.Sample = sample(value)
	}
}

// list returns the fields in the order they were found, marked used when
// used has them.
func (s *fieldSet) list(used map[string]bool) []Field {
	out := make([]Field, len(s.fields))
	for i, f := range s.fields {
		out[i] = *f
		out[i].Used = isUsed(used, f.Path)
	}
	return out
}

// sample returns v whitespace-collapsed, as valid UTF-8, cut at sampleLen
// runes.
func sample(v string) string {
	v = collapseSpace(strings.ToValidUTF8(v, "\uFFFD"))
	n := 0
	for i := range v {
		if n == sampleLen {
			return v[:i]
		}
		n++
	}
	return v
}

// joinPath joins a parent path and a child's name with sep; a child of the
// channel or item itself (parent "") is its name alone.
func joinPath(parent, sep, name string) string {
	if parent == "" {
		return name
	}
	return parent + sep + name
}

// xmlInspector walks an RSS, RDF or Atom document, collecting its fields and
// the source of its items.
type xmlInspector struct {
	body    []byte // the document, without a BOM: what d reads
	d       *xml.Decoder
	channel *fieldSet
	items   *fieldSet
	raws    []string
}

// inspectXML returns the channel's and items' fields of an RSS, RDF or Atom
// document, and the source of each item: rss/channel/item, rdf:RDF/item or
// feed/entry. It returns what it found before any syntax error.
func inspectXML(body []byte) (channel, items *fieldSet, raws []string) {
	b := bytes.TrimPrefix(body, utf8BOM)
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	// Pass any declared encoding through undecoded, so offsets are the
	// document's.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	w := &xmlInspector{body: b, d: d, channel: newFieldSet(false), items: newFieldSet(true)}
	_ = w.document() // what was found so far is still shown
	return w.channel, w.items, w.raws
}

func (w *xmlInspector) document() error {
	var root xml.StartElement
	for {
		tok, err := w.d.Token()
		if err != nil {
			return err
		}
		if t, ok := tok.(xml.StartElement); ok {
			root = t
			break
		}
	}
	ns := defaultNamespace("", root)

	switch name := elementName(root.Name, ns); name {
	case "rss":
		// The channel holds its fields and the items.
		return w.eachChild(func(c xml.StartElement, off int64) error {
			cns := defaultNamespace(ns, c)
			if elementName(c.Name, cns) != "channel" {
				return w.d.Skip()
			}
			return w.container(cns, "item")
		})
	case "rdf:RDF":
		// The channel holds its fields; the items are its siblings.
		return w.eachChild(func(c xml.StartElement, off int64) error {
			cns := defaultNamespace(ns, c)
			switch elementName(c.Name, cns) {
			case "channel":
				return w.children(w.channel, "", cns, 0)
			case "item":
				return w.item(c, off, cns)
			}
			return w.d.Skip()
		})
	default:
		// Atom: the feed holds its fields and the entries.
		return w.container(ns, "entry")
	}
}

// container reads the rest of the element whose start tag was just read:
// children named itemName are items, the others channel fields.
func (w *xmlInspector) container(ns, itemName string) error {
	return w.eachChild(func(c xml.StartElement, off int64) error {
		cns := defaultNamespace(ns, c)
		if elementName(c.Name, cns) == itemName {
			return w.item(c, off, cns)
		}
		return w.field(w.channel, "", c, ns, 1)
	})
}

// eachChild reads the rest of the element whose start tag was just read,
// calling fn with each child's start tag and the offset of its "<". fn must
// read the rest of the child.
func (w *xmlInspector) eachChild(fn func(c xml.StartElement, off int64) error) error {
	for {
		off := w.d.InputOffset()
		tok, err := w.d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := fn(t, off); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// item reads the rest of an item whose start tag start (in default
// namespace ns) began at off, recording its fields and its source.
func (w *xmlInspector) item(start xml.StartElement, off int64, ns string) error {
	w.items.startItem()
	w.attrs(w.items, "", start)
	if err := w.children(w.items, "", ns, 0); err != nil {
		return err
	}
	w.raws = append(w.raws, string(w.body[off:w.d.InputOffset()]))
	return nil
}

// field records the element start, a child at level (1 for a child of the
// channel or item) of path parent whose parent's default namespace is
// parentNS, then reads the rest of it.
func (w *xmlInspector) field(set *fieldSet, parent string, start xml.StartElement, parentNS string, level int) error {
	ns := defaultNamespace(parentNS, start)
	path := joinPath(parent, "/", elementName(start.Name, ns))
	set.add(path, "")
	w.attrs(set, path, start)
	var text strings.Builder
	for {
		tok, err := w.d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			text.Write(t)
		case xml.StartElement:
			if level >= maxFieldDepth {
				if err := w.d.Skip(); err != nil {
					return err
				}
				continue
			}
			if err := w.field(set, path, t, ns, level+1); err != nil {
				return err
			}
		case xml.EndElement:
			set.add(path, text.String())
			return nil
		}
	}
}

// children reads the rest of the element at path (at level) whose start tag
// was just read, recording its children as fields.
func (w *xmlInspector) children(set *fieldSet, path, ns string, level int) error {
	return w.eachChild(func(c xml.StartElement, _ int64) error {
		return w.field(set, path, c, ns, level+1)
	})
}

// attrs records the attributes of start, other than namespace
// declarations, as fields of path.
func (w *xmlInspector) attrs(set *fieldSet, path string, start xml.StartElement) {
	for _, a := range start.Attr {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		name := a.Name.Local
		if a.Name.Space != "" {
			name = prefixedName(a.Name)
		}
		set.add(path+"@"+name, a.Value)
	}
}

// defaultNamespace returns the default namespace inside start, whose
// parent's is parent.
func defaultNamespace(parent string, start xml.StartElement) string {
	for _, a := range start.Attr {
		if a.Name.Space == "" && a.Name.Local == "xmlns" {
			return a.Value
		}
	}
	return parent
}

// elementName names an element in a Field path: without a prefix in the
// default namespace ns, else with its conventional prefix.
func elementName(n xml.Name, ns string) string {
	if n.Space == "" || n.Space == ns {
		return n.Local
	}
	return prefixedName(n)
}

// prefixedName returns n with its namespace's conventional prefix, or as
// "{uri}name" for an unknown namespace. An undeclared prefix, which the
// decoder leaves as the namespace, is kept when it's a conventional one.
func prefixedName(n xml.Name) string {
	if p, ok := namespacePrefixes[n.Space]; ok {
		return p + ":" + n.Local
	}
	for _, p := range namespacePrefixes {
		if p == n.Space {
			return p + ":" + n.Local
		}
	}
	return "{" + n.Space + "}" + n.Local
}

// inspectJSON returns the feed's and items' fields of a JSON Feed, and each
// item's object indented. It returns what it found before any syntax error.
func inspectJSON(body []byte) (channel, items *fieldSet, raws []string) {
	b := bytes.TrimPrefix(body, utf8BOM)
	channel, items = newFieldSet(false), newFieldSet(true)

	var doc struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(b, &doc); err == nil {
		for _, it := range doc.Items {
			var buf bytes.Buffer
			if err := json.Indent(&buf, it, "", "  "); err != nil {
				raws = nil
				break
			}
			raws = append(raws, buf.String())
		}
	}

	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = jsonFeedFields(d, channel, items) // what was found so far is still shown
	return channel, items, raws
}

// jsonFeedFields reads a JSON Feed's top-level object: "items" into items,
// the other members into channel.
func jsonFeedFields(d *json.Decoder, channel, items *fieldSet) error {
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return err
	}
	for d.More() {
		key, err := jsonKey(d)
		if err != nil {
			return err
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if key != "items" || tok != json.Delim('[') {
			if err := walkJSON(d, channel, key, 1, tok); err != nil {
				return err
			}
			continue
		}
		for d.More() {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			if tok != json.Delim('{') {
				if err := skipJSON(d, tok); err != nil {
					return err
				}
				continue
			}
			items.startItem()
			if err := walkJSONObject(d, items, "", 0); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil { // ']'
			return err
		}
	}
	return nil
}

// walkJSON records the value at path (at level), whose first token tok was
// just read, and its members up to maxFieldDepth.
func walkJSON(d *json.Decoder, set *fieldSet, path string, level int, tok json.Token) error {
	switch tok {
	case json.Delim('{'):
		set.add(path, "")
		return walkJSONObject(d, set, path, level)
	case json.Delim('['):
		set.add(path, "")
		for d.More() {
			elem, err := d.Token()
			if err != nil {
				return err
			}
			switch elem {
			case json.Delim('{'):
				if err := walkJSONObject(d, set, path+"[]", level); err != nil {
					return err
				}
			case json.Delim('['):
				if err := skipJSON(d, elem); err != nil {
					return err
				}
			default:
				set.add(path, jsonScalar(elem))
			}
		}
		_, err := d.Token() // ']'
		return err
	}
	set.add(path, jsonScalar(tok))
	return nil
}

// walkJSONObject reads the rest of an object at path (at level) whose "{"
// was just read, recording its members.
func walkJSONObject(d *json.Decoder, set *fieldSet, path string, level int) error {
	for d.More() {
		key, err := jsonKey(d)
		if err != nil {
			return err
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if level >= maxFieldDepth {
			err = skipJSON(d, tok)
		} else {
			err = walkJSON(d, set, joinPath(path, ".", key), level+1, tok)
		}
		if err != nil {
			return err
		}
	}
	_, err := d.Token() // '}'
	return err
}

// jsonKey reads an object member's name.
func jsonKey(d *json.Decoder) (string, error) {
	tok, err := d.Token()
	if err != nil {
		return "", err
	}
	key, _ := tok.(string)
	return key, nil
}

// skipJSON reads the rest of a value whose first token tok was just read.
func skipJSON(d *json.Decoder, tok json.Token) error {
	if tok != json.Delim('{') && tok != json.Delim('[') {
		return nil
	}
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
	return nil
}

// jsonScalar formats a JSON string, number, boolean or null ("").
func jsonScalar(tok json.Token) string {
	switch v := tok.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}
