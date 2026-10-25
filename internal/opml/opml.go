// Package opml reads the feeds listed in OPML files.
package opml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/net/html/charset"
)

// ErrNotOPML is returned by Parse when the input isn't an OPML document.
var ErrNotOPML = errors.New("opml: not an OPML file")

// Outline is a feed listed in an OPML file.
type Outline struct {
	URL     string   // the xmlUrl attribute, whitespace-trimmed
	Title   string   // the title attribute, else text; whitespace-collapsed
	Folders []string // names of the outlines it's nested in, outermost first, without repeats; whitespace-collapsed
}

// Parse returns the feeds in an OPML document, in document order: every
// <outline> with a non-empty xmlUrl, at any depth, without repeats of the
// same URL string. An outline without an xmlUrl is a folder: the feeds
// nested in it list its name in Folders. A URL listed more than once is one
// Outline, at its first position, with the folders of every listing. A
// document whose root element isn't <opml> is ErrNotOPML; one with no feeds
// returns an empty slice.
func Parse(r io.Reader) ([]Outline, error) {
	// Exports from older readers are often sloppy: unescaped ampersands,
	// HTML entities, unclosed elements, legacy charsets.
	d := xml.NewDecoder(r)
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charset.NewReaderLabel

	if err := readRoot(d); err != nil {
		return nil, err
	}

	outlines := []Outline{}
	seen := map[string]int{} // URL to its index in outlines
	// The names of the open outlines, "" for feeds and unnamed folders. A
	// self-closing <outline/> is a start and an end token, so it pops itself.
	var open []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return outlines, nil
		}
		if err != nil {
			return nil, fmt.Errorf("opml: %w", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if !strings.EqualFold(el.Name.Local, "outline") {
				continue
			}
			o := outline(el.Attr)
			if o.URL == "" {
				open = append(open, o.Title)
				continue
			}
			open = append(open, "")
			if i, ok := seen[o.URL]; ok {
				outlines[i].Folders = addFolders(outlines[i].Folders, open)
				continue
			}
			o.Folders = addFolders(nil, open)
			seen[o.URL] = len(outlines)
			outlines = append(outlines, o)
		case xml.EndElement:
			if strings.EqualFold(el.Name.Local, "outline") && len(open) > 0 {
				open = open[:len(open)-1]
			}
		}
	}
}

// addFolders appends the non-empty names in open that folders doesn't have.
func addFolders(folders, open []string) []string {
	for _, name := range open {
		if name != "" && !slices.Contains(folders, name) {
			folders = append(folders, name)
		}
	}
	return folders
}

// readRoot reads up to the document's root element and returns ErrNotOPML
// unless it is <opml>.
func readRoot(d *xml.Decoder) error {
	for {
		tok, err := d.Token()
		if err != nil {
			// Empty input, or something that isn't XML.
			return ErrNotOPML
		}
		if el, ok := tok.(xml.StartElement); ok {
			if !strings.EqualFold(el.Name.Local, "opml") {
				return ErrNotOPML
			}
			return nil
		}
	}
}

// outline reads an <outline>'s attributes, matching their names
// case-insensitively (xmlUrl, xmlurl).
func outline(attrs []xml.Attr) Outline {
	var o Outline
	var text string
	for _, a := range attrs {
		switch strings.ToLower(a.Name.Local) {
		case "xmlurl":
			o.URL = strings.TrimSpace(a.Value)
		case "title":
			o.Title = collapseSpace(a.Value)
		case "text":
			text = collapseSpace(a.Value)
		}
	}
	if o.Title == "" {
		o.Title = text
	}
	return o
}

// collapseSpace trims s and replaces each run of whitespace with one space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
