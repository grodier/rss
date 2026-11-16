package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// searchPerGroup is how many results each of Search's groups shows.
const searchPerGroup = 3

type searchPageData struct {
	Nav      navData
	Form     searchForm
	Searched bool
	// Feeds are the reader's subscriptions whose name matches (Your feeds).
	Feeds []myFeedRow
	// Saved are the reader's saved articles that match (Saved).
	Saved []rss.SavedArticle
	// Found are the sites that match (New sites), and the lookup offer.
	Found foundSites

	// Layout and Options are the design checkpoint's (#645): how the
	// results unfold, from ?layout= (search_options.go). Temporary.
	Layout  string
	Options searchOptions
}

// Empty reports whether a search found nothing in any group.
func (d searchPageData) Empty() bool {
	return len(d.Feeds) == 0 && len(d.Saved) == 0 && len(d.Found.Sites) == 0
}

// Announce is what live search says when these results come in: how many
// results there are across the groups.
func (d searchPageData) Announce() string {
	if d.Options.Suggest {
		switch n := len(d.Options.Shown); n {
		case 0:
			return "None of your feeds"
		default:
			return fmt.Sprintf("%d of your feeds", n)
		}
	}
	if d.Layout != "full" {
		return countOf(len(d.Options.Shown), "result", "results")
	}
	return countOf(len(d.Feeds)+len(d.Saved)+len(d.Found.Sites), "result", "results")
}

// searchHandler is the whole app's search: the reader's feeds, their saved
// articles and new sites, the first searchPerGroup of each. Like
// discoverHandler it only reads.
func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
	data := searchPageData{
		Nav:    s.nav(r, "search"),
		Form:   searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Layout: searchLayout(r),
	}
	perGroup := searchPerGroup
	if data.Layout != "full" {
		perGroup = searchOptionsLimit + 1
	}
	status := http.StatusOK

	data.Form.CheckField(validator.MaxChars(data.Form.Q, searchMaxChars), "q", "Search must be 200 characters or fewer")

	switch {
	case !data.Form.Valid():
		status = http.StatusUnprocessableEntity
	case data.Form.Q != "":
		userID, _ := s.authenticatedUserID(r)
		q := data.Form.Q

		// While typing, the type-ahead option reads only the reader's feeds
		// (design checkpoint, temporary).
		suggest := suggesting(r, data.Layout)
		if suggest {
			perGroup = suggestLimit
		}
		subs, err := s.services.SubscriptionService.Search(r.Context(), userID, q, perGroup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		var saved []rss.SavedArticle
		var found foundSites
		if suggest {
			found, _ = s.addressOffer(q)
		} else {
			saved, err = s.services.SaveService.ListSaved(r.Context(), userID, q, rss.ArticleCursor{}, perGroup)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
			found, err = s.searchSites(r, q, perGroup)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
		}

		siteIDs := make([]string, len(subs))
		for i, sub := range subs {
			siteIDs[i] = sub.Site.ID
		}
		icons := s.pageIcons(r, siteIDs)
		data.Feeds = make([]myFeedRow, len(subs))
		for i, sub := range subs {
			data.Feeds[i] = newMyFeedRow(sub, icons[sub.Site.ID], false, r.URL.RequestURI())
		}
		data.Saved = saved
		data.Found = found
		data.Searched = true
		if data.Layout != "full" {
			data.Options = s.newSearchOptions(r, data)
		}
	}

	if !data.Searched {
		data.Options.Recent = mockRecent(r, data.Layout)
	}

	if err := s.renderHTML(w, status, "search.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
