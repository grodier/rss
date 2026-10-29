package server

import (
	"net/http"

	"github.com/grodier/rss/internal/ui"
)

// manifestHandler serves the web app manifest, which lets browsers install the
// app. It's public, like /static: browsers fetch it without cookies.
func (s *Server) manifestHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	// Browsers check an installed app's manifest for updates on their own
	// schedule, so a day is plenty.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write(ui.Manifest); err != nil {
		s.logError(r, err)
	}
}
