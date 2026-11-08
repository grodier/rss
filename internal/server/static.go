package server

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

	"github.com/grodier/rss/web"
)

// staticAssets describes the built frontend in web.App, read once at startup.
type staticAssets struct {
	// manifest maps each source path Vite built (the manifest's keys, e.g.
	// "shared/fonts/newsreader.woff2") to its built file under web.App
	// ("assets/newsreader-b12n-WE3.woff2").
	manifest map[string]string
	// versions maps every file /static serves, by its path in web.App, to the
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

// manifestEntry is the part of a .vite/manifest.json entry the server uses.
type manifestEntry struct {
	File    string `json:"file"`
	Name    string `json:"name"`
	IsEntry bool   `json:"isEntry"`
	// Imports are the manifest keys of the chunks a script imports.
	Imports []string `json:"imports"`
	// CSS are the built stylesheets a script imports.
	CSS []string `json:"css"`
}

// Page groups. A page names its group once, {{template "assets" "<group>"}}
// (partials/head.html), and loads that group's files: baseSheet, then the
// group's own sheet, app/styles/<group>.css, and its entry,
// app/entries/<group>.ts, each if it exists (a group has at least one). An
// entry can import another group's sheet, which Vite lists as its CSS.
const (
	baseSheet    = "app/styles/base.css"
	sheetPrefix  = "app/styles/"
	entryPrefix  = "app/entries/"
	sheetExt     = ".css"
	entryExt     = ".ts"
	staticPrefix = "/static/"
)

// static is the built frontend. A missing or invalid build panics: `make
// assets` builds it, and without one the embed in package web doesn't compile.
var static = mustLoadStatic(web.App)

func mustLoadStatic(fsys fs.FS) *staticAssets {
	s, err := loadStatic(fsys)
	if err != nil {
		panic(fmt.Sprintf("load static files: %v", err))
	}
	return s
}

// readManifest reads the build's .vite/manifest.json, keyed by source path
// (or, for a chunk, "_" and its name).
func readManifest(fsys fs.FS) (map[string]manifestEntry, error) {
	b, err := fs.ReadFile(fsys, ".vite/manifest.json")
	if err != nil {
		return nil, err
	}
	var entries map[string]manifestEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse .vite/manifest.json: %w", err)
	}
	return entries, nil
}

func loadStatic(fsys fs.FS) (*staticAssets, error) {
	entries, err := readManifest(fsys)
	if err != nil {
		return nil, err
	}

	s := &staticAssets{manifest: make(map[string]string), versions: make(map[string]string), groups: make(map[string]template.HTML)}
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
		s.versions[p] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	if err != nil {
		return nil, err
	}

	for src, e := range entries {
		if _, ok := s.versions[e.File]; !ok {
			return nil, fmt.Errorf(".vite/manifest.json: %s names a missing file %q", src, e.File)
		}
		s.manifest[src] = e.File
		if !e.IsEntry {
			continue
		}
		ext := path.Ext(e.File)
		hash, ok := strings.CutPrefix(strings.TrimSuffix(e.File, ext), "assets/"+e.Name+"-")
		if !ok || hash == "" {
			return nil, fmt.Errorf(".vite/manifest.json: %s: %q isn't named assets/%s-<hash>%s", src, e.File, e.Name, ext)
		}
		s.entries = append(s.entries, hashedName{file: e.File, name: e.Name, hash: hash, ext: ext})
	}

	for src := range entries {
		group, ok := pageGroup(src)
		if !ok || s.groups[group] != "" {
			continue
		}
		tags, err := groupTags(entries, group)
		if err != nil {
			return nil, err
		}
		s.groups[group] = tags
	}
	return s, nil
}

// pageGroup returns the group whose sheet or entry src (a manifest key) is.
// base.css is every group's, so no group's own.
func pageGroup(src string) (string, bool) {
	if src == baseSheet {
		return "", false
	}
	if name, ok := strings.CutPrefix(src, sheetPrefix); ok {
		name, ok = strings.CutSuffix(name, sheetExt)
		return name, ok && !strings.Contains(name, "/")
	}
	if name, ok := strings.CutPrefix(src, entryPrefix); ok {
		name, ok = strings.CutSuffix(name, entryExt)
		return name, ok && !strings.Contains(name, "/")
	}
	return "", false
}

// groupTags returns the tags that load group's files, from the manifest, in
// the order the head needs them:
//
//  1. a stylesheet link for baseSheet, then for the group's sheet, then for
//     the CSS the entry and the chunks it imports list (recursively);
//  2. a modulepreload link for each chunk the entry imports, recursively, so
//     they download alongside it rather than after it;
//  3. the entry's module script.
//
// Each file is linked once. An unknown group is an error.
func groupTags(m map[string]manifestEntry, group string) (template.HTML, error) {
	sheet, hasSheet := m[sheetPrefix+group+sheetExt]
	entry, hasEntry := m[entryPrefix+group+entryExt]
	if !hasSheet && !hasEntry {
		return "", fmt.Errorf("unknown page group %q: no %s%s%s or %s%s%s", group, sheetPrefix, group, sheetExt, entryPrefix, group, entryExt)
	}
	base, ok := m[baseSheet]
	if !ok {
		return "", fmt.Errorf(".vite/manifest.json has no %s", baseSheet)
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
		var walk func(src string, e manifestEntry) error
		walk = func(src string, e manifestEntry) error {
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
		if err := walk(entryPrefix+group+entryExt, entry); err != nil {
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

// assetTags returns the tags that load a page group's stylesheets and
// scripts (see groupTags), for partials/head.html's "assets". An unknown
// group is an error, which fails the template that names it.
func assetTags(group string) (template.HTML, error) {
	if tags, ok := static.groups[group]; ok {
		return tags, nil
	}
	return "", fmt.Errorf("unknown page group %q", group)
}

// hashed reports whether name (a path in web.App) is a built file whose name
// carries its hash, so it never changes.
func hashed(name string) bool {
	return strings.HasPrefix(name, "assets/")
}

// current returns the built entry that name, a missing file, is an outdated
// version of: the same "assets/<name>-<hash><ext>" with another hash of the
// same length. During a deploy the old and new versions run side by side, so a
// page rendered by one can ask the other for its own entries.
func (s *staticAssets) current(name string) (string, bool) {
	for _, e := range s.entries {
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

// asset returns the URL of a static file, so it can be cached until it
// changes. A name Vite built (a manifest key: its source path under web/,
// e.g. "shared/fonts/newsreader.woff2") links its hashed file:
// "/static/assets/newsreader-b12n-WE3.woff2". Pages link their stylesheets
// and scripts with assetTags instead.
// Any other name is a public file, copied as-is from web/app/public (e.g.
// "img/logo.svg"), and gets its version: "/static/img/logo.svg?v=0123456789ab".
// An unknown name is an error, which fails the template that uses it.
func asset(name string) (string, error) {
	if file, ok := static.manifest[name]; ok {
		return staticPrefix + file, nil
	}
	if v, ok := static.versions[name]; ok && !hashed(name) {
		return staticPrefix + name + "?v=" + v, nil
	}
	return "", fmt.Errorf("unknown static file %q", name)
}

// staticHandler serves web.App under /static. Hashed files, and public files
// asked for with their current ?v=, are cached for a year; anything else must
// be revalidated, against the ETag (embedded files have no modification time
// to revalidate with). Directories and the manifest are 404s.
func (s *Server) staticHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	version, ok := static.versions[name]
	immutable := ok && (hashed(name) || r.URL.Query().Get("v") == version)
	if !ok {
		// An outdated hashed entry gets the current one, which can't be
		// cached under the old name.
		if name, ok = static.current(name); !ok {
			s.notFoundResponse(w, r)
			return
		}
		version = static.versions[name]
	}
	w.Header().Set("ETag", `"`+version+`"`)
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, web.App, name)
}
