package ui

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// cssBlock returns the declarations of the rule that opens with selector in
// css, one per element, in order. The rule must hold no nested blocks.
func cssBlock(t *testing.T, css, selector string) []string {
	t.Helper()
	i := strings.Index(css, selector+" {")
	if i < 0 {
		t.Fatalf("main.css has no %s rule", selector)
	}
	body := css[i+len(selector)+2:]
	body = body[:strings.Index(body, "}")]
	var decls []string
	for d := range strings.SplitSeq(cssComment.ReplaceAllString(body, ""), ";") {
		if d = strings.TrimSpace(d); d != "" {
			decls = append(decls, d)
		}
	}
	return decls
}

// colorTokens returns the names of the custom properties in decls whose value
// is a color, sorted.
func colorTokens(decls []string) []string {
	var names []string
	for _, d := range decls {
		name, value, ok := strings.Cut(d, ":")
		value = strings.TrimSpace(value)
		if ok && strings.HasPrefix(name, "--") && (strings.HasPrefix(value, "#") || strings.HasPrefix(value, "rgb")) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// The dark theme's values are written twice in main.css, for the system
// setting and for the user's choice, so they must stay the same; and every
// color token needs a dark value.
func TestDarkThemeTokens(t *testing.T) {
	b, err := fs.ReadFile(Static, "static/css/main.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)

	system := cssBlock(t, css, `:root:not([data-theme="light"])`)
	chosen := cssBlock(t, css, `:root[data-theme="dark"]`)
	if !slices.Equal(system, chosen) {
		t.Errorf("the dark theme's two blocks differ:\nsystem: %q\nchosen: %q", system, chosen)
	}

	light := colorTokens(cssBlock(t, css, ":root"))
	dark := colorTokens(chosen)
	if len(light) == 0 {
		t.Fatal("found no color tokens on :root")
	}
	if !slices.Equal(light, dark) {
		t.Errorf("light and dark define different color tokens:\nlight: %q\ndark:  %q", light, dark)
	}
}
