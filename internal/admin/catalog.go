package admin

import (
	"net/http"
	"net/url"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// Discover's catalog by section: an overview of what Discover shows, each
// feed linking to its page, where its Discover panel edits it.

// catalogRow is a feed in the catalog with its section's name.
type catalogRow struct {
	rss.AdminCatalogFeed
	Section string
}

// catalogPage is the Discover page's data: a filter link for every
// section and one for all of them, the feeds the current one keeps, and
// whether that's all of them (Every), which shows each row's section.
type catalogPage struct {
	Nav     navData
	Filters []filterLink
	Rows    []catalogRow
	Every   bool
	// Section is the current section's name, "" for all.
	Section string
	Now     time.Time
	// Pick is how many sites have feeds to pick, formatted, for the
	// picking page's link.
	Pick string
}

// catalogHref is the Discover page's URL showing the section slug, or all
// of them with "".
func catalogHref(slug string) string {
	if slug == "" {
		return "/discover"
	}
	return "/discover?" + url.Values{"section": {slug}}.Encode()
}

// catalogHandler lists Discover's catalog in the section ?section= (a
// slug), or every section's feeds in their order when it names none, with
// how many feeds each section has.
func (s *Server) catalogHandler(w http.ResponseWriter, r *http.Request) {
	sections, err := s.services.Feeds.Catalog(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	slug := r.URL.Query().Get("section")
	known := false
	total := 0
	for _, sec := range sections {
		known = known || sec.Slug == slug
		total += len(sec.Feeds)
	}
	if !known {
		slug = ""
	}

	picks, err := s.services.Feeds.CountPick(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := catalogPage{Nav: s.nav(r, "discover"), Every: slug == "", Now: s.now(), Pick: count(picks[rss.AdminPickAll])}
	page.Filters = append(page.Filters, filterLink{Label: "All", Href: catalogHref(""), Current: slug == "", Count: count(total)})
	for _, sec := range sections {
		page.Filters = append(page.Filters, filterLink{Label: sec.Name, Href: catalogHref(sec.Slug), Current: sec.Slug == slug, Count: count(len(sec.Feeds))})
		if slug != "" && sec.Slug != slug {
			continue
		}
		if sec.Slug == slug {
			page.Section = sec.Name
		}
		for _, f := range sec.Feeds {
			page.Rows = append(page.Rows, catalogRow{AdminCatalogFeed: f, Section: sec.Name})
		}
	}
	if err := s.renderHTML(w, http.StatusOK, "discover.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
