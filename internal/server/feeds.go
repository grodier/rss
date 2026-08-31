package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

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

type feedCreateForm struct {
	Url         string
	FieldErrors map[string]string
}

// TODO: move to discover, let discover call happen with zero value data
func (s *Server) createFeedHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	parsedForm := feedCreateForm{
		Url:         r.FormValue("url"),
		FieldErrors: map[string]string{},
	}

	if strings.TrimSpace(parsedForm.Url) == "" {
		parsedForm.FieldErrors["url"] = "URL is required"
	}

	if len(parsedForm.FieldErrors) > 0 {
		data := struct {
			Form any
		}{Form: parsedForm}

		if err := s.renderHTML(w, http.StatusUnprocessableEntity, "discover.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		return
	}

	// feed := psql.Feed{
	// 	Url:         "https://georgerodier.com/rss.xml",
	// 	SiteUrl:     "https://georgerodier.com",
	// 	Title:       "George Rodier",
	// 	Description: "George Rodier's personal blog",
	// }

	feed := psql.Feed{
		Url:         parsedForm.Url,
		SiteUrl:     "",
		Title:       "",
		Description: "",
	}

	id, err := s.services.FeedService.Create(feed)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/feeds/%s", id), http.StatusSeeOther)
}
