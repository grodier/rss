package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

const (
	searchMaxChars = 200
	searchLimit    = 20
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
	Sites    []rss.SiteWithFeeds
	Searched bool
	Flash    string
	// LookupQ is the address to offer as a site lookup (see lookupAddress),
	// or "" if q doesn't look like a website address.
	LookupQ string
	// AppLink is set instead of LookupQ when q is an address on the app
	// itself, such as a shared timeline's link, which is never looked up.
	AppLink bool
	// Subscribed holds the IDs of the result feeds the user follows.
	Subscribed map[string]bool
	// Icons holds the icon URLs of the result sites, or of the sections'
	// sites, that have one.
	Icons map[string]string
	// Sections is Discover's catalog as this reader sees it, shown when
	// there's no query; empty when the catalog has nothing for them.
	Sections []rss.DiscoverSection
	// Import is what Discover says about importing, with no query.
	Import discoverImport
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
	default:
		var siteKey string
		if u, err := discovery.ParseInput(data.Form.Q); err == nil {
			siteKey = discovery.SiteKey(u)
			if s.isAppLink(u) {
				data.AppLink = true
			} else {
				data.LookupQ = lookupAddress(u)
			}
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

	if err := s.renderHTML(w, status, "discover.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// isAppLink reports whether u (from discovery.ParseInput) is on the app's own
// site, which is never looked up.
func (s *Server) isAppLink(u *url.URL) bool {
	return s.appSiteKey != "" && discovery.SiteKey(u) == s.appSiteKey
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
