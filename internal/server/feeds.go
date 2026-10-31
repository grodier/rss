package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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

// siteSubscriptions is one site's feeds on the user's feeds page.
type siteSubscriptions struct {
	Site rss.Site
	Subs []rss.Subscription
}

func (s *Server) feedsHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	subs, err := s.services.SubscriptionService.ListByUser(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// subs is ordered by site, so a site's feeds are consecutive.
	var sites []siteSubscriptions
	for _, sub := range subs {
		if n := len(sites); n > 0 && sites[n-1].Site.ID == sub.Site.ID {
			sites[n-1].Subs = append(sites[n-1].Subs, sub)
			continue
		}
		sites = append(sites, siteSubscriptions{Site: sub.Site, Subs: []rss.Subscription{sub}})
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
	}{
		Nav:              s.nav(r, "feeds"),
		Sites:            sites,
		Icons:            s.pageIcons(r, siteIDs),
		Flash:            s.sessionManager.PopString(r.Context(), "flash"),
		FailingThreshold: failingThreshold,
	}

	if err := s.renderHTML(w, http.StatusOK, "feeds.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

func (s *Server) feedHandler(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")
	if !validator.Matches(feedID, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
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
	// CrossOriginProtection doesn't cover GET (see searchHandler). This write
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
		Subscribed     bool
		RefreshPending bool
		Flash          string
		Debug          []debugField
	}{
		Nav:            s.nav(r, ""),
		Feed:           feed,
		Name:           sub.Title,
		Site:           site,
		Icons:          s.pageIcons(r, []string{site.ID}),
		Rows:           rows,
		Before:         !before.IsZero(),
		Next:           next,
		Subscribed:     subscribed,
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
	feed, form, ok := s.subscriptionTarget(w, r)
	if !ok {
		return
	}

	if !feed.GoneAt.IsZero() {
		s.redirectWithFlash(w, r, form, feed, "This feed no longer exists, so you can't subscribe to it.")
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
	s.redirectWithFlash(w, r, form, feed, "Subscribed to "+feedLabel(feed)+".")
}

// feedUnsubscribeHandler removes the user's subscription to a feed, including
// a gone one. Unsubscribing when not subscribed is a no-op with the same
// message.
func (s *Server) feedUnsubscribeHandler(w http.ResponseWriter, r *http.Request) {
	feed, form, ok := s.subscriptionTarget(w, r)
	if !ok {
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.SubscriptionService.Unsubscribe(r.Context(), userID, feed.ID); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.redirectWithFlash(w, r, form, feed, "Unsubscribed from "+feedLabel(feed)+".")
}

// subscriptionTarget reads what the subscribe and unsubscribe handlers share:
// the feed named in the URL and the posted form. If ok is false it has already
// written the response.
func (s *Server) subscriptionTarget(w http.ResponseWriter, r *http.Request) (feed rss.Feed, form returnForm, ok bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.Feed{}, returnForm{}, false
	}

	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return rss.Feed{}, returnForm{}, false
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Feed{}, returnForm{}, false
	}
	return feed, form, true
}

// redirectWithFlash flashes msg and sends the user back to the page they
// posted from, or to the feed page if that isn't a local path.
func (s *Server) redirectWithFlash(w http.ResponseWriter, r *http.Request, form returnForm, feed rss.Feed, msg string) {
	s.sessionManager.Put(r.Context(), "flash", msg)
	http.Redirect(w, r, safeReturnPath(form.ReturnTo, "/feeds/"+feed.ID), http.StatusSeeOther)
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
	http.Redirect(w, r, safeReturnPath(form.ReturnTo, "/feeds"), http.StatusSeeOther)
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
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
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
	// The back link and Cancel go to My feeds or the feed's page, whichever
	// the form came from, so the link can name it.
	backURL, backLabel := "/feeds/"+sub.Feed.ID, cmp.Or(sub.Title, feedLabel(sub.Feed))
	if form.ReturnTo == "/feeds" {
		backURL, backLabel = "/feeds", "My feeds"
	}
	data := struct {
		Nav       navData
		Sub       rss.Subscription
		Icons     map[string]string
		Tags      []tagOption
		Form      subscriptionForm
		BackURL   string
		BackLabel string
	}{
		Nav:       s.nav(r, "feeds"),
		Sub:       sub,
		Icons:     s.pageIcons(r, []string{sub.Site.ID}),
		Tags:      options,
		Form:      form,
		BackURL:   backURL,
		BackLabel: backLabel,
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
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
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
		msg = "No new articles"
	case 1:
		msg = "1 new article"
	default:
		msg = fmt.Sprintf("%d new articles", res.New)
	}
	if res.Updated > 0 {
		msg += fmt.Sprintf(", %d updated", res.Updated)
	}
	return msg + "."
}
