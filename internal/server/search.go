package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/grodier/rss/internal/discovery"
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
	Nav      navData
	Form     searchForm
	Sites    []rss.SiteWithFeeds
	Searched bool
	Flash    string
	// LookupQ is the address to offer as a site lookup (see lookupAddress),
	// or "" if q doesn't look like a website address.
	LookupQ string
	// Subscribed holds the IDs of the result feeds the user follows.
	Subscribed map[string]bool
	// Icons holds the icon URLs of the result or Browse sites that have one.
	Icons map[string]string
	// Topics is the site catalog by topic, shown when there's no query
	// (Browse): only sites whose lookup has found them. Sites listed before
	// any topic come first, as a topic named "".
	Topics []rss.CatalogTopic
	// TopicIndex is whether Browse has named topics to link to.
	TopicIndex bool
}

// searchHandler searches the database only. GET requests are not covered by
// CrossOriginProtection, so GET handlers never fetch user-supplied URLs or make
// changes on the user's behalf; at most they do idempotent, bounded upkeep
// (see feedHandler's RequestRefresh).
func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
	data := searchData{
		Nav:   s.nav(r, "search"),
		Form:  searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}
	status := http.StatusOK

	data.Form.CheckField(validator.MaxChars(data.Form.Q, searchMaxChars), "q", "Search must be 200 characters or fewer")

	switch {
	case !data.Form.Valid():
		status = http.StatusUnprocessableEntity
	case data.Form.Q == "":
		topics, err := s.services.CatalogService.Browse(r.Context())
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Topics = topics

		var siteIDs []string
		for _, t := range topics {
			if t.Name != "" {
				data.TopicIndex = true
			}
			for _, site := range t.Sites {
				siteIDs = append(siteIDs, site.ID)
			}
		}
		if len(siteIDs) > 0 {
			data.Icons = s.pageIcons(r, siteIDs)
		}
	default:
		var siteKey string
		if u, err := discovery.ParseInput(data.Form.Q); err == nil {
			data.LookupQ = lookupAddress(u)
			siteKey = discovery.SiteKey(u)
		}
		sites, err := s.services.SearchService.Search(r.Context(), data.Form.Q, siteKey, searchLimit)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Sites = sites
		data.Searched = true

		var feedIDs, siteIDs []string
		for _, site := range sites {
			siteIDs = append(siteIDs, site.Site.ID)
			for _, f := range site.Feeds {
				feedIDs = append(feedIDs, f.ID)
			}
		}
		if len(feedIDs) > 0 {
			userID, _ := s.authenticatedUserID(r)
			data.Subscribed, err = s.services.SubscriptionService.SubscribedFeedIDs(r.Context(), userID, feedIDs)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
		}
		if len(siteIDs) > 0 {
			data.Icons = s.pageIcons(r, siteIDs)
		}
	}

	if err := s.renderHTML(w, status, "search.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// lookupAddress is u (from discovery.ParseInput) as the lookup offer shows and
// posts it: without "https://" and without a lone trailing "/", so
// "https://npr.org/" reads "npr.org". ParseInput turns it back into u.
func lookupAddress(u *url.URL) string {
	a := strings.TrimPrefix(u.String(), "https://")
	if u.Path == "/" && u.RawQuery == "" {
		a = strings.TrimSuffix(a, "/")
	}
	return a
}
