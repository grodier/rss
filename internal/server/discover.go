package server

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

const (
	searchMaxChars = 200
	// discoverPerSection is how many feeds each of Discover's sections shows.
	discoverPerSection = 3
	// importOfferBelow is how many feeds a reader who has never imported
	// follows before Discover stops offering import at the top.
	importOfferBelow = 10
)

// appLinkMessage is shown in place of a lookup for an address on the app
// itself.
const appLinkMessage = "That's a Paper Round link. To follow a shared timeline, paste its link into another feed reader."

type searchForm struct {
	Q                   string
	validator.Validator `form:"-"`
}

type discoverData struct {
	Nav      navData
	Form     searchForm
	Searched bool
	// Results are the search's, with a query and ?scope=, which only a
	// submit sends (discover_search.go).
	Results discoverResults
	// Suggesting says the page has a query but no ?scope=: live search's
	// fetch as the reader types, which gets Suggestions instead (#666).
	Suggesting  bool
	Suggestions discoverSuggestions
	Flash       string
	// Icons holds the icon URLs of the sections' sites that have one.
	Icons map[string]string
	// Sections is Discover's catalog as this reader sees it, shown when
	// there's no query; empty when the catalog has nothing for them.
	Sections []rss.DiscoverSection
	// Reordered says Sections aren't in the default order: the reader's
	// follows or waitlist answer moved one, and a line under the chips says
	// why.
	Reordered bool
	// Import is what Discover says about importing, with no query.
	Import discoverImport
	Box    searchBox
}

// Scope is the form's hidden scope field, which only a submit sends: the
// tab shown, so editing the query and pressing Enter keeps it, else Top.
func (d discoverData) Scope() string {
	if d.Searched {
		return d.Results.Scope
	}
	return "top"
}

// discoverImport is what Discover's page with no query says about importing
// (see discoverImportFor): at most one of its fields is set.
type discoverImport struct {
	// Offer is the import card at the top, for a reader who has never
	// imported and follows fewer than importOfferBelow feeds.
	Offer bool
	// Running is the progress of the reader's latest import while it has
	// feeds pending or running, at the top in place of Offer.
	Running *importProgress
	// Line is the one-line link at the end of the page, otherwise.
	Line bool
}

// importProgress is an import's progress, as the card on Discover shows it.
type importProgress struct {
	ID       string
	Filename string
	Done     int
	Total    int
}

// discoverImportFor decides what Discover offers userID about importing,
// from what the account has done, so it needs no stored state: the progress
// of an import still running, else the card if they have never imported and
// follow fewer than importOfferBelow feeds, else the line.
func (s *Server) discoverImportFor(ctx context.Context, userID string) (discoverImport, error) {
	imports, err := s.services.ImportService.ListByUser(ctx, userID, 1)
	if err != nil {
		return discoverImport{}, err
	}
	if len(imports) > 0 {
		latest := imports[0]
		if latest.Pending > 0 {
			return discoverImport{Running: &importProgress{
				ID:       latest.Import.ID,
				Filename: latest.Import.Filename,
				Done:     latest.Total - latest.Pending,
				Total:    latest.Total,
			}}, nil
		}
		return discoverImport{Line: true}, nil
	}
	n, err := s.services.SubscriptionService.Count(ctx, userID)
	if err != nil {
		return discoverImport{}, err
	}
	if n < importOfferBelow {
		return discoverImport{Offer: true}, nil
	}
	return discoverImport{Line: true}, nil
}

// inDefaultOrder reports whether sections are in Discover's default order,
// by position. Sections sharing a position count as in order either way:
// the database orders their names by its collation, not Go's.
func inDefaultOrder(sections []rss.DiscoverSection) bool {
	return slices.IsSortedFunc(sections, func(a, b rss.DiscoverSection) int {
		return cmp.Compare(a.Position, b.Position)
	})
}

// discoverHandler searches the database only. GET requests are not covered by
// CrossOriginProtection, so GET handlers never fetch user-supplied URLs or make
// changes on the user's behalf; at most they do idempotent, bounded upkeep
// (see feedHandler's RequestRefresh).
func (s *Server) discoverHandler(w http.ResponseWriter, r *http.Request) {
	data := discoverData{
		Nav:   s.nav(r, "discover"),
		Form:  searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
	}
	status := http.StatusOK

	data.Form.CheckField(validator.MaxChars(data.Form.Q, searchMaxChars), "q", "Search must be 200 characters or fewer")

	switch {
	case !data.Form.Valid():
		status = http.StatusUnprocessableEntity
	case data.Form.Q == "":
		userID, _ := s.authenticatedUserID(r)
		sections, err := s.services.DiscoverService.Sections(r.Context(), userID, discoverPerSection)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Sections = sections
		data.Reordered = !inDefaultOrder(sections)
		data.Import, err = s.discoverImportFor(r.Context(), userID)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		var siteIDs []string
		for _, sec := range sections {
			for _, f := range sec.Feeds {
				siteIDs = append(siteIDs, f.Site.ID)
			}
		}
		if len(siteIDs) > 0 {
			data.Icons = s.pageIcons(r, siteIDs)
		}
	case !r.URL.Query().Has("scope"):
		suggestions, err := s.discoverSuggest(r, data.Form.Q)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Suggestions = suggestions
		data.Suggesting = true
	default:
		results, err := s.discoverSearch(r, data.Form.Q)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Results = results
		data.Searched = true
	}

	data.Box = discoverBox(data.Form)
	if err := s.renderHTML(w, status, "discover.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// discoverBox is Discover's search box for form: its query and error.
func discoverBox(form searchForm) searchBox {
	return searchBox{
		ID:          "q",
		Label:       "Search",
		Placeholder: "Search or paste a link",
		Value:       form.Q,
		ClearURL:    "/discover",
		InPlace:     true,
		Error:       form.FieldErrors["q"],
	}
}

// foundSites is a site search's results.
type foundSites struct {
	Sites []rss.SiteWithFeeds
	// Groups are the sites on shared hosts counted rather than listed, one
	// row each (groupResult).
	Groups []rss.SiteGroup
	// LookupQ is the address to offer as a site lookup (see lookupAddress),
	// or "" if q doesn't look like a website address or the search already
	// found what a lookup of it would (see knownAddress).
	LookupQ string
	// AppLink is set instead of LookupQ when q is an address on the app
	// itself, such as a shared timeline's link, which is never looked up.
	AppLink bool
	// Subscribed holds the IDs of the result feeds the user follows.
	Subscribed map[string]bool
}

// searchSites finds up to limit sites for q, a valid, non-empty query, and
// the groups of the rest on shared hosts (see psql's Search); with host
// set, only the sites on that host, none grouped. It finds them
// with which of their feeds the user follows, and what to offer when q is
// an address.
func (s *Server) searchSites(r *http.Request, q, host string, limit int) (foundSites, error) {
	var found foundSites
	var siteKey string
	u, _ := discovery.ParseInput(q) // nil when q isn't an address
	if u != nil {
		siteKey = discovery.SiteKey(u)
	}
	sites, groups, err := s.services.SearchService.Search(r.Context(), q, siteKey, host, limit)
	if err != nil {
		return foundSites{}, err
	}
	found.Sites, found.Groups = sites, groups
	if u != nil {
		if s.isAppLink(u) {
			found.AppLink = true
		} else {
			homeChecked, err := s.homePageChecked(r, u, sites)
			if err != nil {
				return foundSites{}, err
			}
			if !knownAddress(u, sites, homeChecked) {
				found.LookupQ = lookupAddress(u)
			}
		}
	}

	var feedIDs []string
	for _, site := range sites {
		for _, f := range site.Feeds {
			feedIDs = append(feedIDs, f.ID)
		}
	}
	if len(feedIDs) > 0 {
		userID, _ := s.authenticatedUserID(r)
		found.Subscribed, err = s.services.SubscriptionService.SubscribedFeedIDs(r.Context(), userID, feedIDs)
		if err != nil {
			return foundSites{}, err
		}
	}
	return found, nil
}

// homePageChecked reports whether u is the home page of one of sites whose
// lookup has finished on its home page. A site found from another address
// (a feed, or a page on another host such as feeds.npr.org) hasn't had its
// home page's feeds looked for.
func (s *Server) homePageChecked(r *http.Request, u *url.URL, sites []rss.SiteWithFeeds) (bool, error) {
	key := discovery.SiteKey(u)
	if !discovery.IsHomePage(u) || !slices.ContainsFunc(sites, func(site rss.SiteWithFeeds) bool { return site.Site.Host == key }) {
		return false, nil
	}
	l, err := s.services.LookupService.GetBySiteKey(r.Context(), key)
	if errors.Is(err, rss.ErrNoRecord) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return l.Status == rss.LookupDone && l.HomePage, nil
}

// knownAddress reports whether looking up u (from discovery.ParseInput)
// would find nothing that sites, a search's results, don't already show:
// u is a found site's home page and that has been looked up (homeChecked,
// see homePageChecked), or u is one of a found site's feeds. Any other
// address on a found site, such as a feed its home page doesn't link to, is
// offered as a lookup, which adds what it finds to the site
// (LookupStore.Request re-runs a finished lookup for a different address).
func knownAddress(u *url.URL, sites []rss.SiteWithFeeds, homeChecked bool) bool {
	if homeChecked {
		return true
	}
	for _, site := range sites {
		for _, f := range site.Feeds {
			if sameAddress(u, f.Url) {
				return true
			}
		}
	}
	return false
}

// sameAddress reports whether u and feedURL, a feed's stored address, are the
// same page but for the scheme and a leading "www.", which a reader leaves
// out when pasting ("example.com/feed" is https) and SiteKey ignores.
func sameAddress(u *url.URL, feedURL string) bool {
	f, err := url.Parse(feedURL)
	if err != nil {
		return false
	}
	return discovery.KeyHost(discovery.SiteKey(f)) == discovery.KeyHost(discovery.SiteKey(u)) && f.Port() == u.Port() &&
		f.EscapedPath() == u.EscapedPath() && f.RawQuery == u.RawQuery
}

// isAppLink reports whether u (from discovery.ParseInput) is on the app's own
// site, with a path ("paperround.app/@x") or without, which is never looked
// up.
func (s *Server) isAppLink(u *url.URL) bool {
	return s.appSiteKey != "" && discovery.KeyHost(discovery.SiteKey(u)) == s.appSiteKey
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

// sectionPageSize is how many articles a section's Recent posts shows.
const sectionPageSize = feedPageSize

// discoverRow is a catalog feed's row (partials/discover_feed.html), on
// Discover and a section's page.
type discoverRow struct {
	Feed     rss.DiscoverFeed
	Icon     string // its site's icon URL, or "" for its letter
	ReturnTo string // where Follow sends the reader back to, without the row's anchor
}

func newDiscoverRow(f rss.DiscoverFeed, icon, returnTo string) discoverRow {
	return discoverRow{Feed: f, Icon: icon, ReturnTo: returnTo}
}

// followButton is Follow (partials/discover_feed.html's follow-form): in an
// article row's foot, on a section's Recent posts, for a feed the reader
// doesn't follow, and on a site's page for each of its feeds.
type followButton struct {
	FeedID   string
	Label    string // rss.DiscoverFeed.Label
	ReturnTo string
	// Following renders it pressed, as "Following", which unfollows.
	Following bool
}

type discoverSectionData struct {
	Nav     navData
	Section rss.Section
	Posts   bool // the Recent posts tab, else Feeds
	Flash   string
	Icons   map[string]string
	// Feeds is the Feeds tab's list.
	Feeds []rss.DiscoverFeed
	// Rows, Before and Next are the Recent posts tab's, as on a feed page.
	Rows   []articleRow
	Before bool
	Next   string
}

// discoverSectionHandler shows a section's page with its Feeds tab: every
// catalog feed in the section, followed or not.
func (s *Server) discoverSectionHandler(w http.ResponseWriter, r *http.Request) {
	data, ok := s.discoverSectionPage(w, r)
	if !ok {
		return
	}
	userID, _ := s.authenticatedUserID(r)
	feeds, err := s.services.DiscoverService.SectionFeeds(r.Context(), userID, data.Section.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data.Feeds = feeds
	siteIDs := make([]string, len(feeds))
	for i, f := range feeds {
		siteIDs[i] = f.Site.ID
	}
	data.Icons = s.pageIcons(r, siteIDs)

	if err := s.renderHTML(w, http.StatusOK, "discover_section.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// discoverSectionPostsHandler shows a section's page with its Recent posts
// tab: its catalog feeds' articles, newest first, paged like a feed page.
func (s *Server) discoverSectionPostsHandler(w http.ResponseWriter, r *http.Request) {
	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older posts isn't valid.")
		return
	}
	data, ok := s.discoverSectionPage(w, r)
	if !ok {
		return
	}
	data.Posts = true

	userID, _ := s.authenticatedUserID(r)
	posts, err := s.services.DiscoverService.SectionPosts(r.Context(), userID, data.Section.ID, before, sectionPageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if len(posts) > sectionPageSize {
		posts = posts[:sectionPageSize]
		last := posts[len(posts)-1].Article
		data.Next = encodeCursor(rss.ArticleCursor{At: last.FeedSortAt(), ID: last.ID})
	}

	rows := make([]articleRow, len(posts))
	siteIDs := make([]string, len(posts))
	for i, p := range posts {
		feed := p.Feed.Feed
		feed.Title = p.Feed.Label() // the site's name with the feed's, as on the Feeds tab
		rows[i] = articleRow{Article: p.Article, Feed: feed, ReturnTo: r.URL.RequestURI()}
		if !p.Feed.Following {
			// Back to the row, as on the Feeds tab, so following without
			// JavaScript doesn't lose the reader's place.
			rows[i].Follow = &followButton{FeedID: feed.ID, Label: p.Feed.Label(), ReturnTo: r.URL.RequestURI() + "#article-" + p.Article.ID}
		}
		siteIDs[i] = p.Feed.Site.ID
	}
	data.Icons = s.pageIcons(r, siteIDs)
	for i := range rows {
		rows[i].Icon = data.Icons[rows[i].Feed.SiteID]
	}
	if err := s.fillRead(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if err := s.fillSaved(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if err := s.fillStarred(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.fillDebug(r, rows)
	if before.IsZero() {
		markFirstImageEager(rows)
	}
	data.Rows = rows
	data.Before = !before.IsZero()

	if err := s.renderHTML(w, http.StatusOK, "discover_section.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// discoverSectionPage reads what both of a section's tabs show: the
// section named by the URL's slug, and the flash. If ok is false it has
// already written the response.
func (s *Server) discoverSectionPage(w http.ResponseWriter, r *http.Request) (data discoverSectionData, ok bool) {
	sec, err := s.services.DiscoverService.Section(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return discoverSectionData{}, false
	}
	return discoverSectionData{
		Nav:     s.nav(r, "discover"),
		Section: sec,
		Flash:   s.sessionManager.PopString(r.Context(), "flash"),
	}, true
}
