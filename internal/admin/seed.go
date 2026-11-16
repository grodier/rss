package admin

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
	"github.com/grodier/rss/internal/validator"
)

// The seed list: which of internal/seed/sites.txt's sites were found, and
// how many of their feeds are in Discover's catalog, so the owner sees a
// failed entry without reading the deploy log. The file stays the list's
// source; this page only reads it and looks entries up again.

// seedFilters are the seed list page's filters, in the order it offers
// them; the first is the default. Keep says whether an entry is under it.
var seedFilters = []struct {
	Key, Label string
	Keep       func(rss.AdminSeedEntry) bool
}{
	{"all", "All", func(rss.AdminSeedEntry) bool { return true }},
	{"not-found", "Not found", func(e rss.AdminSeedEntry) bool {
		return e.State() == rss.SeedFailed || e.State() == rss.SeedNoFeeds
	}},
	{"waiting", "Waiting", func(e rss.AdminSeedEntry) bool {
		return e.State() == rss.SeedWaiting || e.State() == rss.SeedRunning
	}},
	{"not-in-discover", "Found but not in Discover", func(e rss.AdminSeedEntry) bool {
		return e.State() == rss.SeedFound && e.InDiscover == 0
	}},
}

// seedFilter returns the seed list filter key names, or the default when
// the page doesn't offer it.
func seedFilter(key string) string {
	for _, f := range seedFilters {
		if f.Key == key {
			return key
		}
	}
	return seedFilters[0].Key
}

// seedHref is the seed list page's URL with filter, leaving out the
// default.
func seedHref(filter string) string {
	if filter == seedFilters[0].Key {
		return "/seed"
	}
	return "/seed?" + url.Values{"filter": {filter}}.Encode()
}

// seedRow is a seed list entry as the page shows it: where it stands, in
// the feeds' Problem style, and whether it offers Look up again.
type seedRow struct {
	rss.AdminSeedEntry
	Status feedProblem
	Retry  bool
}

// seedStatus says where e stands: found with its feeds' count, failed with
// its error, no feeds, waiting or running.
func seedStatus(e rss.AdminSeedEntry) feedProblem {
	switch e.State() {
	case rss.SeedFound:
		if e.Feeds == 1 {
			return feedProblem{Text: "Found 1 feed"}
		}
		return feedProblem{Text: fmt.Sprintf("Found %d feeds", e.Feeds)}
	case rss.SeedFailed:
		return feedProblem{Text: "Failed", Detail: e.Lookup.Error, Bad: true}
	case rss.SeedNoFeeds:
		return feedProblem{Text: "No feeds", Bad: true}
	case rss.SeedRunning:
		return feedProblem{Text: "Running"}
	}
	if e.Lookup.ID == "" {
		return feedProblem{Text: "Waiting", Detail: "not queued yet"}
	}
	return feedProblem{Text: "Waiting"}
}

// seedRetry reports whether e offers Look up again: when its lookup
// failed or found no feeds.
func seedRetry(e rss.AdminSeedEntry) bool {
	return e.State() == rss.SeedFailed || e.State() == rss.SeedNoFeeds
}

// seedPage is the seed list page's data.
type seedPage struct {
	Nav     navData
	Flash   string
	Filters []filterLink
	Filter  string
	Rows    []seedRow
	// Total is how many entries the list has.
	Total int
}

// seedHandler lists the seed list's entries under ?filter=, in the file's
// order, with how many each filter keeps.
func (s *Server) seedHandler(w http.ResponseWriter, r *http.Request) {
	filter := seedFilter(r.URL.Query().Get("filter"))
	entries, err := s.services.Feeds.SeedList(r.Context(), s.config.SeedList)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	page := seedPage{
		Nav:    s.nav(r, "seed"),
		Flash:  s.sessionManager.PopString(r.Context(), sessionFlash),
		Filter: filter,
		Total:  len(entries),
	}
	for _, f := range seedFilters {
		n := 0
		for _, e := range entries {
			if f.Keep(e) {
				n++
				if f.Key == filter {
					page.Rows = append(page.Rows, seedRow{AdminSeedEntry: e, Status: seedStatus(e), Retry: seedRetry(e)})
				}
			}
		}
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: seedHref(f.Key), Current: f.Key == filter, Count: count(n)})
	}
	if err := s.renderHTML(w, http.StatusOK, "seed.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// seedLookupForm looks up the seed list's entry with SiteKey again, then
// goes back to the list under Filter.
type seedLookupForm struct {
	SiteKey             string `form:"site_key"`
	Filter              string `form:"filter"`
	validator.Validator `form:"-"`
}

// seedLookupHandler queues a lookup of the posted seed list entry's own
// address, even when an earlier lookup found a site, since a site's
// lookup (siteLookupHandler) fetches its home page, which isn't the
// entry's when the entry is a feed. It's recorded as seed.lookup. A site
// key that isn't on the list is 404.
func (s *Server) seedLookupHandler(w http.ResponseWriter, r *http.Request) {
	var form seedLookupForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	var entry seed.Entry
	for _, e := range s.config.SeedList {
		if e.SiteKey == form.SiteKey {
			entry = e
		}
	}
	if entry.SiteKey == "" {
		s.notFoundResponse(w, r)
		return
	}
	l, err := s.services.Feeds.QueueSeedLookup(r.Context(), entry, s.actor(r))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	msg := "Queued a lookup of " + entry.SiteKey + ": refresh this page to see how it went."
	if l.Status == rss.LookupRunning {
		msg = "A lookup of " + entry.SiteKey + " is already running: refresh this page to see how it went."
	}
	s.backTo(w, r, seedHref(seedFilter(form.Filter)), msg)
}
