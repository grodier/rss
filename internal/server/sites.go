package server

import (
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
		Feeds       []rss.Feed
		Subscribed  map[string]bool
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
		Feeds:       feeds,
		Subscribed:  subscribed,
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
