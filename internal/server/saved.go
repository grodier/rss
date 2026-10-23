package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/grodier/rss/internal/rss"
)

// savedSearchMaxRunes caps the Saved page's search query; a longer one is cut,
// not rejected.
const savedSearchMaxRunes = 200

// savedHandler shows the user's saved articles, newest saved first, a page
// at a time, filtered by the search query q when there is one.
func (s *Server) savedHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if runes := []rune(q); len(runes) > savedSearchMaxRunes {
		q = strings.TrimSpace(string(runes[:savedSearchMaxRunes]))
	}

	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
		return
	}

	items, err := s.services.SaveService.ListSaved(r.Context(), userID, q, before, timelinePageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	next := ""
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1]
		next = savedURL(q, encodeCursor(rss.ArticleCursor{At: last.SavedAt, ID: last.Article.ID}))
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
		Q      string
		Rows   []articleRow
		Before bool
		Newest string
		Next   string
		// Search shows the search form: there are saves to search, or a
		// query to change. A later page implies saves.
		Search bool
	}{
		Nav:    s.nav(r, "saved"),
		Flash:  s.sessionManager.PopString(r.Context(), "flash"),
		Q:      q,
		Rows:   rows,
		Before: !before.IsZero(),
		Newest: savedURL(q, ""),
		Next:   next,
		Search: len(rows) > 0 || q != "" || !before.IsZero(),
	}

	if err := s.renderHTML(w, http.StatusOK, "saved.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// savedURL returns the Saved page's URL for the query q and the cursor
// before, leaving out whichever is empty.
func savedURL(q, before string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if before != "" {
		v.Set("before", before)
	}
	if len(v) == 0 {
		return "/saved"
	}
	return "/saved?" + v.Encode()
}
