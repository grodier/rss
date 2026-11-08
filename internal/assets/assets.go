// Package assets reads a frontend build (web.App, the reader's, or
// web.Admin, the admin's) from its Vite manifest: the tags that load each
// page group's files, the URLs of its other files, and how /static serves
// them.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Assets describes one build, read once at startup.
type Assets struct {
	fsys fs.FS
	// baseSheet, sheetPrefix and entryPrefix name the build's page groups'
	// files (see Load).
	baseSheet, sheetPrefix, entryPrefix string
	// manifest maps each source path Vite built (the manifest's keys, e.g.
	// "shared/fonts/newsreader.woff2") to its built file in fsys
	// ("assets/newsreader-b12n-WE3.woff2").
	manifest map[string]string
	// versions maps every file /static serves, by its path in fsys, to the
	// first 12 hex digits of its content's SHA-256: its ETag, and the ?v= of a
	// public file.
	versions map[string]string
	// entries are the built entries (scripts and stylesheets), the files an
	// outdated hashed name falls back to (see current). Chunks aren't among
	// them.
	entries []hashedName
	// groups maps each page group to the tags that load its files (see
	// groupTags).
	groups map[string]template.HTML
}

// hashedName is a built file whose name carries its content hash:
// "assets/" + name + "-" + hash + ext.
type hashedName struct {
	file, name, hash, ext string
}

// ManifestEntry is the part of a .vite/manifest.json entry the servers use.
type ManifestEntry struct {
	File    string `json:"file"`
	Name    string `json:"name"`
	IsEntry bool   `json:"isEntry"`
	// Imports are the manifest keys of the chunks a script imports.
	Imports []string `json:"imports"`
	// CSS are the built stylesheets a script imports.
	CSS []string `json:"css"`
}

// Page groups. A page names its group once, {{template "assets" "<group>"}},
// and loads that group's files: <dir>/styles/base.css, then the group's own
// sheet, <dir>/styles/<group>.css, and its entry, <dir>/entries/<group>.ts,
// each if it exists (a group has at least one). An entry can import another
// group's sheet, which Vite lists as its CSS.
const (
	sheetExt     = ".css"
	entryExt     = ".ts"
	staticPrefix = "/static/"
)

// MustLoad is Load, panicking on an error: `make assets` builds the frontend,
// and without a build the embeds in package web don't compile.
func MustLoad(fsys fs.FS, dir string) *Assets {
	a, err := Load(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("load static files: %v", err))
	}
	return a
}

// ReadManifest reads a build's .vite/manifest.json, keyed by source path
// (or, for a chunk, "_" and its name).
func ReadManifest(fsys fs.FS) (map[string]ManifestEntry, error) {
	b, err := fs.ReadFile(fsys, ".vite/manifest.json")
	if err != nil {
		return nil, err
	}
	var entries map[string]ManifestEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse .vite/manifest.json: %w", err)
	}
	return entries, nil
}

// Load reads the build in fsys, whose page groups' sources are under dir
// (relative to web/: "app" for the reader, "admin" for the admin).
func Load(fsys fs.FS, dir string) (*Assets, error) {
	entries, err := ReadManifest(fsys)
	if err != nil {
		return nil, err
	}

	a := &Assets{
		fsys:        fsys,
		baseSheet:   dir + "/styles/base" + sheetExt,
		sheetPrefix: dir + "/styles/",
		entryPrefix: dir + "/entries/",
		manifest:    make(map[string]string),
		versions:    make(map[string]string),
		groups:      make(map[string]template.HTML),
	}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == ".vite" {
				return fs.SkipDir
			}
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a.versions[p] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	if err != nil {
		return nil, err
	}

	for src, e := range entries {
		if _, ok := a.versions[e.File]; !ok {
			return nil, fmt.Errorf(".vite/manifest.json: %s names a missing file %q", src, e.File)
		}
		a.manifest[src] = e.File
		if !e.IsEntry {
			continue
		}
		ext := path.Ext(e.File)
		hash, ok := strings.CutPrefix(strings.TrimSuffix(e.File, ext), "assets/"+e.Name+"-")
		if !ok || hash == "" {
			return nil, fmt.Errorf(".vite/manifest.json: %s: %q isn't named assets/%s-<hash>%s", src, e.File, e.Name, ext)
		}
		a.entries = append(a.entries, hashedName{file: e.File, name: e.Name, hash: hash, ext: ext})
	}

	for src := range entries {
		group, ok := a.pageGroup(src)
		if !ok || a.groups[group] != "" {
			continue
		}
		tags, err := a.groupTags(entries, group)
		if err != nil {
			return nil, err
		}
		a.groups[group] = tags
	}
	return a, nil
}

// pageGroup returns the group whose sheet or entry src (a manifest key) is.
// base.css is every group's, so no group's own.
func (a *Assets) pageGroup(src string) (string, bool) {
	if src == a.baseSheet {
		return "", false
	}
	if name, ok := strings.CutPrefix(src, a.sheetPrefix); ok {
		name, ok = strings.CutSuffix(name, sheetExt)
		return name, ok && !strings.Contains(name, "/")
	}
	if name, ok := strings.CutPrefix(src, a.entryPrefix); ok {
		name, ok = strings.CutSuffix(name, entryExt)
		return name, ok && !strings.Contains(name, "/")
	}
	return "", false
}

// groupTags returns the tags that load group's files, from the manifest, in
// the order the head needs them:
//
//  1. a stylesheet link for the base sheet, then for the group's sheet, then
//     for the CSS the entry and the chunks it imports list (recursively);
//  2. a modulepreload link for each chunk the entry imports, recursively, so
//     they download alongside it rather than after it;
//  3. the entry's module script.
//
// Each file is linked once. An unknown group is an error.
func (a *Assets) groupTags(m map[string]ManifestEntry, group string) (template.HTML, error) {
	sheet, hasSheet := m[a.sheetPrefix+group+sheetExt]
	entry, hasEntry := m[a.entryPrefix+group+entryExt]
	if !hasSheet && !hasEntry {
		return "", fmt.Errorf("unknown page group %q: no %s%s%s or %s%s%s", group, a.sheetPrefix, group, sheetExt, a.entryPrefix, group, entryExt)
	}
	base, ok := m[a.baseSheet]
	if !ok {
		return "", fmt.Errorf(".vite/manifest.json has no %s", a.baseSheet)
	}

	var sheets, preloads []string
	linked := map[string]bool{}
	addSheet := func(file string) {
		if !linked[file] {
			linked[file] = true
			sheets = append(sheets, file)
		}
	}
	addSheet(base.File)
	if hasSheet {
		addSheet(sheet.File)
	}
	if hasEntry {
		visited := map[string]bool{}
		var walk func(src string, e ManifestEntry) error
		walk = func(src string, e ManifestEntry) error {
			for _, css := range e.CSS {
				addSheet(css)
			}
			for _, imp := range e.Imports {
				if visited[imp] {
					continue
				}
				visited[imp] = true
				chunk, ok := m[imp]
				if !ok {
					return fmt.Errorf(".vite/manifest.json: %s imports a missing %q", src, imp)
				}
				preloads = append(preloads, chunk.File)
				if err := walk(imp, chunk); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(a.entryPrefix+group+entryExt, entry); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	tag := func(format, file string) {
		if b.Len() > 0 {
			b.WriteString("\n  ")
		}
		fmt.Fprintf(&b, format, html.EscapeString(staticPrefix+file))
	}
	for _, f := range sheets {
		tag(`<link rel="stylesheet" href="%s" />`, f)
	}
	for _, f := range preloads {
		tag(`<link rel="modulepreload" href="%s" />`, f)
	}
	if hasEntry {
		tag(`<script type="module" src="%s"></script>`, entry.File)
	}
	return template.HTML(b.String()), nil
}

// Tags returns the tags that load a page group's stylesheets and scripts
// (see groupTags), for the "assets" template of the pages' head. An unknown
// group is an error, which fails the template that names it.
func (a *Assets) Tags(group string) (template.HTML, error) {
	if tags, ok := a.groups[group]; ok {
		return tags, nil
	}
	return "", fmt.Errorf("unknown page group %q", group)
}

// hashed reports whether name (a path in the build) is a built file whose
// name carries its hash, so it never changes.
func hashed(name string) bool {
	return strings.HasPrefix(name, "assets/")
}

// current returns the built entry that name, a missing file, is an outdated
// version of: the same "assets/<name>-<hash><ext>" with another hash of the
// same length. During a deploy the old and new versions run side by side, so a
// page rendered by one can ask the other for its own entries.
func (a *Assets) current(name string) (string, bool) {
	for _, e := range a.entries {
		hash, ok := strings.CutPrefix(name, "assets/"+e.name+"-")
		if !ok {
			continue
		}
		hash, ok = strings.CutSuffix(hash, e.ext)
		if ok && len(hash) == len(e.hash) && !strings.ContainsAny(hash, "/.") {
			return e.file, true
		}
	}
	return "", false
}

// URL returns the URL of a static file, so it can be cached until it
// changes. A name Vite built (a manifest key: its source path under web/,
// e.g. "shared/fonts/newsreader.woff2") links its hashed file:
// "/static/assets/newsreader-b12n-WE3.woff2". Pages link their stylesheets
// and scripts with Tags instead.
// Any other name is a public file, copied as-is from the build's public
// directory (e.g. "img/logo.svg"), and gets its version:
// "/static/img/logo.svg?v=0123456789ab".
// An unknown name is an error, which fails the template that uses it.
func (a *Assets) URL(name string) (string, error) {
	if file, ok := a.manifest[name]; ok {
		return staticPrefix + file, nil
	}
	if v, ok := a.versions[name]; ok && !hashed(name) {
		return staticPrefix + name + "?v=" + v, nil
	}
	return "", fmt.Errorf("unknown static file %q", name)
}

// Serve serves the build's file at r's path under /static/, and reports
// whether there was one; when there wasn't, it writes nothing, so the caller
// answers with its own 404 page. Hashed files, and public files asked for with
// their current ?v=, are cached for a year; anything else must be
// revalidated, against the ETag (embedded files have no modification time to
// revalidate with). Directories and the manifest aren't served.
func (a *Assets) Serve(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, staticPrefix)
	version, ok := a.versions[name]
	immutable := ok && (hashed(name) || r.URL.Query().Get("v") == version)
	if !ok {
		// An outdated hashed entry gets the current one, which can't be
		// cached under the old name.
		if name, ok = a.current(name); !ok {
			return false
		}
		version = a.versions[name]
	}
	w.Header().Set("ETag", `"`+version+`"`)
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, a.fsys, name)
	return true
}
