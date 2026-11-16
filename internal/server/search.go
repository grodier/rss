package server

import (
	"cmp"
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
	// Host is the page the box is on: "search", or "discover" (?ia=e).
	Host string
	// FormAction is the box's action, which live search fetches, and
	// Hidden its hidden fields, which only a submit sends.
	FormAction string
	Hidden     map[string]string
	// SubmitAction is where Enter goes, when not FormAction.
	SubmitAction string
	// Box is the search field's row (design checkpoint, temporary).
	Box searchBox
}

// Empty reports whether a search found nothing in any group.
func (d searchPageData) Empty() bool {
	return len(d.Feeds) == 0 && len(d.Saved) == 0 && len(d.Found.Sites) == 0
}

// Announce is what live search says when these results come in: how many
// results there are across the groups.
func (d searchPageData) Announce() string {
	if d.Options.Suggest {
		return countOf(len(d.Options.Shown), "feed", "feeds")
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
	s.searchPage(w, r, "search")
}

// searchPage renders Search's page on host: "search", or "discover" when
// Discover is the search place (?ia=e, temporary).
func (s *Server) searchPage(w http.ResponseWriter, r *http.Request, host string) {
	data := searchPageData{
		Nav:        s.nav(r, host),
		Form:       searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Layout:     searchLayout(r),
		Host:       host,
		FormAction: "/search",
	}
	if data.Layout != "full" {
		base, v := searchBase(r)
		data.FormAction = base + "?" + v.Encode()
		if host == "discover" && v.Get("ia") == "f" {
			// Typing stays on Discover; Enter goes to Search.
			data.FormAction, data.SubmitAction = "/discover?"+v.Encode(), "/search"
		}
		data.Hidden = map[string]string{"scope": cmp.Or(r.URL.Query().Get("scope"), "top")}
		for k := range v {
			data.Hidden[k] = v.Get(k)
		}
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
		siteLimit := perGroup
		if suggest {
			// Some found sites are all followed, and left out of New.
			perGroup, siteLimit = suggestFeeds, suggestSites*2
		}
		subs, err := s.services.SubscriptionService.Search(r.Context(), userID, q, perGroup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		var saved []rss.SavedArticle
		if !suggest {
			saved, err = s.services.SaveService.ListSaved(r.Context(), userID, q, rss.ArticleCursor{}, perGroup)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
		}
		found, err := s.searchSites(r, q, siteLimit)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
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
			// Whether the reader has saved anything decides the tabs
			// (temporary: a real build would count saves cheaply).
			any, err := s.services.SaveService.ListSaved(r.Context(), userID, "", rss.ArticleCursor{}, 1)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
			data.Options = s.newSearchOptions(r, data, len(any) > 0)
		}
	}

	data.Box = newSearchBox(r, data.Form.Q, "Search or paste a link", data.SubmitAction, true)

	if !data.Searched && data.Layout != "full" {
		data.Options.Recent = mockRecent(r)
	}

	if err := s.renderHTML(w, status, "search.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
