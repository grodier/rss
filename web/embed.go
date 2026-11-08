// Package web holds the built frontend: what `npm run build` writes to dist/.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist/app
var app embed.FS

// App is the reader's built assets, at the paths /static serves them under
// (dist/app's contents).
var App fs.FS = mustSub(app, "dist/app")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
