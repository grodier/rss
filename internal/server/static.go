package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/grodier/rss/internal/ui"
)

// staticVersions maps each file under ui.Static's static/ directory, by its
// path relative to static/ (e.g. "css/main.css"), to its version: the first
// 12 hex digits of the SHA-256 of the content it's served with.
// staticRewritten holds that content for the stylesheets, whose relative
// url()s are rewritten to versioned URLs (see mustHashStatic); other files are
// served as embedded.
var staticVersions, staticRewritten = mustHashStatic()

// cssURL matches a url() in a stylesheet: its optional quote and its target.
var cssURL = regexp.MustCompile(`url\(\s*(["']?)([^"')\s]+)(["']?)\s*\)`)

// mustHashStatic versions every static file. Stylesheets are hashed after the
// other files, with each relative url() rewritten to the versioned URL of the
// file it points to, so they can be cached like the files they link (fonts),
// and a changed font changes its stylesheet's version too. A url() naming a
// file that isn't under static/ panics, like any other failure here.
func mustHashStatic() (map[string]string, map[string][]byte) {
	versions := make(map[string]string)
	rewritten := make(map[string][]byte)
	var stylesheets []string
	err := fs.WalkDir(ui.Static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(p, "static/")
		if path.Ext(name) == ".css" {
			stylesheets = append(stylesheets, name)
			return nil
		}
		b, err := fs.ReadFile(ui.Static, p)
		if err != nil {
			return err
		}
		versions[name] = hashVersion(b)
		return nil
	})
	if err != nil {
		panic(fmt.Sprintf("hash static files: %v", err))
	}
	for _, name := range stylesheets {
		b, err := fs.ReadFile(ui.Static, "static/"+name)
		if err != nil {
			panic(fmt.Sprintf("hash static files: %v", err))
		}
		b, err = versionStylesheetURLs(name, b, versions)
		if err != nil {
			panic(fmt.Sprintf("hash static files: %v", err))
		}
		rewritten[name] = b
		versions[name] = hashVersion(b)
	}
	return versions, rewritten
}

// versionStylesheetURLs rewrites each relative url() in the stylesheet name
// (relative to static/) to "url("<target>?v=<version>")", resolved against
// the stylesheet's directory. data: and absolute URLs are left alone.
func versionStylesheetURLs(name string, css []byte, versions map[string]string) ([]byte, error) {
	var bad error
	out := cssURL.ReplaceAllFunc(css, func(m []byte) []byte {
		sub := cssURL.FindSubmatch(m)
		ref := string(sub[2])
		if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "#") || strings.Contains(ref, ":") {
			return m
		}
		target := path.Join(path.Dir(name), ref)
		v, ok := versions[target]
		if !ok {
			if bad == nil {
				bad = fmt.Errorf("%s: url(%q) names no static file", name, ref)
			}
			return m
		}
		return []byte(`url("` + ref + "?v=" + v + `")`)
	})
	return out, bad
}

func hashVersion(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// asset returns the URL of the static file name (relative to static/, e.g.
// "css/main.css") with its version, so it can be cached until it changes:
// "/static/css/main.css?v=0123456789ab". An unknown name is an error, which
// fails the template that uses it.
func asset(name string) (string, error) {
	v, ok := staticVersions[name]
	if !ok {
		return "", fmt.Errorf("unknown static file %q", name)
	}
	return "/static/" + name + "?v=" + v, nil
}
