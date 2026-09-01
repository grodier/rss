package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/psql"
	"github.com/grodier/rss/internal/validator"
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
	Url                 string `form:"url"`
	validator.Validator `form:"-"`
}

// TODO: move to discover, let discover call happen with zero value data
func (s *Server) createFeedHandler(w http.ResponseWriter, r *http.Request) {
	var form feedCreateForm
	err := s.decodePostForm(r, &form)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	form.CheckField(validator.NotBlank(form.Url), "url", "URL cannot be blank")

	if !form.Valid() {
		data := struct {
			Form any
		}{Form: form}

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
		Url:         form.Url,
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
