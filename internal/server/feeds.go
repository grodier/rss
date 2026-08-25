package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/psql"
)

func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.services.FeedService.GetLatest()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Feeds []psql.Feed
	}{Feeds: feeds}

	if err := s.renderHTML(w, http.StatusOK, "feeds.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")
	if feedID == "" {
		// TODO: Fix this
		s.badRequestResponse(w, r, &MalformedRequest{Msg: "id is required"})
		return
	}

	feed, err := s.services.FeedService.GetByID(feedID)
	if err != nil {
		if errors.Is(err, psql.ErrNoRecord) {
			http.NotFound(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	data := struct {
		Name string
	}{Name: feed.Title}

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

func (s *Server) createFeedHandler(w http.ResponseWriter, r *http.Request) {
	feed := psql.Feed{
		Url:         "https://georgerodier.com/rss.xml",
		SiteUrl:     "https://georgerodier.com",
		Title:       "George Rodier",
		Description: "George Rodier's personal blog",
	}

	id, err := s.services.FeedService.Create(feed)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/feeds/%s", id), http.StatusSeeOther)
}
