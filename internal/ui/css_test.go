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

// motionProperties are the properties a transition may not name outside
// prefers-reduced-motion: no-preference: they move, scale or resize things.
var motionProperties = map[string]bool{
	"transform": true, "translate": true, "scale": true, "rotate": true,
	"all": true, "block-size": true, "height": true, "clip-path": true,
}

// motionOutsideOptIn returns a description of each piece of motion in css
// that isn't inside a @media (prefers-reduced-motion: no-preference) block:
// animations, transitions of motionProperties and @view-transition rules.
// The spinner's pulse under prefers-reduced-motion: reduce is allowed.
func motionOutsideOptIn(css string) []string {
	css = cssComment.ReplaceAllString(css, "")
	var (
		stack []string // the preludes of the open blocks, outermost first
		found []string
		start int
	)
	optedIn := func() bool {
		return slices.ContainsFunc(stack, func(p string) bool {
			return strings.HasPrefix(p, "@media") && strings.Contains(p, "prefers-reduced-motion: no-preference")
		})
	}
	spinnerPulse := func() bool {
		return len(stack) > 0 && stack[len(stack)-1] == ".spinner" &&
			slices.ContainsFunc(stack, func(p string) bool {
				return strings.HasPrefix(p, "@media") && strings.Contains(p, "prefers-reduced-motion: reduce")
			})
	}
	where := func() string { return strings.Join(stack, " > ") }
	declaration := func(d string) {
		name, value, ok := strings.Cut(strings.TrimSpace(d), ":")
		if !ok || optedIn() {
			return
		}
		name = strings.TrimSpace(name)
		switch name {
		case "animation", "animation-name":
			if !spinnerPulse() {
				found = append(found, where()+": "+strings.TrimSpace(d))
			}
		case "transition", "transition-property":
			for w := range strings.FieldsFuncSeq(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
				if motionProperties[w] {
					found = append(found, where()+": "+strings.TrimSpace(d))
					break
				}
			}
		}
	}
	for i, c := range css {
		switch c {
		case '{':
			prelude := strings.Join(strings.Fields(css[start:i]), " ")
			if strings.HasPrefix(prelude, "@view-transition") && !optedIn() {
				found = append(found, where()+": "+prelude)
			}
			stack = append(stack, prelude)
			start = i + 1
		case ';':
			declaration(css[start:i])
			start = i + 1
		case '}':
			declaration(css[start:i])
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			start = i + 1
		}
	}
	return found
}

// Motion is opt-in: every animation, and every transition that moves
// something, is written inside @media (prefers-reduced-motion: no-preference).
// See "Motion and feedback" in docs/decisions.md.
func TestMotionIsOptIn(t *testing.T) {
	b, err := fs.ReadFile(Static, "static/css/main.css")
	if err != nil {
		t.Fatal(err)
	}
	if found := motionOutsideOptIn(string(b)); len(found) > 0 {
		t.Errorf("main.css has motion outside @media (prefers-reduced-motion: no-preference):\n%s", strings.Join(found, "\n"))
	}

	tests := []struct {
		name string
		css  string
		ok   bool
	}{
		{"animation at the top level", `.a { animation: x 1s; }`, false},
		{"animation-name at the top level", `.a { animation-name: x }`, false},
		{"animation inside no-preference", `@media (prefers-reduced-motion: no-preference) { .a { animation: x 1s; } }`, true},
		{"animation nested in a layer", `@layer components { @media (prefers-reduced-motion: no-preference) { .a { animation: x 1s; } } }`, true},
		{"animation in a layer only", `@layer components { .a { animation: x 1s; } }`, false},
		{"background-color transition", `.a { transition: background-color 150ms ease-out; }`, true},
		{"color and opacity transitions", `.a { transition: color 150ms, opacity 150ms; }`, true},
		{"transform transition", `.a { transition: transform 150ms; }`, false},
		{"transform as a later transition", `.a { transition: color 150ms, transform 150ms; }`, false},
		{"all transition", `.a { transition: all 150ms; }`, false},
		{"transition-property height", `.a { transition-property: height; }`, false},
		{"transform transition inside no-preference", `@media (prefers-reduced-motion: no-preference) { .a { transition: transform 150ms; } }`, true},
		{"view transition at the top level", `@view-transition { navigation: auto; }`, false},
		{"view transition inside no-preference", `@media (prefers-reduced-motion: no-preference) { @view-transition { navigation: auto; } }`, true},
		{"spinner pulse inside reduce", `@media (prefers-reduced-motion: reduce) { .spinner { animation: pulse 1.6s infinite; } }`, true},
		{"another animation inside reduce", `@media (prefers-reduced-motion: reduce) { .a { animation: pulse 1.6s infinite; } }`, false},
		{"commented-out animation", `.a { /* animation: x 1s; */ color: red; }`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := motionOutsideOptIn(tt.css)
			if ok := len(found) == 0; ok != tt.ok {
				t.Errorf("motionOutsideOptIn(%q) = %q, want ok = %v", tt.css, found, tt.ok)
			}
		})
	}
}
