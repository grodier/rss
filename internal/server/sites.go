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

	icons := s.pageIcons(r, []string{site.ID})
	rows := siteFeedRows(site, icons[site.ID], feeds, facts, subscribed, r.URL.Path)
	posts, err := s.sitePosts(r, userID, rows)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	// PROTOTYPE (option f): posts without Follow or avatars, and what
	// they're drawn from when that's some of the site's feeds.
	var postsFrom string
	if r.URL.Query().Get("layout") == "f" {
		names := map[string]string{}
		for _, f := range rows {
			names[f.Feed.ID] = f.Name
		}
		// Each post names its feed only when they're from several.
		distinct := map[string]bool{}
		for _, p := range posts {
			distinct[p.Article.FeedID] = true
		}
		for i := range posts {
			if len(distinct) < 2 {
				posts[i].Feed = rss.Feed{}
			}
			posts[i].Follow = nil
			posts[i].NoAvatar = true
			if posts[i].Feed.ID != "" {
				posts[i].Feed.Title = names[posts[i].Feed.ID]
			}
		}
		var catalog []string
		live := 0
		for _, f := range rows {
			if f.Gone() {
				continue
			}
			live++
			if f.Facts.Section.ID != "" {
				catalog = append(catalog, f.Name)
			}
		}
		if len(catalog) > 0 && len(catalog) < live {
			postsFrom = strings.Join(catalog, ", ")
		}
	}
	// PROTOTYPE (option c): a tab per feed.
	tab := r.URL.Query().Get("feed")
	if r.URL.Query().Get("layout") == "c" && len(rows) > 0 {
		if tab == "" {
			tab = rows[0].Feed.ID
		}
		for i := range rows {
			if rows[i].Feed.ID == tab {
				posts, err = s.sitePosts(r, userID, rows[i:i+1])
				if err != nil {
					s.serverErrorHTML(w, r, err)
					return
				}
			}
		}
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
		Nav         navData
		Site        rss.Site
		Icons       map[string]string
		Feeds       []siteFeedRow
		Posts       []articleRow
		Live        bool   // some feed isn't gone
		Layout      string // PROTOTYPE
		Tab         string // PROTOTYPE
		PostsFrom   string // PROTOTYPE
		LastChecked time.Time
		Checking    bool
		CanRecheck  bool
		LookupID    string
		Flash       string
		Debug       *siteDebugView
	}{
		Nav:         s.nav(r, ""),
		Site:        site,
		Icons:       icons,
		Feeds:       rows,
		Posts:       posts,
		Live:        slices.ContainsFunc(rows, func(f siteFeedRow) bool { return !f.Gone() }),
		Layout:      r.URL.Query().Get("layout"),
		Tab:         tab,
		PostsFrom:   postsFrom,
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

// sitePostsLimit is how many of a site's newest articles its page shows.
const sitePostsLimit = 10

// siteFeedRow is one of a site's feeds as its page lists it.
type siteFeedRow struct {
	Feed   rss.Feed
	Name   string // PROTOTYPE: its title without the site's name (shortFeedName)
	Facts  rss.FeedFacts
	Site   string       // its site's name, for the avatar's letter
	Icon   string       // its site's icon URL, or "" for its letter
	Follow followButton // Follow, or Following when the reader follows it
}

// Gone is whether the feed no longer exists (it answered 410).
func (f siteFeedRow) Gone() bool { return !f.Feed.GoneAt.IsZero() }

// siteFeedRows lists a site's feeds for its page: those in Discover's
// catalog first, as an admin picked them, then the others, and the gone
// ones last, each group in feeds' order. Follow sends the reader back to
// path, at the feed's row.
func siteFeedRows(site rss.Site, icon string, feeds []rss.Feed, facts map[string]rss.FeedFacts, subscribed map[string]bool, path string) []siteFeedRow {
	rows := make([]siteFeedRow, len(feeds))
	for i, f := range feeds {
		rows[i] = siteFeedRow{
			Feed:  f,
			Name:  shortFeedName(cmp.Or(f.Title, f.Url), cmp.Or(site.Title, site.Host)),
			Facts: facts[f.ID],
			Site:  cmp.Or(site.Title, site.Host),
			Icon:  icon,
			Follow: followButton{
				FeedID:    f.ID,
				Label:     rss.DiscoverFeed{Feed: f, Site: site}.Label(),
				ReturnTo:  path + "#feed-" + f.ID,
				Following: subscribed[f.ID],
			},
		}
	}
	rank := func(r siteFeedRow) int {
		switch {
		case r.Gone():
			return 2
		case r.Facts.Section.ID != "":
			return 0
		default:
			return 1
		}
	}
	// Within a group, the most recently posting first, as a section's
	// page orders its feeds, so a capped list shows the active ones.
	slices.SortStableFunc(rows, func(a, b siteFeedRow) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return b.Facts.Latest.FeedSortAt().Compare(a.Facts.Latest.FeedSortAt())
	})
	return rows
}

// sitePosts returns a site's newest sitePostsLimit articles as its page's
// Recent posts: those of its feeds in Discover's catalog, which an admin
// picked as its best, or of all its feeds when none is, leaving out gone
// feeds either way. A copy of an article in two of the feeds (a blog's
// all-posts and category feeds) shows once. With more than one feed, each
// row names its feed.
func (s *Server) sitePosts(r *http.Request, userID string, feeds []siteFeedRow) ([]articleRow, error) {
	var catalog, live []string
	byID := make(map[string]siteFeedRow, len(feeds))
	for _, f := range feeds {
		byID[f.Feed.ID] = f
		if f.Gone() {
			continue
		}
		live = append(live, f.Feed.ID)
		if f.Facts.Section.ID != "" {
			catalog = append(catalog, f.Feed.ID)
		}
	}
	ids := live
	if len(catalog) > 0 {
		ids = catalog
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// Twice the limit, so copies dropped below still leave a full list.
	articles, err := s.services.ArticleService.ListByFeeds(r.Context(), ids, rss.ArticleCursor{}, 2*sitePostsLimit)
	if err != nil {
		return nil, err
	}
	posts := make([]articleRow, 0, sitePostsLimit)
	seen := map[string]bool{}
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
		row := articleRow{Article: a, ReturnTo: r.URL.RequestURI()}
		f := byID[a.FeedID]
		if len(feeds) > 1 {
			// Named as Follow names it, site and feed title, so each post
			// says which feed it's from.
			row.Feed = f.Feed
			row.Feed.Title = f.Follow.Label
			row.Icon = f.Icon
		}
		if !f.Follow.Following {
			follow := f.Follow
			follow.ReturnTo = r.URL.RequestURI() + "#article-" + a.ID
			row.Follow = &follow
		}
		posts = append(posts, row)
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

// canRecheck reports whether a new lookup of the site may be requested: there
// is none yet, or at now the last one finished longer ago than the TTL that
// LookupStore.Request enforces for its result.
func canRecheck(l rss.Lookup, now time.Time) bool {
	age := now.Sub(l.FinishedAt)
	switch l.Status {
	case "":
		return true
	case rss.LookupDone:
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
