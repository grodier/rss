package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// feedPageSize is how many articles a feed page shows.
const feedPageSize = 30

const (
	// refreshCooldown is how long after a successful fetch a feed can't be
	// refreshed again.
	refreshCooldown = 5 * time.Minute
	// refreshTimeout bounds a refresh. Keep it under the server's
	// WriteTimeout (10s) so the redirect can still be written.
	refreshTimeout = 8 * time.Second
)

// failingThreshold is how many consecutive failed fetches make the feeds
// page warn that a feed is failing, so a single failure doesn't raise an alarm.
const failingThreshold = 3

// siteSubscriptions is one site's feeds on the user's feeds page, or, sorted
// by Recently updated, one feed.
type siteSubscriptions struct {
	Site rss.Site
	Subs []myFeed
	// Grouped gives the site a header over its feeds: it has two or more
	// subscriptions, whether or not they all match the search.
	Grouped bool
	// Hidden is set when none of Subs matches the search.
	Hidden bool
	// AfterGroup puts a double rule above a section that isn't a group when
	// the nearest shown section above it is, so a group's end is marked
	// whichever sections the search hides. feeds-filter.ts sets it the same
	// way as the reader types.
	AfterGroup bool
}

// myFeed is one row of My feeds.
type myFeed struct {
	rss.Subscription
	// Filter is what the search matches, lowercased: the row's name, the
	// feed's title, and the site's title and host, a line each.
	// feeds-filter.ts matches the same text as the reader types.
	Filter string
	// Hidden is set when the row doesn't match the search. Every row is
	// rendered, hidden or not, so the search can change without a reload.
	Hidden bool
}

// feedsSortUpdated is My feeds' sort=updated: the feeds whose latest article
// is newest first, ungrouped. Without it, sites are listed by name.
const feedsSortUpdated = "updated"

// feedsSearchMaxRunes caps My feeds' search query; a longer one is cut, not
// rejected.
const feedsSearchMaxRunes = 200

// feedsURL returns My feeds' URL for the kind, the sort and the query q,
// leaving out whichever is empty.
func feedsURL(kind discovery.Kind, sort, q string) string {
	v := url.Values{}
	if kind != "" {
		v.Set("kind", string(kind))
	}
	if sort != "" {
		v.Set("sort", sort)
	}
	if q != "" {
		v.Set("q", q)
	}
	if len(v) == 0 {
		return "/feeds"
	}
	return "/feeds?" + v.Encode()
}

// feedKind is one of My feeds' kinds (#862), in the order its filter lists
// them.
type feedKind struct {
	Kind  discovery.Kind
	Label string
	// None is the line shown when the reader follows none of this kind.
	None string
}

var feedKinds = []feedKind{
	{discovery.KindWebsites, "Websites", "You don't follow any websites."},
	{discovery.KindVideos, "Videos", "You don't follow any video channels."},
	{discovery.KindSocial, "Social", "You don't follow any social accounts."},
}

// feedKindOf returns the kind named k in a URL, and false for an unknown
// one.
func feedKindOf(k string) (feedKind, bool) {
	for _, fk := range feedKinds {
		if string(fk.Kind) == k {
			return fk, true
		}
	}
	return feedKind{}, false
}

// kindLink is a choice in My feeds' filter.
type kindLink struct {
	Label, URL string
	Current    bool
}

// myFeedFilter is the text My feeds' search matches for sub (myFeed.Filter).
func myFeedFilter(sub rss.Subscription) string {
	name := cmp.Or(sub.Title, sub.Feed.Title, sub.Feed.Url)
	return strings.ToLower(strings.Join([]string{name, sub.Feed.Title, sub.Site.Title, sub.Site.Host}, "\n"))
}

// feedsHandler shows My feeds: the user's subscriptions grouped by site, or,
// with sort=updated, the feeds whose latest article is newest first. A
// search q hides the rows that don't match their name, feed title, site
// title or host. A kind leaves out the other kinds' feeds; the filter that
// chooses it shows when the user's feeds span two or more kinds.
func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	kind, _ := feedKindOf(r.URL.Query().Get("kind"))
	sort := r.URL.Query().Get("sort")
	if sort != feedsSortUpdated {
		sort = ""
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if runes := []rune(q); len(runes) > feedsSearchMaxRunes {
		q = strings.TrimSpace(string(runes[:feedsSearchMaxRunes]))
	}

	subs, err := s.services.SubscriptionService.ListByUser(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	total := len(subs)

	// The kinds the user follows, over all their feeds, then only the
	// chosen kind's feeds.
	has := map[discovery.Kind]bool{}
	for _, sub := range subs {
		has[discovery.KindOf(sub.Site.Host)] = true
	}
	if kind.Kind != "" {
		subs = slices.DeleteFunc(subs, func(sub rss.Subscription) bool {
			return discovery.KindOf(sub.Site.Host) != kind.Kind
		})
	}
	// A user with no feeds at all gets the page's empty state, not a
	// filter, whatever the URL's kind.
	var kinds []kindLink
	if total > 0 && (len(has) > 1 || kind.Kind != "") {
		kinds = append(kinds, kindLink{Label: "All", URL: feedsURL("", sort, q), Current: kind.Kind == ""})
		for _, fk := range feedKinds {
			// The current kind is listed even when the user has none of
			// it, so the filter says where they are.
			if has[fk.Kind] || fk.Kind == kind.Kind {
				kinds = append(kinds, kindLink{Label: fk.Label, URL: feedsURL(fk.Kind, sort, q), Current: fk.Kind == kind.Kind})
			}
		}
	}
	var none string
	if total > 0 && len(subs) == 0 {
		none = kind.None
	}

	var sites []siteSubscriptions
	if sort == feedsSortUpdated {
		// Newest latest article first, feeds without articles last; ties
		// keep ListByUser's order, by site and name.
		slices.SortStableFunc(subs, func(a, b rss.Subscription) int {
			return b.LatestArticleAt.Compare(a.LatestArticleAt)
		})
		for _, sub := range subs {
			sites = append(sites, siteSubscriptions{Site: sub.Site, Subs: []myFeed{{Subscription: sub}}})
		}
	} else {
		// subs is ordered by site, so a site's feeds are consecutive.
		for _, sub := range subs {
			if n := len(sites); n > 0 && sites[n-1].Site.ID == sub.Site.ID {
				sites[n-1].Subs = append(sites[n-1].Subs, myFeed{Subscription: sub})
				sites[n-1].Grouped = true
				continue
			}
			sites = append(sites, siteSubscriptions{Site: sub.Site, Subs: []myFeed{{Subscription: sub}}})
		}
	}

	needle := strings.ToLower(q)
	matches := 0
	for i := range sites {
		site := &sites[i]
		site.Hidden = true
		for j := range site.Subs {
			row := &site.Subs[j]
			row.Filter = myFeedFilter(row.Subscription)
			row.Hidden = !strings.Contains(row.Filter, needle)
			if !row.Hidden {
				site.Hidden = false
				matches++
			}
		}
	}

	afterGroup := false
	for i := range sites {
		if sites[i].Hidden {
			continue
		}
		sites[i].AfterGroup = afterGroup && !sites[i].Grouped
		afterGroup = sites[i].Grouped
	}

	siteIDs := make([]string, len(sites))
	for i, site := range sites {
		siteIDs[i] = site.Site.ID
	}

	data := struct {
		Nav              navData
		Sites            []siteSubscriptions
		Icons            map[string]string
		Flash            string
		FailingThreshold int
		Kind             discovery.Kind
		Sort             string
		Q                string
		// Total is how many feeds the user follows, of every kind.
		Total int
		// Count is how many of them the page lists: those of Kind.
		Count int
		// Matches is how many rows match Q (all of them without one).
		Matches int
		// Kinds is the filter's choices, All first; nil when the user's
		// feeds are all of one kind and none is chosen.
		Kinds []kindLink
		// None is the line shown when the user follows none of Kind.
		None string
		// ByName and ByUpdated are the sort links, keeping Kind and Q.
		ByName, ByUpdated string
		// ReturnTo brings Edit subscription back to this kind and sort.
		ReturnTo string
		Box      searchBox
	}{
		Nav:              s.nav(r, "feeds"),
		Sites:            sites,
		Icons:            s.pageIcons(r, siteIDs),
		Flash:            s.sessionManager.PopString(r.Context(), "flash"),
		FailingThreshold: failingThreshold,
		Kind:             kind.Kind,
		Sort:             sort,
		Q:                q,
		Total:            total,
		Count:            len(subs),
		Matches:          matches,
		Kinds:            kinds,
		None:             none,
		ByName:           feedsURL(kind.Kind, "", q),
		ByUpdated:        feedsURL(kind.Kind, feedsSortUpdated, q),
		ReturnTo:         feedsURL(kind.Kind, sort, ""),
		Box: searchBox{
			ID:          "feeds-q",
			Label:       "Search my feeds",
			Placeholder: "Search my feeds",
			Value:       q,
			ClearURL:    feedsURL(kind.Kind, sort, ""),
			InPlace:     true,
		},
	}

	if err := s.renderHTML(w, http.StatusOK, "feeds.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feedID, ok := s.uuidParam(w, r, "id")
	if !ok {
		return
	}

	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older posts isn't valid.")
		return
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), feedID)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// Viewing a stale feed queues a background refresh, so the page doesn't
	// wait on a fetch. GET handlers otherwise never write, since
	// CrossOriginProtection doesn't cover GET (see discoverHandler). This write
	// is idempotent upkeep rather than a change made on the user's behalf: it
	// can only queue a fetch of a feed that's already stale, and it changes
	// nothing the user sees except freshness, so a forged request gains
	// nothing. Subscriptions aren't checked: for a subscribed feed the request
	// is harmless.
	pending := !feed.RefreshRequestedAt.IsZero()
	if !pending && feed.GoneAt.IsZero() && !feed.NextFetch.After(time.Now()) {
		if err := s.services.FeedService.RequestRefresh(r.Context(), feed.ID); err != nil {
			s.logError(r, err) // the page still renders
		} else {
			pending = true
		}
	}

	site, err := s.services.SiteService.GetByID(r.Context(), feed.SiteID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	articles, err := s.services.ArticleService.ListByFeed(r.Context(), feed.ID, before, feedPageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	next := ""
	if len(articles) > feedPageSize {
		articles = articles[:feedPageSize]
		last := articles[len(articles)-1]
		next = encodeCursor(rss.ArticleCursor{At: last.FeedSortAt(), ID: last.ID})
	}

	rows := make([]articleRow, len(articles))
	for i, a := range articles {
		rows[i] = articleRow{Article: a, ReturnTo: r.URL.RequestURI()}
	}

	userID, _ := s.authenticatedUserID(r)
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

	var subscribed bool
	sub, err := s.services.SubscriptionService.Get(r.Context(), userID, feed.ID)
	switch {
	case err == nil:
		subscribed = true
	case !errors.Is(err, rss.ErrNoRecord):
		s.serverErrorHTML(w, r, err)
		return
	}

	var debug []debugField
	if s.debugEnabled(r) {
		fd, err := s.services.DebugService.FeedDebug(r.Context(), []string{feed.ID})
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		debug = feedDebugFields(feed, fd[feed.ID], time.Now())
	}

	flash := s.sessionManager.PopString(r.Context(), "flash")

	data := struct {
		Nav            navData
		Feed           rss.Feed
		Name           string // the user's name for the feed; "" if none or not subscribed
		Site           rss.Site
		Icons          map[string]string
		Rows           []articleRow
		Before         bool
		Next           string
		Follow         followButton // posted as a plain form: Edit shows only while following
		RefreshPending bool
		Flash          string
		Debug          []debugField
	}{
		Nav:    s.nav(r, ""),
		Feed:   feed,
		Name:   sub.Title,
		Site:   site,
		Icons:  s.pageIcons(r, []string{site.ID}),
		Rows:   rows,
		Before: !before.IsZero(),
		Next:   next,
		Follow: followButton{
			FeedID: feed.ID, Label: cmp.Or(sub.Title, feedLabel(feed)), ReturnTo: "/feeds/" + feed.ID,
			Following: subscribed, Manage: true, Reload: true,
		},
		RefreshPending: pending,
		Flash:          flash,
		Debug:          debug,
	}

	if err := s.renderHTML(w, http.StatusOK, "feed.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// feedSubscribeHandler subscribes the user to a feed. Subscribing again is a
// no-op with the same message.
func (s *Server) feedSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	var form returnForm
	feed, ok := s.subscriptionTarget(w, r, &form)
	if !ok {
		return
	}

	if !feed.GoneAt.IsZero() {
		s.redirectWithFlash(w, r, form, feed, "This feed no longer exists, so you can't follow it.")
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.SubscriptionService.Subscribe(r.Context(), userID, feed.ID); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}
	s.toggledFeed(w, r, form, feed, "Following "+feedLabel(feed)+".")
}

// feedUnsubscribeHandler removes the user's subscription to a feed, including
// a gone one. Unsubscribing when not subscribed is a no-op with the same
// message. A subscription with a name or tags, which unfollowing would
// lose, goes to the confirmation (feedUnfollowHandler) instead, unless the
// form is that confirmation's, or Edit subscription's, whose Unfollow says
// so (confirm=true). With fetch (save-star.ts) that's the same redirect,
// which save-star.ts doesn't follow: it submits the form, so the browser
// does.
func (s *Server) feedUnsubscribeHandler(w http.ResponseWriter, r *http.Request) {
	var form unsubscribeForm
	feed, ok := s.subscriptionTarget(w, r, &form)
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if !form.Confirm {
		sub, err := s.services.SubscriptionService.Get(r.Context(), userID, feed.ID)
		switch {
		case err == nil && losesOnUnfollow(sub):
			u := "/feeds/" + feed.ID + "/unfollow?" + url.Values{"return_to": {httpserve.SafeReturnPath(form.ReturnTo, "/feeds/"+feed.ID)}}.Encode()
			http.Redirect(w, r, u, http.StatusSeeOther)
			return
		case err != nil && !errors.Is(err, rss.ErrNoRecord):
			s.serverErrorHTML(w, r, err)
			return
		}
	}
	if err := s.services.SubscriptionService.Unsubscribe(r.Context(), userID, feed.ID); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.toggledFeed(w, r, returnForm{ReturnTo: form.ReturnTo}, feed, "Unfollowed "+feedLabel(feed)+".")
}

// unsubscribeForm is what Following, Unfollow and the confirmation post.
type unsubscribeForm struct {
	ReturnTo string `form:"return_to"`
	// Confirm is set by the forms that already warn that the name and tags
	// go: the confirmation's and Edit subscription's.
	Confirm bool `form:"confirm"`
}

// losesOnUnfollow reports whether unfollowing sub loses something the
// reader made, its name or tags, so it needs confirming first.
func losesOnUnfollow(sub rss.Subscription) bool {
	return unfollowLoses(sub) != ""
}

// unfollowLoses is what unfollowing sub loses, as the confirmation says it
// ("Your {it} for it will be removed."): "name and tags", "name", "tags",
// or "" for nothing but the follow.
func unfollowLoses(sub rss.Subscription) string {
	switch {
	case sub.Title != "" && len(sub.Tags) > 0:
		return "name and tags"
	case sub.Title != "":
		return "name"
	case len(sub.Tags) > 0:
		return "tags"
	}
	return ""
}

// feedUnfollowHandler is the confirmation before unfollowing a feed whose
// subscription has a name or tags: what goes, then Unfollow, which posts
// to feedUnsubscribeHandler with confirm=true, and Cancel, back to
// return_to. Not following the feed (unfollowed in another tab) is a 404.
func (s *Server) feedUnfollowHandler(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subscriptionFromURL(w, r)
	if !ok {
		return
	}
	data := struct {
		Nav      navData
		Sub      rss.Subscription
		Name     string
		Loses    string // unfollowLoses
		Icons    map[string]string
		ReturnTo string
	}{
		Nav:      s.nav(r, "feeds"),
		Sub:      sub,
		Name:     cmp.Or(sub.Title, feedLabel(sub.Feed)),
		Loses:    unfollowLoses(sub),
		Icons:    s.pageIcons(r, []string{sub.Site.ID}),
		ReturnTo: httpserve.SafeReturnPath(r.URL.Query().Get("return_to"), "/feeds/"+sub.Feed.ID),
	}
	if err := s.renderHTML(w, http.StatusOK, "unfollow.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// toggledFeed answers a successful follow or unfollow: a 204 when save-star.ts
// posted it with fetch, which says what happened itself, else a redirect
// back with msg as the flash.
func (s *Server) toggledFeed(w http.ResponseWriter, r *http.Request, form returnForm, feed rss.Feed, msg string) {
	if fetched(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.redirectWithFlash(w, r, form, feed, msg)
}

// subscriptionTarget reads what the subscribe and unsubscribe handlers share:
// the feed named in the URL, and the posted form, decoded into dst. If ok is
// false it has already written the response.
func (s *Server) subscriptionTarget(w http.ResponseWriter, r *http.Request, dst any) (feed rss.Feed, ok bool) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return rss.Feed{}, false
	}

	if err := s.decodePostForm(w, r, dst); err != nil {
		s.formErrorResponse(w, r, err)
		return rss.Feed{}, false
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Feed{}, false
	}
	return feed, true
}

// redirectWithFlash flashes msg and sends the user back to the page they
// posted from, or to the feed page if that isn't a local path.
func (s *Server) redirectWithFlash(w http.ResponseWriter, r *http.Request, form returnForm, feed rss.Feed, msg string) {
	s.sessionManager.Put(r.Context(), "flash", msg)
	http.Redirect(w, r, httpserve.SafeReturnPath(form.ReturnTo, "/feeds/"+feed.ID), http.StatusSeeOther)
}

type subscriptionForm struct {
	Title               string   `form:"title"`
	Tags                []string `form:"tags"`     // IDs of the ticked tags
	NewTags             string   `form:"new_tags"` // comma-separated names
	ReturnTo            string   `form:"return_to"`
	validator.Validator `form:"-"`
}

// subscriptionTitleMaxChars is the longest name a user can give a subscription.
const subscriptionTitleMaxChars = 100

// subscriptionEditHandler shows the form for editing the user's subscription
// to a feed: the name they give it and its tags.
func (s *Server) subscriptionEditHandler(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subscriptionFromURL(w, r)
	if !ok {
		return
	}
	userID, _ := s.authenticatedUserID(r)
	tags, err := s.services.TagService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	form := subscriptionForm{Title: sub.Title, ReturnTo: r.URL.Query().Get("return_to")}
	for _, tag := range sub.Tags {
		form.Tags = append(form.Tags, tag.ID)
	}
	s.renderSubscriptionEdit(w, r, http.StatusOK, sub, tags, form)
}

// subscriptionEditPostHandler saves the user's name for a feed and replaces
// its tags with the ticked ones plus the new ones typed. A blank name, or one
// equal to the feed's title, is stored as "" so the feed's own title (and
// later changes to it) shows instead.
func (s *Server) subscriptionEditPostHandler(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subscriptionFromURL(w, r)
	if !ok {
		return
	}

	var form subscriptionForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	tags, err := s.services.TagService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// A tag deleted in another tab is ignored rather than an error.
	form.Tags = ownTagIDs(form.Tags, tags)
	newNames := splitTagNames(form.NewTags)

	form.Title = strings.TrimSpace(form.Title)
	form.CheckField(validator.MaxChars(form.Title, subscriptionTitleMaxChars), "title", "Must be 100 characters or fewer.")
	for _, name := range newNames {
		if !validator.MaxChars(name, tagNameMaxChars) {
			form.AddFieldError("new_tags", "Each tag must be 50 characters or fewer.")
			break
		}
	}
	if !form.Valid() {
		s.renderSubscriptionEdit(w, r, http.StatusUnprocessableEntity, sub, tags, form)
		return
	}

	// Tags first: if they're refused (too many), nothing has changed yet.
	if err := s.services.TagService.SetForSubscription(r.Context(), userID, sub.Feed.ID, form.Tags, newNames); err != nil {
		switch {
		case errors.Is(err, rss.ErrTooManyTags):
			form.AddFieldError("new_tags", "You can have up to 100 tags.")
			s.renderSubscriptionEdit(w, r, http.StatusUnprocessableEntity, sub, tags, form)
		case errors.Is(err, rss.ErrNoRecord):
			s.notFoundResponse(w, r)
		default:
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	title := form.Title
	if title == sub.Feed.Title {
		title = ""
	}
	if err := s.services.SubscriptionService.Rename(r.Context(), userID, sub.Feed.ID, title); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.sessionManager.Put(r.Context(), "flash", "Subscription updated.")
	http.Redirect(w, r, httpserve.SafeReturnPath(form.ReturnTo, "/feeds"), http.StatusSeeOther)
}

// ownTagIDs returns the IDs in ids that are of tags, in order and without
// repeats.
func ownTagIDs(ids []string, tags []rss.Tag) []string {
	own := make(map[string]bool, len(tags))
	for _, tag := range tags {
		own[tag.ID] = true
	}
	var kept []string
	for _, id := range ids {
		if own[id] {
			kept = append(kept, id)
			own[id] = false
		}
	}
	return kept
}

// splitTagNames splits comma-separated tag names and normalizes each with
// rss.TagName, dropping empty ones and repeats (ignoring case; the first
// spelling wins).
func splitTagNames(s string) []string {
	var names []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		name := rss.TagName(part)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, name)
	}
	return names
}

// subscriptionFromURL returns the user's subscription to the feed named in the
// URL. If ok is false it has already written the response: 404 if the id isn't
// a UUID or the user doesn't subscribe to the feed.
func (s *Server) subscriptionFromURL(w http.ResponseWriter, r *http.Request) (sub rss.Subscription, ok bool) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return rss.Subscription{}, false
	}

	userID, _ := s.authenticatedUserID(r)
	sub, err := s.services.SubscriptionService.Get(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Subscription{}, false
	}
	return sub, true
}

// tagOption is a checkbox for a tag on the Edit subscription form.
type tagOption struct {
	Tag     rss.Tag
	Checked bool
}

// renderSubscriptionEdit renders subscription_edit.html with status, with a
// checkbox for each of tags, ticked if form.Tags has it.
func (s *Server) renderSubscriptionEdit(w http.ResponseWriter, r *http.Request, status int, sub rss.Subscription, tags []rss.Tag, form subscriptionForm) {
	options := make([]tagOption, len(tags))
	for i, tag := range tags {
		options[i] = tagOption{Tag: tag, Checked: slices.Contains(form.Tags, tag.ID)}
	}
	data := struct {
		Nav      navData
		Sub      rss.Subscription
		Icons    map[string]string
		Tags     []tagOption
		Form     subscriptionForm
		ReturnTo string
	}{
		Nav:      s.nav(r, "feeds"),
		Sub:      sub,
		Icons:    s.pageIcons(r, []string{sub.Site.ID}),
		Tags:     options,
		Form:     form,
		ReturnTo: httpserve.SafeReturnPath(form.ReturnTo, "/feeds"),
	}
	if err := s.renderHTML(w, status, "subscription_edit.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// feedLabel is how messages refer to a feed: its title, else its URL.
func feedLabel(feed rss.Feed) string {
	if feed.Title != "" {
		return feed.Title
	}
	return feed.Url
}

// postingRate describes a feed's posting rate for people.
func postingRate(perWeek float64) string {
	switch {
	case perWeek <= 0:
		return "No posts in the last 30 days"
	case perWeek < 1:
		return "Less than one post a week"
	case perWeek < 14:
		n := math.Round(perWeek)
		if n == 1 {
			return "About 1 post a week"
		}
		return fmt.Sprintf("About %d posts a week", int(n))
	default:
		return fmt.Sprintf("About %d posts a day", int(math.Round(perWeek/7)))
	}
}

func (s *Server) feedRefreshHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := s.uuidParam(w, r, "id")
	if !ok {
		return
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	redirect := func(msg string) {
		s.sessionManager.Put(r.Context(), "flash", msg)
		http.Redirect(w, r, "/feeds/"+feed.ID, http.StatusSeeOther)
	}

	// Check these first so these clicks don't use up the user's limit.
	if !feed.GoneAt.IsZero() {
		redirect("This feed no longer exists, so it can't be refreshed.")
		return
	}
	if !feed.LastFetched.IsZero() && time.Since(feed.LastFetched) < refreshCooldown {
		redirect("This feed was refreshed in the last few minutes. Try again later.")
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if !s.refreshLimiter.Allow(userID) {
		redirect("You've refreshed a lot of feeds recently. Please wait a few minutes and try again.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()
	res, err := s.services.Refresher.Refresh(ctx, feed)
	switch {
	case err == nil:
		redirect(refreshFlash(res))
	case errors.Is(err, ingest.ErrGone):
		s.logger.Info("feed gone", "feed_id", feed.ID, "error", err)
		redirect("This feed no longer exists. We've stopped checking it.")
	case errors.Is(err, ingest.ErrUnreachable):
		s.logger.Info("feed refresh failed", "feed_id", feed.ID, "error", err)
		redirect("Couldn't reach this feed. Try again later.")
	case errors.Is(err, ingest.ErrNotFeed):
		s.logger.Info("feed refresh failed", "feed_id", feed.ID, "error", err)
		redirect("This feed's address didn't return a feed. Try again later.")
	case errors.Is(err, rss.ErrNoRecord):
		s.notFoundResponse(w, r)
	default:
		s.serverErrorHTML(w, r, err)
	}
}

// refreshFlash describes the outcome of a successful refresh.
func refreshFlash(res rss.FetchResult) string {
	var msg string
	switch res.New {
	case 0:
		msg = "No new posts"
	case 1:
		msg = "1 new post"
	default:
		msg = fmt.Sprintf("%d new posts", res.New)
	}
	if res.Updated > 0 {
		msg += fmt.Sprintf(", %d updated", res.Updated)
	}
	return msg + "."
}
