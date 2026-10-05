package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	site, err := s.services.SiteService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	feeds, err := s.services.FeedService.ListBySite(r.Context(), site.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Site  rss.Site
		Feeds []rss.Feed
		Flash string
	}{
		Site:  site,
		Feeds: feeds,
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}

	if err := s.renderHTML(w, http.StatusOK, "site.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
