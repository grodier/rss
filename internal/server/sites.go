package server

import (
	"errors"
	"net/http"
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

	// No lookup for the site's key means it can be checked now.
	lookup, err := s.services.LookupService.GetBySiteKey(r.Context(), site.Host)
	if err != nil && !errors.Is(err, rss.ErrNoRecord) {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Site        rss.Site
		Feeds       []rss.Feed
		Subscribed  map[string]bool
		LastChecked time.Time
		Checking    bool
		CanRecheck  bool
		LookupID    string
		Flash       string
	}{
		Site:        site,
		Feeds:       feeds,
		Subscribed:  subscribed,
		LastChecked: lookup.FinishedAt,
		Checking:    lookupInProgress(lookup),
		CanRecheck:  canRecheck(lookup, time.Now()),
		LookupID:    lookup.ID,
		Flash:       s.sessionManager.PopString(r.Context(), "flash"),
	}

	if err := s.renderHTML(w, http.StatusOK, "site.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// canRecheck reports whether a new lookup of the site may be requested: there
// is none yet, or at now the last one finished longer ago than the TTL that
// LookupStore.Request enforces for its status.
func canRecheck(l rss.Lookup, now time.Time) bool {
	age := now.Sub(l.FinishedAt)
	switch l.Status {
	case "":
		return true
	case rss.LookupDone:
		return age > lookupDoneTTL
	case rss.LookupFailed:
		return age > lookupFailedTTL
	default:
		return false
	}
}
