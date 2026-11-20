package server

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	site, err := s.services.SiteService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	feeds, err := s.services.FeedService.ListBySite(r.Context(), site.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	facts, err := s.services.FeedService.SiteFeedFacts(r.Context(), site.ID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	feedIDs := make([]string, len(feeds))
	for i, f := range feeds {
		feedIDs[i] = f.ID
	}
	userID, _ := s.authenticatedUserID(r)
	subscribed, err := s.services.SubscriptionService.SubscribedFeedIDs(r.Context(), userID, feedIDs)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	rows := siteFeedRows(site, feeds, facts, subscribed)
	postFeeds := sitePostFeeds(rows)
	posts, err := s.sitePosts(r, userID, postFeeds)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// No lookup for the site's key means it can be checked now.
	lookup, err := s.services.LookupService.GetBySiteKey(r.Context(), site.Host)
	if err != nil && !errors.Is(err, rss.ErrNoRecord) {
		s.serverErrorHTML(w, r, err)
		return
	}

	var debug *siteDebugView
	if s.debugEnabled(r) {
		debug, err = s.siteDebug(r.Context(), site, feeds, lookup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	data := struct {
		Nav   navData
		Site  rss.Site
		Icons map[string]string
		// Feeds is the site's first siteFeedsShown feeds and MoreFeeds the
		// rest, behind "Show N more feeds"; FeedCount counts both.
		Feeds     []siteFeedRow
		MoreFeeds []siteFeedRow
		FeedCount int
		// OnlyFeed is the site's feed when it has just one, whose Follow
		// and facts are in the page's head instead of a list; else nil.
		OnlyFeed *siteFeedRow
		Posts    []articleRow
		// PostsFrom names the feeds Recent posts are drawn from when
		// they're only some of the site's live feeds; empty otherwise.
		PostsFrom   []string
		Live        bool // some feed isn't gone
		LastChecked time.Time
		Checking    bool
		CanRecheck  bool
		LookupID    string
		Flash       string
		Debug       *siteDebugView
	}{
		Nav:         s.nav(r, ""),
		Site:        site,
		Icons:       s.pageIcons(r, []string{site.ID}),
		Feeds:       rows[:min(len(rows), siteFeedsShown)],
		MoreFeeds:   rows[min(len(rows), siteFeedsShown):],
		FeedCount:   len(rows),
		OnlyFeed:    onlyFeed(site, rows),
		Posts:       posts,
		PostsFrom:   postsFrom(rows, postFeeds),
		Live:        slices.ContainsFunc(rows, func(f siteFeedRow) bool { return !f.Gone() }),
		LastChecked: lookup.FinishedAt,
		Checking:    lookupInProgress(lookup),
		CanRecheck:  canRecheck(lookup, time.Now()),
		LookupID:    lookup.ID,
		Flash:       s.sessionManager.PopString(r.Context(), "flash"),
		Debug:       debug,
	}

	if err := s.renderHTML(w, http.StatusOK, "site.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

const (
	// sitePostsLimit is how many of a site's newest articles its page
	// shows.
	sitePostsLimit = 10
	// siteFeedsShown is how many of a site's feeds its page lists before
	// "Show N more feeds", so a site with many keeps its posts in view.
	siteFeedsShown = 4
)

// siteFeedRow is one of a site's feeds as its page lists it.
type siteFeedRow struct {
	Feed   rss.Feed
	Name   string // its title without the site's name (shortFeedName)
	Facts  rss.FeedFacts
	Follow followButton // Follow, or Following when the reader follows it
}

// Gone is whether the feed no longer exists (it answered 410).
func (f siteFeedRow) Gone() bool { return !f.Feed.GoneAt.IsZero() }

// inCatalog is whether the feed is in Discover's catalog.
func (f siteFeedRow) inCatalog() bool { return f.Facts.Section.ID != "" }

// siteFeedRows lists a site's feeds for its page: those in Discover's
// catalog first, as an admin picked them, then the others, and the gone
// ones last, each group most recently posting first, as a section's page
// orders its feeds, so the capped list shows the active ones. Follow sends
// the reader back to the feed's row.
func siteFeedRows(site rss.Site, feeds []rss.Feed, facts map[string]rss.FeedFacts, subscribed map[string]bool) []siteFeedRow {
	siteName := cmp.Or(site.Title, site.Host)
	rows := make([]siteFeedRow, len(feeds))
	for i, f := range feeds {
		rows[i] = siteFeedRow{
			Feed:  f,
			Name:  shortFeedName(cmp.Or(f.Title, f.Url), siteName),
			Facts: facts[f.ID],
			Follow: followButton{
				FeedID:    f.ID,
				Label:     rss.DiscoverFeed{Feed: f, Site: site}.Label(),
				ReturnTo:  "/sites/" + site.ID + "#feed-" + f.ID,
				Following: subscribed[f.ID],
			},
		}
	}
	rank := func(r siteFeedRow) int {
		switch {
		case r.Gone():
			return 2
		case r.inCatalog():
			return 0
		default:
			return 1
		}
	}
	slices.SortStableFunc(rows, func(a, b siteFeedRow) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return b.Facts.LatestAt.Compare(a.Facts.LatestAt)
	})
	return rows
}

// onlyFeed is rows' one feed, or nil when there are none or several. Its
// Follow, in the page's head, sends the reader back to the page's top.
func onlyFeed(site rss.Site, rows []siteFeedRow) *siteFeedRow {
	if len(rows) != 1 {
		return nil
	}
	f := rows[0]
	f.Follow.ReturnTo = "/sites/" + site.ID
	return &f
}

// shortFeedName is a feed's title on its site's page, where the site's
// name is already in the head: without a leading siteName and separator
// ("The Daily Ledger – Opinion" is "Opinion"), else the title as it is.
func shortFeedName(title, siteName string) string {
	rest, ok := strings.CutPrefix(title, siteName)
	if !ok {
		return title
	}
	for _, sep := range []string{" – ", " — ", " - ", ": ", " | ", " · "} {
		if short, ok := strings.CutPrefix(rest, sep); ok && short != "" {
			return short
		}
	}
	return title
}

// sitePostFeeds returns the feeds a site's Recent posts are drawn from,
// in rows' order: its live feeds in Discover's catalog, which an admin
// picked as its best, or all its live feeds when none is.
func sitePostFeeds(rows []siteFeedRow) []siteFeedRow {
	var catalog, live []siteFeedRow
	for _, f := range rows {
		if f.Gone() {
			continue
		}
		live = append(live, f)
		if f.inCatalog() {
			catalog = append(catalog, f)
		}
	}
	if len(catalog) > 0 {
		return catalog
	}
	return live
}

// postsFrom names the feeds Recent posts are drawn from (postFeeds, from
// sitePostFeeds) when they're only some of rows' live feeds, so the
// reader can tell why the others' posts are missing; else nil.
func postsFrom(rows, postFeeds []siteFeedRow) []string {
	live := 0
	for _, f := range rows {
		if !f.Gone() {
			live++
		}
	}
	if len(postFeeds) == live {
		return nil
	}
	names := make([]string, len(postFeeds))
	for i, f := range postFeeds {
		names[i] = f.Name
	}
	return names
}

// sitePosts returns the newest sitePostsLimit articles of feeds as a
// site's Recent posts. A copy of an article in two of the feeds (a blog's
// all-posts and category feeds) shows once. When the posts come from more
// than one feed, each names its feed (by its short name, without an
// avatar, as the site's is in the head); Follow is in the feed list
// above, so posts don't repeat it.
func (s *Server) sitePosts(r *http.Request, userID string, feeds []siteFeedRow) ([]articleRow, error) {
	if len(feeds) == 0 {
		return nil, nil
	}
	ids := make([]string, len(feeds))
	byID := make(map[string]siteFeedRow, len(feeds))
	for i, f := range feeds {
		ids[i] = f.Feed.ID
		byID[f.Feed.ID] = f
	}

	// Twice the limit, so copies dropped below still leave a full list.
	articles, err := s.services.ArticleService.ListByFeeds(r.Context(), ids, rss.ArticleCursor{}, 2*sitePostsLimit)
	if err != nil {
		return nil, err
	}
	posts := make([]articleRow, 0, sitePostsLimit)
	seen := map[string]bool{}
	fromFeeds := map[string]bool{}
	for _, a := range articles {
		if len(posts) == sitePostsLimit {
			break
		}
		if a.CanonicalURL != "" {
			if seen[a.CanonicalURL] {
				continue
			}
			seen[a.CanonicalURL] = true
		}
		posts = append(posts, articleRow{Article: a, ReturnTo: r.URL.RequestURI()})
		fromFeeds[a.FeedID] = true
	}
	if len(fromFeeds) > 1 {
		for i := range posts {
			f := byID[posts[i].Article.FeedID]
			posts[i].Feed = f.Feed
			posts[i].Feed.Title = f.Name
			posts[i].NoAvatar = true
		}
	}

	if err := s.fillRead(r.Context(), userID, posts); err != nil {
		return nil, err
	}
	if err := s.fillSaved(r.Context(), userID, posts); err != nil {
		return nil, err
	}
	if err := s.fillStarred(r.Context(), userID, posts); err != nil {
		return nil, err
	}
	s.fillDebug(r, posts)
	markFirstImageEager(posts)
	return posts, nil
}

// siteDebug loads the site page's debug panel: the site's fields and a line
// for each of feeds.
func (s *Server) siteDebug(ctx context.Context, site rss.Site, feeds []rss.Feed, lookup rss.Lookup) (*siteDebugView, error) {
	d, err := s.services.DebugService.SiteDebug(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	feedIDs := make([]string, len(feeds))
	for i, f := range feeds {
		feedIDs[i] = f.ID
	}
	fd, err := s.services.DebugService.FeedDebug(ctx, feedIDs)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	view := &siteDebugView{Fields: siteDebugFields(site, d, lookup, now)}
	for _, f := range feeds {
		view.Feeds = append(view.Feeds, siteFeedDebugLine(f, fd[f.ID], now))
	}
	return view, nil
}

// canRecheck reports whether a new lookup of the site's home page may be
// requested: there is none yet, the last one ran on another address (a feed
// or an article, so the home page's feeds are unchecked), or at now it
// finished longer ago than the TTL that LookupStore.Request enforces for its
// result.
func canRecheck(l rss.Lookup, now time.Time) bool {
	age := now.Sub(l.FinishedAt)
	switch l.Status {
	case "":
		return true
	case rss.LookupDone:
		if !ranOnHomePage(l) {
			return true
		}
		if l.SiteID == "" {
			return age > lookupFailedTTL
		}
		return age > lookupDoneTTL
	case rss.LookupFailed:
		return age > lookupFailedTTL
	default:
		return false
	}
}

// ranOnHomePage reports whether l ran on its site's home page.
func ranOnHomePage(l rss.Lookup) bool {
	u, err := url.Parse(l.URL)
	return err == nil && discovery.IsHomePage(u)
}

// siteIconHandler serves a site's stored icon. Icons are fetched from other
// sites, so they're locked down like any untrusted content: a CSP that
// allows nothing and sandboxes the response, and nosniff (from
// commonHeaders). Icon URLs carry the icon's hash as ?v=, so a request for
// the current hash can be cached for good.
func (s *Server) siteIconHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	icon, err := s.services.SiteService.GetIcon(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("ETag", `"`+icon.Hash+`"`)
	if r.URL.Query().Get("v") == icon.Hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}
	if etagMatches(r.Header.Get("If-None-Match"), icon.Hash) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	h.Set("Content-Type", icon.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(icon.Data)))
	w.WriteHeader(http.StatusOK)
	w.Write(icon.Data)
}

// etagMatches reports whether an If-None-Match header lists the entity tag
// "hash" (weak or strong) or is "*".
func etagMatches(header, hash string) bool {
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
		if tag == "*" || tag == `"`+hash+`"` {
			return true
		}
	}
	return false
}

// siteIconURLs returns the icon URL (/sites/{id}/icon?v={hash}) of each of
// siteIDs that has an icon.
func (s *Server) siteIconURLs(ctx context.Context, siteIDs []string) (map[string]string, error) {
	ids := slices.Compact(slices.Sorted(slices.Values(siteIDs)))
	hashes, err := s.services.SiteService.IconHashes(ctx, ids)
	if err != nil {
		return nil, err
	}
	urls := make(map[string]string, len(hashes))
	for id, hash := range hashes {
		urls[id] = "/sites/" + id + "/icon?v=" + url.QueryEscape(hash)
	}
	return urls, nil
}

// pageIcons is siteIconURLs for a page, where icons are decoration: an error
// is logged and the page shows letters instead.
func (s *Server) pageIcons(r *http.Request, siteIDs []string) map[string]string {
	urls, err := s.siteIconURLs(r.Context(), siteIDs)
	if err != nil {
		s.logError(r, err)
		return map[string]string{}
	}
	return urls
}
