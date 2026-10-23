package server

import (
	"net/http"

	"github.com/grodier/rss/internal/rss"
)

// savedHandler shows the user's saved articles, newest saved first, a page
// at a time.
func (s *Server) savedHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)

	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
		return
	}

	items, err := s.services.SaveService.ListSaved(r.Context(), userID, before, timelinePageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	next := ""
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1]
		next = encodeCursor(rss.ArticleCursor{At: last.SavedAt, ID: last.Article.ID})
	}

	rows := make([]articleRow, len(items))
	for i, item := range items {
		rows[i] = articleRow{Article: item.Article, Feed: item.Feed, Saved: true, ReturnTo: r.URL.RequestURI()}
	}
	if err := s.fillRead(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.fillIcons(r, rows)

	data := struct {
		Nav    navData
		Flash  string
		Rows   []articleRow
		Before bool
		Next   string
	}{
		Nav:    s.nav(r, "saved"),
		Flash:  s.sessionManager.PopString(r.Context(), "flash"),
		Rows:   rows,
		Before: !before.IsZero(),
		Next:   next,
	}

	if err := s.renderHTML(w, http.StatusOK, "saved.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
