package admin

import (
	"net/http"

	"github.com/grodier/rss/internal/assets"
	"github.com/grodier/rss/web"
)

// static is the admin's built frontend, whose page groups are admin/styles
// and admin/entries (see package assets). A page names its group once,
// {{template "assets" "<group>"}} (partials/head.html).
var static = assets.MustLoad(web.Admin, "admin")

// staticHandler serves web.Admin under /static (see assets.Assets.Serve),
// with the admin's 404 page for anything else.
func (s *Server) staticHandler(w http.ResponseWriter, r *http.Request) {
	if !static.Serve(w, r) {
		s.notFoundResponse(w, r)
	}
}
