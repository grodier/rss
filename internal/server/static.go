package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"

	"github.com/grodier/rss/internal/ui"
)

// staticVersions maps each file under ui.Static's static/ directory, by its
// path relative to static/ (e.g. "css/main.css"), to its version: the first
// 12 hex digits of the SHA-256 of its content.
var staticVersions = mustHashStatic()

func mustHashStatic() map[string]string {
	versions := make(map[string]string)
	err := fs.WalkDir(ui.Static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(ui.Static, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		versions[strings.TrimPrefix(path, "static/")] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	if err != nil {
		panic(fmt.Sprintf("hash static files: %v", err))
	}
	return versions
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
