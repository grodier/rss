package server

import (
	"html/template"
	"net/http"

	"github.com/grodier/rss/internal/assets"
	"github.com/grodier/rss/web"
)

// static is the reader's built frontend, whose page groups are app/styles
// and app/entries (see package assets). A page names its group once,
// {{template "assets" "<group>"}} (partials/head.html).
var static = assets.MustLoad(web.App, "app")

// assetTags returns the tags that load a page group's stylesheets and
// scripts, for partials/head.html's "assets".
func assetTags(group string) (template.HTML, error) {
	return static.Tags(group)
}

// asset returns the URL of a static file: a name Vite built (e.g.
// "shared/fonts/newsreader.woff2"), or a public file from web/app/public
// (e.g. "img/logo.svg"). See assets.Assets.URL.
func asset(name string) (string, error) {
	return static.URL(name)
}

// staticHandler serves web.App under /static (see assets.Assets.Serve), with
// the app's 404 page for anything else.
func (s *Server) staticHandler(w http.ResponseWriter, r *http.Request) {
	if !static.Serve(w, r) {
		s.notFoundResponse(w, r)
	}
}
