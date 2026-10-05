package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.services.FeedService.GetLatest(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Feeds []rss.Feed
	}{Feeds: feeds}

	if err := s.renderHTML(w, http.StatusOK, "feeds.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")
	if !validator.Matches(feedID, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), feedID)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	site, err := s.services.SiteService.GetByID(r.Context(), feed.SiteID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	flash := s.sessionManager.PopString(r.Context(), "flash")

	data := struct {
		Feed  rss.Feed
		Site  rss.Site
		Flash string
	}{
		Feed:  feed,
		Site:  site,
		Flash: flash,
	}

	if err := s.renderHTML(w, http.StatusOK, "feed.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) subscribeFeedHandler(w http.ResponseWriter, r *http.Request) {
	data := map[string]string{
		"message": "Subscribed successfully",
	}

	if err := s.writeJSON(w, http.StatusCreated, data, nil); err != nil {
		s.serverErrorJSON(w, r, err)
	}
}
