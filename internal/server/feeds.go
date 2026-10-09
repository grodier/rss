package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// feedArticleLimit is how many articles the feed page lists.
const feedArticleLimit = 50

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

	data := struct {
		Sites            []siteSubscriptions
		Flash            string
		FailingThreshold int
	}{
		Sites:            sites,
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

	articles, err := s.services.ArticleService.ListByFeed(r.Context(), feed.ID, feedArticleLimit)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	subscribed, err := s.services.SubscriptionService.SubscribedFeedIDs(r.Context(), userID, []string{feed.ID})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	flash := s.sessionManager.PopString(r.Context(), "flash")

	data := struct {
		Feed           rss.Feed
		Site           rss.Site
		Articles       []rss.Article
		Subscribed     bool
		RefreshPending bool
		Flash          string
	}{
		Feed:           feed,
		Site:           site,
		Articles:       articles,
		Subscribed:     subscribed[feed.ID],
		RefreshPending: pending,
		Flash:          flash,
	}

	if err := s.renderHTML(w, http.StatusOK, "feed.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

type subscriptionForm struct {
	// ReturnTo is the local path to redirect back to, usually the page the
	// form was posted from.
	ReturnTo string `form:"return_to"`
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
func (s *Server) subscriptionTarget(w http.ResponseWriter, r *http.Request) (feed rss.Feed, form subscriptionForm, ok bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return rss.Feed{}, subscriptionForm{}, false
	}

	if err := s.decodePostForm(r, &form); err != nil {
		s.serverErrorHTML(w, r, err)
		return rss.Feed{}, subscriptionForm{}, false
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Feed{}, subscriptionForm{}, false
	}
	return feed, form, true
}

// redirectWithFlash flashes msg and sends the user back to the page they
// posted from, or to the feed page if that isn't a local path.
func (s *Server) redirectWithFlash(w http.ResponseWriter, r *http.Request, form subscriptionForm, feed rss.Feed, msg string) {
	s.sessionManager.Put(r.Context(), "flash", msg)
	http.Redirect(w, r, safeReturnPath(form.ReturnTo, "/feeds/"+feed.ID), http.StatusSeeOther)
}

// feedLabel is how messages refer to a feed: its title, else its URL.
func feedLabel(feed rss.Feed) string {
	if feed.Title != "" {
		return feed.Title
	}
	return feed.Url
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
