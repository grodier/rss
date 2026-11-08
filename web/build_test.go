package web

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

// cssURL matches a url() in a stylesheet and captures its target.
var cssURL = regexp.MustCompile(`url\(\s*["']?([^"')\s]+)`)

// Vite rewrites the url()s of the stylesheets it builds to the files it
// emits; each must be one /static serves.
func TestBuiltCSSURLsResolve(t *testing.T) {
	stylesheets, err := fs.Glob(App, "assets/*.css")
	if err != nil {
		t.Fatal(err)
	}
	if len(stylesheets) == 0 {
		t.Fatal("found no built stylesheets")
	}
	urls := 0
	for _, name := range stylesheets {
		b, err := fs.ReadFile(App, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range cssURL.FindAllStringSubmatch(string(b), -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") {
				continue
			}
			urls++
			target, ok := strings.CutPrefix(ref, "/static/")
			if !ok {
				if strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") {
					t.Errorf("%s: url(%s) is outside /static/", name, ref)
					continue
				}
				target = path.Join(path.Dir(name), ref)
			}
			target, _, _ = strings.Cut(target, "?")
			if _, err := fs.Stat(App, target); err != nil {
				t.Errorf("%s: url(%s): %v", name, ref, err)
			}
		}
	}
	if urls == 0 {
		t.Error("the built stylesheets have no url()s; the fonts should be linked")
	}
}

// cssProperty matches a declaration's property name, prefixed or not.
var cssProperty = regexp.MustCompile(`(?:^|[;{\s])(-(?:webkit|moz)-)?([a-z][a-z-]*)\s*:`)

// The minifier adds vendor-prefixed copies of properties for older browsers,
// and can drop the standard one when the source writes both with the same
// var() (backdrop-filter did). Every property the source stylesheet writes
// unprefixed, and the build prefixes, must stay unprefixed in the build too.
func TestBuiltCSSKeepsStandardProperties(t *testing.T) {
	src, err := os.ReadFile("app/styles/main.css")
	if err != nil {
		t.Fatal(err)
	}
	standard := map[string]bool{}
	for _, m := range cssProperty.FindAllStringSubmatch(string(src), -1) {
		if m[1] == "" {
			standard[m[2]] = true
		}
	}

	built, err := fs.Glob(App, "assets/main-*.css")
	if err != nil || len(built) != 1 {
		t.Fatalf("built main.css: %v, %v", built, err)
	}
	b, err := fs.ReadFile(App, built[0])
	if err != nil {
		t.Fatal(err)
	}
	prefixed, kept := map[string]string{}, map[string]bool{}
	for _, m := range cssProperty.FindAllStringSubmatch(string(b), -1) {
		if m[1] == "" {
			kept[m[2]] = true
		} else {
			prefixed[m[2]] = m[1]
		}
	}
	for p, prefix := range prefixed {
		if standard[p] && !kept[p] {
			t.Errorf("the built main.css has %s%s but no %s", prefix, p, p)
		}
	}
}
