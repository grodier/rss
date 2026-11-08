package assets

import (
	"testing"
)

// groupTags against a manifest written here: shared chunks, CSS imported by
// an entry and by a chunk, and broken manifests. The reader's build (dir
// "app") and the admin's ("admin") name their groups' files the same way.
func TestGroupTags(t *testing.T) {
	for _, dir := range []string{"app", "admin"} {
		t.Run(dir, func(t *testing.T) {
			a := &Assets{baseSheet: dir + "/styles/base.css", sheetPrefix: dir + "/styles/", entryPrefix: dir + "/entries/"}
			m := map[string]ManifestEntry{
				dir + "/styles/base.css":   {File: "assets/base-1.css", IsEntry: true},
				dir + "/styles/a.css":      {File: "assets/a-1.css", IsEntry: true},
				dir + "/styles/other.css":  {File: "assets/other-1.css", IsEntry: true},
				dir + "/entries/a.ts":      {File: "assets/a-1.js", IsEntry: true, Imports: []string{"_x.js", "_y.js"}, CSS: []string{"assets/a-1.css", "assets/other-1.css"}},
				dir + "/entries/b.ts":      {File: "assets/b-1.js", IsEntry: true},
				"_x.js":                    {File: "assets/chunks/x-1.js", Imports: []string{"_z.js"}},
				"_y.js":                    {File: "assets/chunks/y-1.js", Imports: []string{"_z.js"}, CSS: []string{"assets/y-1.css"}},
				"_z.js":                    {File: "assets/chunks/z-1.js"},
				"shared/fonts/f.woff2":     {File: "assets/f-1.woff2"},
				dir + "/entries/broken.ts": {File: "assets/broken-1.js", IsEntry: true, Imports: []string{"_missing.js"}},
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
				got, err := a.groupTags(m, tt.group)
				if err != nil || string(got) != tt.want {
					t.Errorf("groupTags(%q) = %v\n%s\nwant:\n%s", tt.group, err, got, tt.want)
				}
			}
			for _, group := range []string{"nope", "broken"} {
				if got, err := a.groupTags(m, group); err == nil {
					t.Errorf("groupTags(%q) = %q, nil error; want an error", group, got)
				}
			}
			// The other build's files aren't this one's groups.
			other := map[string]string{"app": "admin", "admin": "app"}[dir]
			m[other+"/entries/c.ts"] = ManifestEntry{File: "assets/c-1.js", IsEntry: true}
			if got, err := a.groupTags(m, "c"); err == nil {
				t.Errorf("groupTags(%q) of %s's build = %q, nil error; want an error", "c", other, got)
			}
			delete(m, dir+"/styles/base.css")
			if got, err := a.groupTags(m, "a"); err == nil {
				t.Errorf("groupTags without base.css = %q, nil error; want an error", got)
			}
		})
	}
}

func TestPageGroup(t *testing.T) {
	a := &Assets{baseSheet: "admin/styles/base.css", sheetPrefix: "admin/styles/", entryPrefix: "admin/entries/"}
	tests := []struct {
		src, group string
		ok         bool
	}{
		{"admin/styles/admin.css", "admin", true},
		{"admin/entries/admin.ts", "admin", true},
		{"admin/styles/base.css", "", false},
		{"admin/styles/partials/shell.css", "", false},
		{"app/styles/reading.css", "", false},
		{"shared/fonts/newsreader.woff2", "", false},
		{"_chunk.js", "", false},
	}
	for _, tt := range tests {
		if group, ok := a.pageGroup(tt.src); ok != tt.ok || (ok && group != tt.group) {
			t.Errorf("pageGroup(%q) = %q, %v; want %q, %v", tt.src, group, ok, tt.group, tt.ok)
		}
	}
}
