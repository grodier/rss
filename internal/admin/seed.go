package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The seed list: the sites in the seed_sites table, newest first, with
// whether each was found and how many of its feeds are in Discover's
// catalog, so the owner sees a failed entry and looks it up again. Nothing
// retries on its own: Look up again and Look up all again do.

// seedPageSize is how many entries the seed list page lists at once.
const seedPageSize = 100

// seedFilters are the seed list page's filters, in the order it offers
// them; the first is the default.
var seedFilters = []struct{ Key, Label string }{
	{rss.AdminSeedAll, "All"},
	{rss.AdminSeedNotFound, "Not found"},
	{rss.AdminSeedWaiting, "Waiting"},
	{rss.AdminSeedNotInDiscover, "Found but not in Discover"},
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

// seedHref is the seed list page's URL with filter, from after the entry
// with site key after, leaving out the defaults.
func seedHref(filter, after string) string {
	v := url.Values{}
	if filter != seedFilters[0].Key {
		v.Set("filter", filter)
	}
	if after != "" {
		v.Set("after", after)
	}
	if len(v) == 0 {
		return "/seed"
	}
	return "/seed?" + v.Encode()
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
	Now     time.Time
	// Total is how many entries the list has.
	Total int
	// RetryAll is how many entries Look up all again would look up again,
	// formatted, on the Not found filter; "" when it isn't offered.
	RetryAll string
	// Next is the URL of the next page, "" on the last.
	Next string
}

// seedHandler lists the seed list's entries, seedPageSize at a time,
// newest first, under ?filter=, from after the entry ?after= (a site key),
// with how many each filter keeps. A cursor that isn't on the list, or is
// its filter's last entry, is 404.
func (s *Server) seedHandler(w http.ResponseWriter, r *http.Request) {
	filter := seedFilter(r.URL.Query().Get("filter"))
	after := r.URL.Query().Get("after")
	// One more than a page, to know whether there's another.
	entries, err := s.services.Feeds.SeedList(r.Context(), rss.AdminSeedQuery{Filter: filter, After: after, Limit: seedPageSize + 1})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if after != "" && len(entries) == 0 {
		s.notFoundResponse(w, r)
		return
	}
	counts, err := s.services.Feeds.CountSeed(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	page := seedPage{
		Nav:    s.nav(r, "seed"),
		Flash:  s.sessionManager.PopString(r.Context(), sessionFlash),
		Filter: filter,
		Now:    s.now(),
		Total:  counts[rss.AdminSeedAll],
	}
	if n := counts[rss.AdminSeedNotFound]; filter == rss.AdminSeedNotFound && n > 0 {
		page.RetryAll = count(n)
	}
	if len(entries) > seedPageSize {
		entries = entries[:seedPageSize]
		page.Next = seedHref(filter, entries[len(entries)-1].SiteKey)
	}
	for _, f := range seedFilters {
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: seedHref(f.Key, ""), Current: f.Key == filter, Count: count(counts[f.Key])})
	}
	for _, e := range entries {
		page.Rows = append(page.Rows, seedRow{AdminSeedEntry: e, Status: seedStatus(e), Retry: seedRetry(e)})
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
	l, err := s.services.Feeds.QueueSeedLookup(r.Context(), form.SiteKey, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}
	msg := "Queued a lookup of " + form.SiteKey + ": refresh this page to see how it went."
	if l.Status == rss.LookupRunning {
		msg = "A lookup of " + form.SiteKey + " is already running: refresh this page to see how it went."
	}
	s.backTo(w, r, seedHref(seedFilter(form.Filter), ""), msg)
}

// seedRetryHandler looks up every entry under Not found again, at the
// seed's priority, behind readers' lookups, and goes back to Not found.
// It's recorded as seed.retry with the count, unless there were none.
func (s *Server) seedRetryHandler(w http.ResponseWriter, r *http.Request) {
	n, err := s.services.Feeds.RetrySeedLookups(r.Context(), s.actor(r))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	var msg string
	switch n {
	case 0:
		msg = "Nothing to look up again."
	case 1:
		msg = "Looking up 1 site again."
	default:
		msg = "Looking up " + count(n) + " sites again."
	}
	s.backTo(w, r, seedHref(rss.AdminSeedNotFound, ""), msg)
}
