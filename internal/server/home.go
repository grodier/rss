package server

import (
	"net/http"

	"github.com/grodier/rss/internal/rss"
)

// timelinePageSize is how many articles a timeline page shows.
const timelinePageSize = 30

func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticatedUserID(r)
	if !ok {
		if err := s.renderHTML(w, http.StatusOK, "home.html", nil); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
		return
	}

	items, err := s.services.ArticleService.ListTimeline(r.Context(), userID, before, timelinePageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	next := ""
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1].Article
		next = encodeCursor(rss.ArticleCursor{At: last.TimelineAt, ID: last.ID})
	}

	rows := make([]articleRow, len(items))
	for i, item := range items {
		rows[i] = articleRow{Article: item.Article, Feed: item.Feed}
	}

	data := struct {
		Rows   []articleRow
		Before bool
		Next   string
	}{Rows: rows, Before: !before.IsZero(), Next: next}

	if err := s.renderHTML(w, http.StatusOK, "timeline.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
