// Package web holds the built frontend: what `npm run build` writes to dist/,
// the reader's build in dist/app and the admin's in dist/admin.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist/app
var app embed.FS

//go:embed all:dist/admin
var admin embed.FS

// App is the reader's built assets, at the paths /static serves them under
// (dist/app's contents).
var App fs.FS = mustSub(app, "dist/app")

// Admin is the admin's built assets, at the paths its /static serves them
// under (dist/admin's contents).
var Admin fs.FS = mustSub(admin, "dist/admin")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
