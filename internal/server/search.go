package server

import (
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

const (
	searchMaxChars = 200
	searchLimit    = 20
)

type searchForm struct {
	Q                   string
	validator.Validator `form:"-"`
}

type searchData struct {
	Form     searchForm
	Sites    []rss.SiteWithFeeds
	Searched bool
	Flash    string
}

// searchHandler searches the database only. It never fetches or writes, since
// GET requests are not covered by CrossOriginProtection.
func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
	data := searchData{
		Form:  searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}
	status := http.StatusOK

	data.Form.CheckField(validator.MaxChars(data.Form.Q, searchMaxChars), "q", "Search must be 200 characters or fewer")

	switch {
	case !data.Form.Valid():
		status = http.StatusUnprocessableEntity
	case data.Form.Q != "":
		sites, err := s.services.SearchService.Search(r.Context(), data.Form.Q, searchLimit)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Sites = sites
		data.Searched = true
	}

	if err := s.renderHTML(w, status, "search.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
