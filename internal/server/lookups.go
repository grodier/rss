package server

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

const (
	lookupDoneTTL      = 24 * time.Hour
	lookupFailedTTL    = time.Hour
	lookupPollInterval = 250 * time.Millisecond
)

type lookupForm struct {
	Q                   string `form:"q"`
	validator.Validator `form:"-"`
}

// lookupCreateHandler queues a lookup of the site the user searched for and
// waits up to s.lookupWait for it to finish. It redirects to the site page if
// the lookup found one in time, and to the lookup's status page otherwise.
func (s *Server) lookupCreateHandler(w http.ResponseWriter, r *http.Request) {
	var form lookupForm
	if err := s.decodePostForm(r, &form); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	u, err := discovery.ParseInput(form.Q)
	if err != nil {
		form.AddFieldError("q", "Enter a website address like example.com")
		data := searchData{
			Form:  searchForm(form),
			Flash: s.sessionManager.PopString(r.Context(), "flash"),
		}
		if err := s.renderHTML(w, http.StatusUnprocessableEntity, "search.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if ok, retryAfter := s.lookupLimiter.AllowWithRetry(userID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		s.tooManyRequestsResponse(w, r)
		return
	}

	lookup, err := s.services.LookupService.Request(r.Context(), discovery.SiteKey(u), u.String(), lookupDoneTTL, lookupFailedTTL)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	lookup, err = s.waitForLookup(r, lookup)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	if lookup.Status == rss.LookupDone && lookup.SiteID != "" {
		http.Redirect(w, r, "/sites/"+lookup.SiteID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/lookups/"+lookup.ID, http.StatusSeeOther)
}

// waitForLookup polls lookup until it is done or failed, s.lookupWait has
// passed, or the request is canceled, and returns its latest state.
func (s *Server) waitForLookup(r *http.Request, lookup rss.Lookup) (rss.Lookup, error) {
	deadline := time.Now().Add(s.lookupWait)
	for lookupInProgress(lookup) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		t := time.NewTimer(min(lookupPollInterval, remaining))
		select {
		case <-r.Context().Done():
			t.Stop()
			return lookup, nil
		case <-t.C:
		}

		var err error
		lookup, err = s.services.LookupService.GetByID(r.Context(), lookup.ID)
		if err != nil {
			return rss.Lookup{}, err
		}
	}
	return lookup, nil
}

func lookupInProgress(l rss.Lookup) bool {
	return l.Status == rss.LookupPending || l.Status == rss.LookupRunning
}

// lookupHandler shows a lookup's status. While the lookup is in progress the
// page refreshes itself; once it has found a site it redirects there.
func (s *Server) lookupHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	lookup, err := s.services.LookupService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	if lookup.Status == rss.LookupDone && lookup.SiteID != "" {
		http.Redirect(w, r, "/sites/"+lookup.SiteID, http.StatusSeeOther)
		return
	}

	// lookup.Error is internal detail and must never reach the template.
	data := struct {
		ID         string
		SiteKey    string
		InProgress bool
		Failed     bool
		Flash      string
	}{
		ID:         lookup.ID,
		SiteKey:    lookup.SiteKey,
		InProgress: lookupInProgress(lookup),
		Failed:     lookup.Status == rss.LookupFailed,
		Flash:      s.sessionManager.PopString(r.Context(), "flash"),
	}

	if err := s.renderHTML(w, http.StatusOK, "lookup.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
