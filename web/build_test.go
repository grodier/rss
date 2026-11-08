package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// cssURL matches a url() in a stylesheet and captures its target.
var cssURL = regexp.MustCompile(`url\(\s*["']?([^"')\s]+)`)

// builds are the frontend's two builds, by name.
var builds = []struct {
	name string
	fsys fs.FS
}{{"app", App}, {"admin", Admin}}

// Vite rewrites the url()s of the stylesheets it builds to the files it
// emits; each must be one /static serves, from the same build.
func TestBuiltCSSURLsResolve(t *testing.T) {
	for _, b := range builds {
		t.Run(b.name, func(t *testing.T) {
			testBuiltCSSURLsResolve(t, b.fsys)
		})
	}
}

func testBuiltCSSURLsResolve(t *testing.T, fsys fs.FS) {
	stylesheets, err := fs.Glob(fsys, "assets/*.css")
	if err != nil {
		t.Fatal(err)
	}
	if len(stylesheets) == 0 {
		t.Fatal("found no built stylesheets")
	}
	urls := 0
	for _, name := range stylesheets {
		b, err := fs.ReadFile(fsys, name)
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
			if _, err := fs.Stat(fsys, target); err != nil {
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
// var() (backdrop-filter did). Every property the source stylesheets write
// unprefixed, and the build prefixes, must stay unprefixed in the build too.
func TestBuiltCSSKeepsStandardProperties(t *testing.T) {
	standard := map[string]bool{}
	for _, name := range stylesheets(t) {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range cssProperty.FindAllStringSubmatch(string(src), -1) {
			if m[1] == "" {
				standard[m[2]] = true
			}
		}
	}

	for _, b := range builds {
		t.Run(b.name, func(t *testing.T) {
			testBuiltCSSKeepsStandardProperties(t, b.fsys, standard)
		})
	}
}

func testBuiltCSSKeepsStandardProperties(t *testing.T, fsys fs.FS, standard map[string]bool) {
	built, err := fs.Glob(fsys, "assets/*.css")
	if err != nil || len(built) == 0 {
		t.Fatalf("built stylesheets: %v, %v", built, err)
	}
	for _, name := range built {
		b, err := fs.ReadFile(fsys, name)
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
				t.Errorf("the built %s has %s%s but no %s", name, prefix, p, p)
			}
		}
	}
}

// manifestEntry is the part of a .vite/manifest.json entry these tests read.
type manifestEntry struct {
	File    string   `json:"file"`
	Imports []string `json:"imports"`
	CSS     []string `json:"css"`
}

// The reading pages' budgets, gzipped: their JavaScript (the reading entry
// and the chunks it imports) and their CSS (base.css, reading.css and the
// entry's CSS). Each is what #547 measured plus 10%. The reading pages are
// where readers spend their time, so growing them is a decision: raise a
// budget only in a PR that says why in its description.
const (
	readingJSBudget  = 5970  // 5427 measured
	readingCSSBudget = 10210 // 9284 measured
)

// The admin's build has a manifest, with its page group's entry and the base
// sheet every admin page links (internal/admin reads them at startup).
func TestAdminManifest(t *testing.T) {
	b, err := fs.ReadFile(Admin, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]manifestEntry
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"admin/entries/admin.ts", "admin/styles/base.css"} {
		e, ok := m[src]
		if !ok {
			t.Errorf("the admin manifest has no %s", src)
			continue
		}
		if _, err := fs.Stat(Admin, e.File); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	for src := range m {
		if strings.HasPrefix(src, "app/") {
			t.Errorf("the admin build has the reader's %s", src)
		}
	}
}

func TestReadingBundleBudget(t *testing.T) {
	b, err := fs.ReadFile(App, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]manifestEntry
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	entry, ok := m["app/entries/reading.ts"]
	if !ok {
		t.Fatal("the manifest has no app/entries/reading.ts")
	}

	js := []string{entry.File}
	css := []string{m["app/styles/base.css"].File, m["app/styles/reading.css"].File}
	seen := map[string]bool{}
	var walk func(e manifestEntry)
	walk = func(e manifestEntry) {
		css = append(css, e.CSS...)
		for _, imp := range e.Imports {
			if !seen[imp] {
				seen[imp] = true
				js = append(js, m[imp].File)
				walk(m[imp])
			}
		}
	}
	walk(entry)
	slices.Sort(css)
	css = slices.Compact(css)

	for _, tt := range []struct {
		kind   string
		files  []string
		budget int
	}{
		{"JavaScript", js, readingJSBudget},
		{"CSS", css, readingCSSBudget},
	} {
		size := 0
		for _, name := range tt.files {
			size += gzipSize(t, name)
		}
		t.Logf("the reading pages' %s: %d bytes gzipped, in %s", tt.kind, size, strings.Join(tt.files, ", "))
		if size > tt.budget {
			t.Errorf("the reading pages' %s is %d bytes gzipped, over its budget of %d. Raising a budget is a decision: say why in the PR description.", tt.kind, size, tt.budget)
		}
	}
}

// gzipSize returns the size of name, a file in App, gzipped at the default
// level.
func gzipSize(t *testing.T, name string) int {
	t.Helper()
	b, err := fs.ReadFile(App, name)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Len()
}
