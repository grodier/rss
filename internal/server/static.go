package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/grodier/rss/web"
)

// staticAssets describes the built frontend in web.App, read once at startup.
type staticAssets struct {
	// manifest maps each source path Vite built (the manifest's keys, e.g.
	// "app/app.js") to its built file under web.App ("assets/app-B2x9kQ1z.js").
	manifest map[string]string
	// versions maps every file /static serves, by its path in web.App, to the
	// first 12 hex digits of its content's SHA-256: its ETag, and the ?v= of a
	// public file.
	versions map[string]string
	// entries are the built entries (scripts and stylesheets), the files an
	// outdated hashed name falls back to (see current).
	entries []hashedName
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
}

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

func loadStatic(fsys fs.FS) (*staticAssets, error) {
	b, err := fs.ReadFile(fsys, ".vite/manifest.json")
	if err != nil {
		return nil, err
	}
	var entries map[string]manifestEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse .vite/manifest.json: %w", err)
	}

	s := &staticAssets{manifest: make(map[string]string), versions: make(map[string]string)}
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
	return s, nil
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
// e.g. "app/app.js") links its hashed file: "/static/assets/app-B2x9kQ1z.js".
// Any other name is a public file, copied as-is from web/app/public (e.g.
// "img/logo.svg"), and gets its version: "/static/img/logo.svg?v=0123456789ab".
// An unknown name is an error, which fails the template that uses it.
func asset(name string) (string, error) {
	if file, ok := static.manifest[name]; ok {
		return "/static/" + file, nil
	}
	if v, ok := static.versions[name]; ok && !hashed(name) {
		return "/static/" + name + "?v=" + v, nil
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
