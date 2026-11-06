package ui

import (
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

// shapeAttrs are the attributes of a shape that decide how it's drawn.
var shapeAttrs = []string{"d", "x", "y", "width", "height", "rx", "fill", "stroke", "stroke-width"}

// logoShapes returns each shape drawn in the SVG document in r, in order, as
// its element name and shapeAttrs. Shapes inside <defs> (the clip) and the
// dusk, which the inline logo adds for the sunrise, are left out.
func logoShapes(t *testing.T, r io.Reader) []string {
	t.Helper()
	var (
		shapes []string
		inDefs int
	)
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			attrs := map[string]string{}
			for _, a := range el.Attr {
				attrs[a.Name.Local] = a.Value
			}
			switch {
			case el.Name.Local == "defs":
				inDefs++
			case inDefs > 0 || strings.Contains(attrs["class"], "logo-dusk"):
			case el.Name.Local == "rect" || el.Name.Local == "path" || el.Name.Local == "circle":
				shape := el.Name.Local
				for _, name := range shapeAttrs {
					if v, ok := attrs[name]; ok {
						shape += fmt.Sprintf(" %s=%q", name, v)
					}
				}
				shapes = append(shapes, shape)
			}
		case xml.EndElement:
			if el.Name.Local == "defs" {
				inDefs--
			}
		}
	}
	return shapes
}

// The landing page and Log in draw the logo inline so its sun can rise
// (partials/logo.html); it must draw the same shapes as static/img/logo.svg.
func TestInlineLogoMatchesFile(t *testing.T) {
	file, err := fs.ReadFile(Static, "static/img/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	partial, err := fs.ReadFile(Templates, "templates/partials/logo.html")
	if err != nil {
		t.Fatal(err)
	}
	inline := strings.TrimSpace(templateAction.ReplaceAllString(string(partial), ""))

	want := logoShapes(t, strings.NewReader(string(file)))
	got := logoShapes(t, strings.NewReader(inline))
	if len(want) == 0 {
		t.Fatal("found no shapes in logo.svg")
	}
	if !slices.Equal(got, want) {
		t.Errorf("the inline logo's shapes differ from logo.svg's:\ninline: %q\nfile:   %q", got, want)
	}
	for _, class := range []string{"logo-sun", "logo-sky", "logo-dusk"} {
		if !strings.Contains(inline, `class="`+class+`"`) {
			t.Errorf("the inline logo has no %s", class)
		}
	}
}
