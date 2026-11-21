package admin

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// Picking Discover's feeds a page of sites at a time: the queue of sites
// with feeds still to decide (not gone, in the catalog or skipped), each
// feed with what helps choose, a tick and a section, and each site with
// Skip the rest. Save applies the page in one transaction; skips are
// undone from the Skipped filter.

// pickPageSize is how many sites the picking page lists at once.
const pickPageSize = 25

// maxPickBytes caps the picking page's posted body, over maxFormBytes,
// since a page holds up to 25 sites of 20 feeds, each posting four fields.
const maxPickBytes = 256 << 10

// pickSilentAfter is how long a feed goes without a post before the page
// notes it.
const pickSilentAfter = 90 * 24 * time.Hour

// pickFilters are the picking page's filters, in the order it offers
// them; the first is the default.
var pickFilters = []struct{ Key, Label string }{
	{rss.AdminPickSeed, "From the seed list"},
	{rss.AdminPickAll, "All sites"},
	{rss.AdminPickSkipped, "Skipped"},
}

// pickFilter returns the picking page's filter key names, or the default
// when the page doesn't offer it.
func pickFilter(key string) string {
	for _, f := range pickFilters {
		if f.Key == key {
			return key
		}
	}
	return pickFilters[0].Key
}

// pickHref is the picking page's URL with filter, from after the site
// after, leaving out the defaults.
func pickHref(filter, after string) string {
	v := url.Values{}
	if filter != pickFilters[0].Key {
		v.Set("filter", filter)
	}
	if after != "" {
		v.Set("after", after)
	}
	if len(v) == 0 {
		return "/discover/pick"
	}
	return "/discover/pick?" + v.Encode()
}

// pickForm is the picking page's Save: the page it was on (Filter and
// After, to show it again on an error), each listed feed with its site,
// tick and section, and each listed site with whether to skip the rest.
type pickForm struct {
	Filter string         `form:"filter"`
	After  string         `form:"after"`
	Feeds  []pickFeedForm `form:"feeds"`
	Sites  []pickSiteForm `form:"sites"`
}

type pickFeedForm struct {
	ID      string `form:"id"`
	Site    string `form:"site"`
	Pick    bool   `form:"pick"`
	Section string `form:"section"`
}

type pickSiteForm struct {
	ID   string `form:"id"`
	Skip bool   `form:"skip"`
}

// pickChoice is a feed's choices as the page shows them, and its error.
type pickChoice struct {
	Pick    bool
	Section string
	Error   string
}

// pickFeedRow is a feed on the picking page: its form index, choices,
// whether it's posted nothing in pickSilentAfter, and its error.
type pickFeedRow struct {
	rss.AdminPickFeed
	Index int
	pickChoice
	Silent bool
}

// pickSiteRow is a site on the picking page, its form index and its feeds.
type pickSiteRow struct {
	rss.AdminPickSite
	Index int
	Skip  bool
	Rows  []pickFeedRow
}

// pickPage is the picking page's data.
type pickPage struct {
	Nav      navData
	Flash    string
	Filters  []filterLink
	Filter   string
	After    string
	Skipped  bool
	Sites    []pickSiteRow
	Sections []rss.Section
	Now      time.Time
	// Error is a failed Save's error about a feed the page no longer
	// lists; "" for none.
	Error string
	// Next is the URL of the next page, "" on the last.
	Next string
}

// pickHandler lists the sites under ?filter= with feeds to decide,
// pickPageSize at a time, from after the site ?after=, each feed's section
// pre-filled from its site's hint. A cursor that isn't a site in the
// filter's order, or is its last, is 404.
func (s *Server) pickHandler(w http.ResponseWriter, r *http.Request) {
	filter := pickFilter(r.URL.Query().Get("filter"))
	after := r.URL.Query().Get("after")
	s.renderPick(w, r, http.StatusOK, filter, after, nil, nil, "")
}

// renderPick renders the picking page under filter from after the site
// after, with posted choices by feed ID and skips by site ID (nil for the
// page's own: the hint, a tick only for a site whose one candidate its
// seed entry's hint places, and Skip the rest ticked) and a non-field
// error.
func (s *Server) renderPick(w http.ResponseWriter, r *http.Request, status int, filter, after string,
	choices map[string]pickChoice, skips map[string]bool, pageErr string) {
	if after != "" && !validator.Matches(after, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}
	// One more than a page, to know whether there's another.
	sites, err := s.services.Feeds.PickSites(r.Context(), rss.AdminPickQuery{Filter: filter, After: after, Limit: pickPageSize + 1})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if after != "" && len(sites) == 0 && choices == nil {
		s.notFoundResponse(w, r)
		return
	}
	counts, err := s.services.Feeds.CountPick(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	sections, err := s.services.Feeds.DiscoverSections(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	now := s.now()
	page := pickPage{
		Nav:      s.nav(r, "discover"),
		Flash:    s.sessionManager.PopString(r.Context(), sessionFlash),
		Filter:   filter,
		After:    after,
		Skipped:  filter == rss.AdminPickSkipped,
		Sections: sections,
		Now:      now,
		Error:    pageErr,
	}
	if len(sites) > pickPageSize {
		sites = sites[:pickPageSize]
		page.Next = pickHref(filter, sites[len(sites)-1].ID)
	}
	for _, f := range pickFilters {
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: pickHref(f.Key, ""), Current: f.Key == filter, Count: count(counts[f.Key])})
	}
	i := 0
	for j, site := range sites {
		row := pickSiteRow{AdminPickSite: site, Index: j, Skip: true}
		if skips != nil {
			row.Skip = skips[site.ID]
		}
		preset := site.HintSeed && len(site.Feeds) == 1 && site.More == 0
		for _, f := range site.Feeds {
			fr := pickFeedRow{AdminPickFeed: f, Index: i, Silent: f.LatestPost.IsZero() || now.Sub(f.LatestPost) > pickSilentAfter}
			fr.pickChoice = pickChoice{Pick: preset, Section: site.Hint.ID}
			if c, ok := choices[f.ID]; ok {
				fr.pickChoice = c
			}
			row.Rows = append(row.Rows, fr)
			i++
		}
		page.Sites = append(page.Sites, row)
	}
	if err := s.renderHTML(w, status, "pick.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// pickFormHandler saves the picking page: its ticked feeds go in the
// catalog under their sections, and the feeds of a Skip the rest site that
// weren't ticked are skipped, all in one transaction, audited per feed. A
// ticked feed without a section, or one the store refuses, saves nothing
// and shows the page again (422) with the posted choices and the error on
// its row. Only the posted feeds are skipped, never others of their sites.
func (s *Server) pickFormHandler(w http.ResponseWriter, r *http.Request) {
	var form pickForm
	if err := s.decodePostFormLimit(w, r, &form, maxPickBytes); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	filter := pickFilter(form.Filter)
	skipSite := map[string]bool{}
	for _, site := range form.Sites {
		if !validator.Matches(site.ID, validator.UUIDRX) {
			s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That page of picks isn't valid.")
			return
		}
		skipSite[site.ID] = site.Skip
	}

	choices := map[string]pickChoice{}
	var picks []rss.DiscoverPick
	var skips []string
	valid := true
	for _, f := range form.Feeds {
		if !validator.Matches(f.ID, validator.UUIDRX) {
			s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That page of picks isn't valid.")
			return
		}
		c := pickChoice{Pick: f.Pick, Section: f.Section}
		switch {
		case f.Pick && f.Section == "":
			c.Error = "Choose a section."
			valid = false
		case f.Pick:
			picks = append(picks, rss.DiscoverPick{FeedID: f.ID, SectionID: f.Section})
		case skipSite[f.Site]:
			skips = append(skips, f.ID)
		}
		choices[f.ID] = c
	}
	if !valid {
		s.renderPick(w, r, http.StatusUnprocessableEntity, filter, form.After, choices, skipSite, "")
		return
	}

	err := s.services.Feeds.SavePicks(r.Context(), picks, skips, s.actor(r))
	var pe *rss.PickError
	if errors.As(err, &pe) {
		msg := "This feed no longer exists."
		switch {
		case errors.Is(err, rss.ErrUnknownSection):
			// Deleted since the page was read.
			msg = "Choose one of the sections."
		case errors.Is(err, rss.ErrFeedGone):
			msg = "This feed is gone, so it can't be in Discover."
		}
		c := choices[pe.FeedID]
		c.Error = msg
		choices[pe.FeedID] = c
		s.renderPick(w, r, http.StatusUnprocessableEntity, filter, form.After, choices, skipSite,
			"Nothing was saved: "+msg)
		return
	}
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.backTo(w, r, pickHref(filter, ""), pickSaved(len(picks), len(skips)))
}

// pickSaved is Save's flash for added feeds put in the catalog and skipped
// feeds skipped.
func pickSaved(added, skipped int) string {
	feeds := " feeds"
	if added == 1 {
		feeds = " feed"
	}
	return "Added " + count(added) + feeds + " to Discover and skipped " + count(skipped) + "."
}

// unskipForm puts the skipped feed Feed back in the queue.
type unskipForm struct {
	Feed                string `form:"feed"`
	validator.Validator `form:"-"`
}

// unskipHandler puts the posted skipped feed back in the queue, recorded
// as feed.discover_unskip, and goes back to Skipped. A feed that isn't
// skipped is 404.
func (s *Server) unskipHandler(w http.ResponseWriter, r *http.Request) {
	var form unskipForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	if !validator.Matches(form.Feed, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}
	err := s.services.Feeds.Unskip(r.Context(), form.Feed, s.actor(r))
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	}
	s.backTo(w, r, pickHref(rss.AdminPickSkipped, ""), "Unskipped: the feed is back in the queue.")
}
