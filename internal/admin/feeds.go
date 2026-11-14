package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// Feeds and sites: finding the feeds that fail, went quiet or answered
// 410, and fixing their fetching. Subscribers are counts, never who
// ("Admin" in docs/decisions.md). The reader's debug inspectors stay in
// www; these pages link to the reader's pages for them.

// feedsPageSize is how many feeds the feeds page lists at once.
const feedsPageSize = 100

// refreshTimeout bounds Refresh now, like the reader's Refresh button:
// under the server's WriteTimeout (10s), so the redirect can still be
// written.
const refreshTimeout = 8 * time.Second

// feedFilters are the feeds page's filters, in the order it offers them;
// the first is the default.
var feedFilters = []struct{ Key, Label string }{
	{rss.AdminFeedsFailing, "Failing"},
	{rss.AdminFeedsGone, "Gone"},
	{rss.AdminFeedsNeverFetched, "Never fetched"},
	{rss.AdminFeedsStale, "Stale"},
	{rss.AdminFeedsNoSubscribers, "No subscribers"},
	{rss.AdminFeedsAll, "All"},
}

// feedColumns are the feeds tables' columns; Sort is the order a column's
// header sorts by, "" for none, and Ascending whether it sorts that way.
var feedColumns = []struct {
	Label, Sort string
	Numeric     bool
	Ascending   bool
}{
	{Label: "Feed"},
	{Label: "Site"},
	{Label: "Subscribers", Sort: rss.AdminFeedsBySubscribers, Numeric: true},
	{Label: "Posts a week", Sort: rss.AdminFeedsByPosts, Numeric: true},
	{Label: "Last success", Sort: rss.AdminFeedsByLastSuccess, Ascending: true},
	{Label: "Last attempt"},
	{Label: "Failures", Sort: rss.AdminFeedsByFailures, Numeric: true},
	{Label: "Next fetch"},
	{Label: "Gone"},
}

// feedsParams are the feeds page's URL parameters.
type feedsParams struct {
	filter, q, sort, after string
}

// readFeedsParams reads the feeds page's parameters from r: a filter or
// order the page doesn't offer is its default. It reports false when
// ?after= isn't a feed's ID.
func readFeedsParams(r *http.Request) (feedsParams, bool) {
	q := r.URL.Query()
	p := feedsParams{filter: feedFilters[0].Key, q: strings.TrimSpace(q.Get("q")), sort: rss.AdminFeedsBySubscribers, after: q.Get("after")}
	for _, f := range feedFilters {
		if q.Get("filter") == f.Key {
			p.filter = f.Key
		}
	}
	for _, c := range feedColumns {
		if c.Sort != "" && q.Get("sort") == c.Sort {
			p.sort = c.Sort
		}
	}
	return p, p.after == "" || validator.Matches(p.after, validator.UUIDRX)
}

// ClearSearch is the feeds page's URL without the search.
func (p feedsPage) ClearSearch() string {
	return feedsParams{filter: p.Filter, sort: p.Sort}.href()
}

// href is the feeds page's URL with p, leaving out the defaults.
func (p feedsParams) href() string {
	v := url.Values{}
	if p.filter != feedFilters[0].Key {
		v.Set("filter", p.filter)
	}
	if p.q != "" {
		v.Set("q", p.q)
	}
	if p.sort != rss.AdminFeedsBySubscribers {
		v.Set("sort", p.sort)
	}
	if p.after != "" {
		v.Set("after", p.after)
	}
	if len(v) == 0 {
		return "/feeds"
	}
	return "/feeds?" + v.Encode()
}

// feedsPage is the feeds page's data.
type feedsPage struct {
	Nav     navData
	Filters []filterLink
	// Filter, Q and Sort are the current filter, search and order, for the
	// search form.
	Filter, Q, Sort string
	Table           feedTable
	// Next is the URL of the next page, "" on the last.
	Next string
}

// feedTable is the feed-table partial's data: its columns, its rows and
// the time they're shown at.
type feedTable struct {
	Columns []sortColumn
	Rows    []rss.AdminFeedRow
	Now     time.Time
}

// feedTableColumns are the feeds tables' headers. With params, the
// sortable ones link to their order, the current one marked; without,
// none do.
func feedTableColumns(params *feedsParams) []sortColumn {
	cols := make([]sortColumn, len(feedColumns))
	for i, c := range feedColumns {
		cols[i] = sortColumn{Label: c.Label, Numeric: c.Numeric}
		if params == nil || c.Sort == "" {
			continue
		}
		next := *params
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

// feedsHandler lists feeds, feedsPageSize at a time, filtered by ?filter=,
// searched by ?q= and ordered by ?sort=, from after the feed ?after=.
func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	params, ok := readFeedsParams(r)
	if !ok {
		s.notFoundResponse(w, r)
		return
	}
	// One more than a page, to know whether there's another.
	rows, err := s.services.Feeds.ListFeeds(r.Context(), rss.AdminFeedQuery{
		Filter: params.filter, Q: params.q, Sort: params.sort, After: params.after, Limit: feedsPageSize + 1,
	})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if params.after != "" && len(rows) == 0 {
		s.notFoundResponse(w, r)
		return
	}

	page := feedsPage{Nav: s.nav(r, "feeds"), Filter: params.filter, Q: params.q, Sort: params.sort}
	if len(rows) > feedsPageSize {
		rows = rows[:feedsPageSize]
		next := params
		next.after = rows[len(rows)-1].ID
		page.Next = next.href()
	}
	for _, f := range feedFilters {
		next := params
		next.filter, next.after = f.Key, ""
		page.Filters = append(page.Filters, filterLink{Label: f.Label, Href: next.href(), Current: f.Key == params.filter})
	}
	page.Table = feedTable{Columns: feedTableColumns(&params), Rows: rows, Now: s.now()}

	if err := s.renderHTML(w, http.StatusOK, "feeds.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// feedPage is a feed's page.
type feedPage struct {
	Nav   navData
	Flash string
	Feed  rss.AdminFeed
	// ReaderURL is the feed's page in the reader, where debug mode
	// inspects it; History, its entries in the audit log.
	ReaderURL string
	History   string
	Now       time.Time
}

// feedFromURL returns the feed the URL's {id} names; when there's none, it
// responds with 404 and returns false.
func (s *Server) feedFromURL(w http.ResponseWriter, r *http.Request) (rss.AdminFeed, bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.AdminFeed{}, false
	}
	feed, err := s.services.Feeds.Feed(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return rss.AdminFeed{}, false
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return rss.AdminFeed{}, false
	}
	return feed, true
}

// feedHandler shows every column of the feed {id}, its site, subscriber
// count, articles and enrichment jobs, and its actions.
func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feed, ok := s.feedFromURL(w, r)
	if !ok {
		return
	}
	data := feedPage{
		Nav:       s.nav(r, "feeds"),
		Flash:     s.sessionManager.PopString(r.Context(), sessionFlash),
		Feed:      feed,
		ReaderURL: s.config.AppURL + "/feeds/" + feed.ID,
		History:   auditHistoryURL("feed", feed.ID),
		Now:       s.now(),
	}
	if err := s.renderHTML(w, http.StatusOK, "feed.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// backTo redirects to path with msg as its flash.
func (s *Server) backTo(w http.ResponseWriter, r *http.Request, path, msg string) {
	s.sessionManager.Put(r.Context(), sessionFlash, msg)
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// feedRefreshHandler fetches the feed {id} now, as background refresh
// would, within refreshTimeout, ignoring the reader's 5-minute rule and
// per-reader limit, and says the outcome. It's recorded as feed.refresh
// with the outcome; the fetch records itself on the feed, as any does.
func (s *Server) feedRefreshHandler(w http.ResponseWriter, r *http.Request) {
	feed, ok := s.feedFromURL(w, r)
	if !ok {
		return
	}
	back := "/feeds/" + feed.ID
	if !feed.GoneAt.IsZero() {
		s.backTo(w, r, back, "This feed is marked gone: clear it first.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()
	res, err := s.services.Refresher.Refresh(ctx, feed.Feed)
	var msg string
	outcome := map[string]any{}
	switch {
	case err == nil && res.NotModified:
		outcome["outcome"] = "not_modified"
		msg = "Refreshed: not modified since the last fetch."
	case err == nil:
		outcome["outcome"], outcome["new"], outcome["updated"] = "saved", res.New, res.Updated
		msg = fmt.Sprintf("Refreshed: %s, %d updated.", articlesCount(res.New), res.Updated)
	case errors.Is(err, ingest.ErrGone):
		outcome["outcome"] = "gone"
		msg = "The feed answered 410 Gone, so it's marked gone."
	case errors.Is(err, ingest.ErrUnreachable) || errors.Is(err, ingest.ErrNotFeed):
		outcome["outcome"], outcome["error"] = "failed", err.Error()
		msg = "The refresh failed: " + err.Error()
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return
	default:
		s.serverErrorHTML(w, r, err)
		return
	}
	// Recorded even when the fetch used up the request's time.
	entry := rss.AuditEntry{AuditActor: s.actor(r), Action: "feed.refresh", TargetType: "feed", TargetID: feed.ID, After: outcome}
	if err := s.services.Audit.Record(context.WithoutCancel(r.Context()), entry); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.backTo(w, r, back, msg)
}

// articlesCount says n new articles.
func articlesCount(n int) string {
	if n == 1 {
		return "1 new article"
	}
	return fmt.Sprintf("%d new articles", n)
}

// feedResetHandler makes the feed {id} due now with its failures cleared.
func (s *Server) feedResetHandler(w http.ResponseWriter, r *http.Request) {
	feed, ok := s.feedFromURL(w, r)
	if !ok {
		return
	}
	s.feedAction(w, r, feed, s.services.Feeds.ResetBackoff(r.Context(), feed.ID, s.actor(r)),
		"Reset its backoff: it's fetched in the next minute or so.")
}

// feedClearGoneHandler makes the gone feed {id} fetchable again, due now.
func (s *Server) feedClearGoneHandler(w http.ResponseWriter, r *http.Request) {
	feed, ok := s.feedFromURL(w, r)
	if !ok {
		return
	}
	if feed.GoneAt.IsZero() {
		s.backTo(w, r, "/feeds/"+feed.ID, "This feed isn't marked gone.")
		return
	}
	s.feedAction(w, r, feed, s.services.Feeds.ClearGone(r.Context(), feed.ID, s.actor(r)),
		"Cleared gone: it's fetched in the next minute or so, if it has subscribers.")
}

// feedAction answers an action on feed that ended with err: back to its
// page with msg, or an error page.
func (s *Server) feedAction(w http.ResponseWriter, r *http.Request, feed rss.AdminFeed, err error, msg string) {
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.backTo(w, r, "/feeds/"+feed.ID, msg)
	}
}

// sitePage is a site's page.
type sitePage struct {
	Nav       navData
	Flash     string
	Site      rss.AdminSite
	Feeds     feedTable
	ReaderURL string
	History   string
	Now       time.Time
}

// siteFromURL returns the site the URL's {id} names; when there's none, it
// responds with 404 and returns false.
func (s *Server) siteFromURL(w http.ResponseWriter, r *http.Request) (rss.AdminSite, bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.AdminSite{}, false
	}
	site, err := s.services.Feeds.Site(r.Context(), id)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
		return rss.AdminSite{}, false
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return rss.AdminSite{}, false
	}
	return site, true
}

// siteHandler shows the site {id}: its columns, its feeds, its icon and its
// lookup, and its actions.
func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromURL(w, r)
	if !ok {
		return
	}
	now := s.now()
	data := sitePage{
		Nav:       s.nav(r, "feeds"),
		Flash:     s.sessionManager.PopString(r.Context(), sessionFlash),
		Site:      site,
		Feeds:     feedTable{Columns: feedTableColumns(nil), Rows: site.Feeds, Now: now},
		ReaderURL: s.config.AppURL + "/sites/" + site.Site.ID,
		History:   auditHistoryURL("site", site.Site.ID),
		Now:       now,
	}
	if err := s.renderHTML(w, http.StatusOK, "site.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// siteLookupHandler queues a lookup of the site {id} at a reader's
// priority, to find feeds it has added.
func (s *Server) siteLookupHandler(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromURL(w, r)
	if !ok {
		return
	}
	_, err := s.services.Feeds.CheckForFeeds(r.Context(), site.Site.ID, s.actor(r))
	s.siteAction(w, r, site, err, "Queued a lookup of "+site.Site.Host+": refresh this page to see how it went.")
}

// siteIconHandler makes the site {id}'s icon due to be checked now.
func (s *Server) siteIconHandler(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromURL(w, r)
	if !ok {
		return
	}
	err := s.services.Feeds.CheckIcon(r.Context(), site.Site.ID, s.actor(r))
	s.siteAction(w, r, site, err, "The icon is checked again in the next minute or so.")
}

// siteAction answers an action on site that ended with err: back to its
// page with msg, or an error page.
func (s *Server) siteAction(w http.ResponseWriter, r *http.Request, site rss.AdminSite, err error, msg string) {
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	case err != nil:
		s.serverErrorHTML(w, r, err)
	default:
		s.backTo(w, r, "/sites/"+site.Site.ID, msg)
	}
}
