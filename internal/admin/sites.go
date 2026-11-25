package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The Sites list: every site with its lookup, its icon, its feeds and its
// subscribers (a count, never who), leading to a site's page, which is
// otherwise reached only through one of its feeds (#706). Icons are shown
// as images, served by the admin itself (siteIconImageHandler), since its
// CSP allows only its own images.

// sitesPageSize is how many sites the Sites page lists at once.
const sitesPageSize = 100

// siteFilters are the Sites page's filters, in the order it offers them;
// the first is the default.
var siteFilters = []struct{ Key, Label string }{
	{rss.AdminSitesAll, "All"},
	{rss.AdminSitesNoIcon, "No icon"},
	{rss.AdminSitesLookupFailed, "Lookup failed"},
	{rss.AdminSitesNoSubscribers, "No subscribers"},
}

// siteColumns are the Sites table's columns, as feedColumns are the feeds
// tables'.
var siteColumns = []struct {
	Label, Sort string
	Numeric     bool
	Ascending   bool
}{
	{Label: "Site", Sort: rss.AdminSitesByHost, Ascending: true},
	{Label: "Lookup"},
	{Label: "Icon"},
	{Label: "Feeds", Sort: rss.AdminSitesByFeeds, Numeric: true},
	{Label: "Subscribers", Sort: rss.AdminSitesBySubscribers, Numeric: true},
	{Label: "Found", Sort: rss.AdminSitesByFound},
}

// sitesParams are the Sites page's URL parameters.
type sitesParams struct {
	filter, q, sort, after string
}

// readSitesParams reads the Sites page's parameters from r: a filter or
// order the page doesn't offer is its default. It reports false when
// ?after= isn't a site's ID.
func readSitesParams(r *http.Request) (sitesParams, bool) {
	q := r.URL.Query()
	p := sitesParams{filter: siteFilters[0].Key, q: strings.TrimSpace(q.Get("q")), sort: rss.AdminSitesBySubscribers, after: q.Get("after")}
	for _, f := range siteFilters {
		if q.Get("filter") == f.Key {
			p.filter = f.Key
		}
	}
	for _, c := range siteColumns {
		if c.Sort != "" && q.Get("sort") == c.Sort {
			p.sort = c.Sort
		}
	}
	return p, p.after == "" || validator.Matches(p.after, validator.UUIDRX)
}

// href is the Sites page's URL with p, leaving out the defaults.
func (p sitesParams) href() string {
	v := url.Values{}
	if p.filter != siteFilters[0].Key {
		v.Set("filter", p.filter)
	}
	if p.q != "" {
		v.Set("q", p.q)
	}
	if p.sort != rss.AdminSitesBySubscribers {
		v.Set("sort", p.sort)
	}
	if p.after != "" {
		v.Set("after", p.after)
	}
	if len(v) == 0 {
		return "/sites"
	}
	return "/sites?" + v.Encode()
}

// siteRow is a site as the Sites table shows it: its lookup in words, in
// the danger tone when it failed, its icon's URL ("" for none) and its icon
// in short.
type siteRow struct {
	rss.AdminSiteRow
	Lookup  feedProblem
	IconURL string
	Icon    string
}

// iconURL is the admin's URL of icon i, versioned by its hash so a changed
// icon gets a new URL and an unchanged one stays cached; "" when there's
// none.
func iconURL(i rss.SiteIcon) string {
	if i.SiteID == "" {
		return ""
	}
	return "/sites/" + i.SiteID + "/icon?" + url.Values{"v": {i.Hash}}.Encode()
}

// iconLabel says what icon i is, "64×64 PNG", or "None" when there's none.
func iconLabel(i rss.SiteIcon) string {
	if i.SiteID == "" {
		return "None"
	}
	kind := strings.ToUpper(strings.TrimPrefix(i.ContentType, "image/"))
	if kind == "X-ICON" {
		kind = "ICO"
	}
	return fmt.Sprintf("%d×%d %s", i.Width, i.Height, kind)
}

// sitesPage is the Sites page's data.
type sitesPage struct {
	Nav     navData
	Filters []filterLink
	// Filter, Q and Sort are the current filter, search and order, for the
	// search form.
	Filter, Q, Sort string
	Columns         []sortColumn
	Rows            []siteRow
	Now             time.Time
	// Next is the URL of the next page, "" on the last.
	Next string
}

// ClearSearch is the Sites page's URL without the search.
func (p sitesPage) ClearSearch() string {
	return sitesParams{filter: p.Filter, sort: p.Sort}.href()
}

// siteTableColumns are the Sites table's headers, each sortable one
// linking to its order with params' filter and search, the current one
// marked.
func siteTableColumns(params sitesParams) []sortColumn {
	cols := make([]sortColumn, len(siteColumns))
	for i, c := range siteColumns {
		cols[i] = sortColumn{Label: c.Label, Numeric: c.Numeric}
		if c.Sort == "" {
			continue
		}
		next := params
		next.sort, next.after = c.Sort, ""
		cols[i].Href = next.href()
		if params.sort == c.Sort {
			cols[i].Sort = "descending"
			if c.Ascending {
				cols[i].Sort = "ascending"
			}
		}
	}
	return cols
}

// sitesHandler lists sites, sitesPageSize at a time, filtered by ?filter=,
// searched by ?q= (hosts and titles) and ordered by ?sort=, from after the
// site ?after=, with how many sites matching the search each filter keeps.
func (s *Server) sitesHandler(w http.ResponseWriter, r *http.Request) {
	params, ok := readSitesParams(r)
	if !ok {
		s.notFoundResponse(w, r)
		return
	}
	// One more than a page, to know whether there's another.
	rows, err := s.services.Feeds.ListSites(r.Context(), rss.AdminSiteQuery{
		Filter: params.filter, Q: params.q, Sort: params.sort, After: params.after, Limit: sitesPageSize + 1,
	})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if params.after != "" && len(rows) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	counts, err := s.services.Feeds.CountSites(r.Context(), params.q)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	now := s.now()
	page := sitesPage{Nav: s.nav(r, "sites"), Filter: params.filter, Q: params.q, Sort: params.sort,
		Columns: siteTableColumns(params), Now: now}
	if len(rows) > sitesPageSize {
		rows = rows[:sitesPageSize]
		next := params
		next.after = rows[len(rows)-1].ID
		page.Next = next.href()
	}
	for _, f := range siteFilters {
		next := params
		next.filter, next.after = f.Key, ""
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: next.href(), Current: f.Key == params.filter, Count: count(counts[f.Key])})
	}
	for _, row := range rows {
		page.Rows = append(page.Rows, siteRow{AdminSiteRow: row, Lookup: lookupSummary(row.Lookup, now), IconURL: iconURL(row.Icon), Icon: iconLabel(row.Icon)})
	}

	if err := s.renderHTML(w, http.StatusOK, "sites.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// siteIconImageHandler serves the site {id}'s stored icon, 404 when it has
// none, so the admin can show it: its CSP allows only its own images, so
// it can't use the reader's /sites/{id}/icon. Like the reader's, a URL
// whose ?v= is the icon's hash is cached for good (privately: the admin is
// behind its login), and the image is sandboxed in case it's opened
// directly.
func (s *Server) siteIconImageHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return
	}
	icon, err := s.services.Feeds.SiteIcon(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}

	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.URL.Query().Get("v") == icon.Hash {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	h.Set("Content-Type", icon.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(icon.Data)))
	w.WriteHeader(http.StatusOK)
	w.Write(icon.Data)
}
