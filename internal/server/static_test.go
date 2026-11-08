package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/ui"
	"github.com/grodier/rss/web"
)

// aChunk returns the path in web.App of one of the build's chunks.
func aChunk(t *testing.T) string {
	t.Helper()
	for src, file := range static.manifest {
		if strings.HasPrefix(src, "_") && strings.HasSuffix(file, ".js") {
			return file
		}
	}
	t.Fatal("the build has no chunks")
	return ""
}

// outdated returns name, a hashed file, with another hash of the same length.
func outdated(name string) string {
	ext := name[strings.LastIndex(name, "."):]
	i := strings.LastIndex(name, "-")
	return name[:i+1] + strings.Repeat("A", len(name)-i-1-len(ext)) + ext
}

// assetTag is one tag assetTags writes: its kind ("stylesheet",
// "modulepreload" or "script") and its URL.
type assetTag struct{ kind, url string }

var assetTagPattern = regexp.MustCompile(`^(?:<link rel="(stylesheet|modulepreload)" href="([^"]+)" />|<script type="(module)" src="([^"]+)"></script>)$`)

func parseAssetTags(t *testing.T, tags string) []assetTag {
	t.Helper()
	var out []assetTag
	for line := range strings.SplitSeq(tags, "\n") {
		m := assetTagPattern.FindStringSubmatch(strings.TrimSpace(line))
		switch {
		case m == nil:
			t.Fatalf("unexpected tag %q in:\n%s", line, tags)
		case m[1] != "":
			out = append(out, assetTag{m[1], m[2]})
		default:
			out = append(out, assetTag{"script", m[4]})
		}
	}
	return out
}

func TestAssetTags(t *testing.T) {
	sheet := func(name string) string {
		u, err := asset("app/styles/" + name + ".css")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	// The chunks an entry imports, recursively, from the manifest.
	chunks := func(group string) []string {
		var urls []string
		seen := map[string]bool{}
		var walk func(src string)
		walk = func(src string) {
			for _, imp := range manifestImports(t, src) {
				if !seen[imp] {
					seen[imp] = true
					urls = append(urls, "/static/"+static.manifest[imp])
					walk(imp)
				}
			}
		}
		walk("app/entries/" + group + ".ts")
		slices.Sort(urls)
		return urls
	}

	tests := []struct {
		group  string
		sheets []string // in order
		script bool
	}{
		{"reading", []string{sheet("base"), sheet("reading")}, true},
		// An entry that imports another group's sheet links it.
		{"timelines", []string{sheet("base"), sheet("settings")}, true},
		{"login", []string{sheet("base"), sheet("solo")}, true},
		// A group without an entry: stylesheets only.
		{"settings", []string{sheet("base"), sheet("settings")}, false},
		{"solo", []string{sheet("base"), sheet("solo")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			html, err := assetTags(tt.group)
			if err != nil {
				t.Fatal(err)
			}
			tags := parseAssetTags(t, string(html))

			// Stylesheets, then preloads, then the script.
			var sheets, preloads, scripts []string
			order := map[string]int{"stylesheet": 0, "modulepreload": 1, "script": 2}
			last := 0
			for _, tag := range tags {
				if order[tag.kind] < last {
					t.Errorf("%s %s comes after a later kind of tag:\n%s", tag.kind, tag.url, html)
				}
				last = order[tag.kind]
				switch tag.kind {
				case "stylesheet":
					sheets = append(sheets, tag.url)
				case "modulepreload":
					preloads = append(preloads, tag.url)
				case "script":
					scripts = append(scripts, tag.url)
				}
			}
			if !slices.Equal(sheets, tt.sheets) {
				t.Errorf("stylesheets = %q, want %q", sheets, tt.sheets)
			}
			if !tt.script {
				if len(preloads)+len(scripts) > 0 {
					t.Errorf("a group without an entry has preloads %q and scripts %q", preloads, scripts)
				}
				return
			}
			if !slices.Equal(scripts, []string{entryJS(tt.group)}) {
				t.Errorf("scripts = %q, want only %q", scripts, entryJS(tt.group))
			}
			// One preload per imported chunk, none twice.
			sorted := slices.Sorted(slices.Values(preloads))
			if want := chunks(tt.group); !slices.Equal(sorted, want) {
				t.Errorf("preloads = %q, want each of %q once", preloads, want)
			}
			if len(preloads) == 0 && tt.group == "reading" {
				t.Error("the reading entry imports no chunks; the test can't check its preloads")
			}
		})
	}

	for _, group := range []string{"nope", "base", "partials/rows", ""} {
		if html, err := assetTags(group); err == nil {
			t.Errorf("assetTags(%q) = %q, nil error; want an error", group, html)
		}
	}

	// A page's head carries its group's tags.
	rr := httptest.NewRecorder()
	newTestServer(t).router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	want, _ := assetTags("login")
	if !strings.Contains(rr.Body.String(), "\n  "+string(want)+"\n") {
		t.Errorf("GET /login lacks the login group's tags:\n%s", rr.Body.String())
	}
}

// manifestImports returns the chunks a manifest entry imports, from the
// build's manifest.
func manifestImports(t *testing.T, src string) []string {
	t.Helper()
	m, err := readManifest(web.App)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m[src]
	if !ok {
		t.Fatalf("the manifest has no %s", src)
	}
	return e.Imports
}

// groupTags against a manifest written here: shared chunks, CSS imported by
// an entry and by a chunk, and broken manifests.
func TestGroupTags(t *testing.T) {
	m := map[string]manifestEntry{
		"app/styles/base.css":   {File: "assets/base-1.css", IsEntry: true},
		"app/styles/a.css":      {File: "assets/a-1.css", IsEntry: true},
		"app/styles/other.css":  {File: "assets/other-1.css", IsEntry: true},
		"app/entries/a.ts":      {File: "assets/a-1.js", IsEntry: true, Imports: []string{"_x.js", "_y.js"}, CSS: []string{"assets/a-1.css", "assets/other-1.css"}},
		"app/entries/b.ts":      {File: "assets/b-1.js", IsEntry: true},
		"_x.js":                 {File: "assets/chunks/x-1.js", Imports: []string{"_z.js"}},
		"_y.js":                 {File: "assets/chunks/y-1.js", Imports: []string{"_z.js"}, CSS: []string{"assets/y-1.css"}},
		"_z.js":                 {File: "assets/chunks/z-1.js"},
		"shared/fonts/f.woff2":  {File: "assets/f-1.woff2"},
		"app/entries/broken.ts": {File: "assets/broken-1.js", IsEntry: true, Imports: []string{"_missing.js"}},
	}
	tests := []struct {
		group, want string
	}{
		{"a", `<link rel="stylesheet" href="/static/assets/base-1.css" />
  <link rel="stylesheet" href="/static/assets/a-1.css" />
  <link rel="stylesheet" href="/static/assets/other-1.css" />
  <link rel="stylesheet" href="/static/assets/y-1.css" />
  <link rel="modulepreload" href="/static/assets/chunks/x-1.js" />
  <link rel="modulepreload" href="/static/assets/chunks/z-1.js" />
  <link rel="modulepreload" href="/static/assets/chunks/y-1.js" />
  <script type="module" src="/static/assets/a-1.js"></script>`},
		{"b", `<link rel="stylesheet" href="/static/assets/base-1.css" />
  <script type="module" src="/static/assets/b-1.js"></script>`},
		{"other", `<link rel="stylesheet" href="/static/assets/base-1.css" />
  <link rel="stylesheet" href="/static/assets/other-1.css" />`},
	}
	for _, tt := range tests {
		got, err := groupTags(m, tt.group)
		if err != nil || string(got) != tt.want {
			t.Errorf("groupTags(%q) = %v\n%s\nwant:\n%s", tt.group, err, got, tt.want)
		}
	}
	for _, group := range []string{"nope", "broken"} {
		if got, err := groupTags(m, group); err == nil {
			t.Errorf("groupTags(%q) = %q, nil error; want an error", group, got)
		}
	}
	delete(m, "app/styles/base.css")
	if got, err := groupTags(m, "a"); err == nil {
		t.Errorf("groupTags without base.css = %q, nil error; want an error", got)
	}
}

// Every page names its group once, through the assets partial, and links no
// stylesheet or script of its own.
func TestPagesNameOneGroup(t *testing.T) {
	assets := regexp.MustCompile(`\{\{template "assets" "([^"]*)"\}\}`)
	own := regexp.MustCompile(`<script|rel="(stylesheet|modulepreload)"`)

	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	partials, err := fs.Glob(ui.Templates, "templates/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(pages, partials...) {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if own.MatchString(src) {
			t.Errorf("%s links a stylesheet or script of its own; name the page's group instead", f)
		}
		if strings.Contains(f, "/partials/") {
			continue
		}
		uses := assets.FindAllStringSubmatch(src, -1)
		if len(uses) != 1 {
			t.Errorf(`%s uses {{template "assets" "<group>"}} %d times, want once`, f, len(uses))
			continue
		}
		if _, err := assetTags(uses[0][1]); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
