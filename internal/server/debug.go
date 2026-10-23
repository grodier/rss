package server

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// debugAllowed reports whether the logged-in user's email is in
// Config.DebugEmails.
func (s *Server) debugAllowed(r *http.Request) bool {
	if len(s.config.DebugEmails) == 0 {
		return false
	}
	sess, ok := s.authenticatedSession(r)
	if !ok || sess.User.Email == "" {
		return false
	}
	return slices.ContainsFunc(s.config.DebugEmails, func(e string) bool {
		return strings.EqualFold(e, sess.User.Email)
	})
}

// debugEnabled reports whether this request shows debug panels: the user
// is debugAllowed and turned debug mode on on this device ("debug" in the
// session).
func (s *Server) debugEnabled(r *http.Request) bool {
	return s.debugAllowed(r) && s.sessionManager.GetBool(r.Context(), "debug")
}

type debugForm struct {
	On string `form:"on"` // "1" turns debug mode on; anything else turns it off
}

// settingsDebugFormHandler turns debug mode on or off for this device. Only
// debugAllowed users can see it exists.
func (s *Server) settingsDebugFormHandler(w http.ResponseWriter, r *http.Request) {
	if !s.debugAllowed(r) {
		s.notFoundResponse(w, r)
		return
	}

	var form debugForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	if form.On == "1" {
		s.sessionManager.Put(r.Context(), "debug", true)
		s.sessionManager.Put(r.Context(), "flash", "Debug mode is on for this device.")
	} else {
		s.sessionManager.Remove(r.Context(), "debug")
		s.sessionManager.Put(r.Context(), "flash", "Debug mode is off for this device.")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// debugField is one row of a debug panel.
type debugField struct {
	Name  string // the column's name, or a derived value's name in words
	Value string // formatted; "" shows as "—"
	Note  string // where it comes from or what it means; may be ""
}

// debugPanel is what the debug-panel partial shows: its title, its fields,
// and for a site, one line per feed.
type debugPanel struct {
	Title  string
	Fields []debugField
	Feeds  []debugField
}

// newDebugPanel is the debugPanel template function: a panel titled title
// with fields and, optionally, a site's feed lines.
func newDebugPanel(title string, fields []debugField, feeds ...[]debugField) debugPanel {
	p := debugPanel{Title: title, Fields: fields}
	for _, f := range feeds {
		p.Feeds = append(p.Feeds, f...)
	}
	return p
}

// siteDebugView is the site page's debug panel: the site's fields and a line
// for each of its feeds.
type siteDebugView struct {
	Fields []debugField
	Feeds  []debugField
}

// internalNote marks values that are never shown outside debug mode.
const internalNote = "internal; only debug mode shows it"

// debugTime formats t as RFC 3339 in UTC with how long ago or ahead of now
// it is ("2026-10-23T14:02:11Z (3h ago)", "… (in 12m)"); "" if t is zero.
func debugTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	rel := debugDuration(d) + " ago"
	if d < 0 {
		rel = "in " + debugDuration(-d)
	}
	return t.UTC().Format(time.RFC3339) + " (" + rel + ")"
}

// debugDuration formats d (not negative) in its largest whole unit: seconds,
// minutes, hours up to two days, then days.
func debugDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
}

// debugBytes formats a size in bytes, or KiB from 1 KiB.
func debugBytes(n int) string {
	if n < 1024 {
		return strconv.Itoa(n) + " B"
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

// feedStatus is a feed's state in words: gone, failing (N in a row), never
// fetched, or ok.
func feedStatus(f rss.Feed) string {
	switch {
	case !f.GoneAt.IsZero():
		return "gone"
	case f.ConsecutiveFailures > 0:
		return fmt.Sprintf("failing (%d in a row)", f.ConsecutiveFailures)
	case f.LastAttempt.IsZero():
		return "never fetched"
	default:
		return "ok"
	}
}

// siteDebugFields are the rows of a site's debug panel: the site, its lookup
// (l is zero if none) and its icon.
func siteDebugFields(site rss.Site, d rss.SiteDebug, l rss.Lookup, now time.Time) []debugField {
	fields := []debugField{
		{Name: "id", Value: site.ID},
		{Name: "host", Value: site.Host, Note: "the site's key"},
		{Name: "url", Value: site.URL},
		{Name: "title", Value: site.Title},
		{Name: "description", Value: site.Description},
		{Name: "created_at", Value: debugTime(site.CreatedAt, now)},
		{Name: "updated_at", Value: debugTime(site.UpdatedAt, now)},
	}

	if l.ID == "" {
		fields = append(fields, debugField{Name: "lookup", Value: "none", Note: "no lookups row for this host"})
	} else {
		fields = append(fields,
			debugField{Name: "lookup status", Value: string(l.Status)},
			debugField{Name: "lookup url", Value: l.URL},
			debugField{Name: "lookup error", Value: l.Error, Note: internalNote},
			debugField{Name: "lookup attempts", Value: strconv.Itoa(l.Attempts)},
			debugField{Name: "lookup requested_at", Value: debugTime(l.RequestedAt, now)},
			debugField{Name: "lookup started_at", Value: debugTime(l.StartedAt, now)},
			debugField{Name: "lookup finished_at", Value: debugTime(l.FinishedAt, now)},
		)
	}

	fields = append(fields,
		debugField{Name: "icon_checked_at", Value: debugTime(d.IconCheckedAt, now), Note: "last time the icon worker looked"},
		debugField{Name: "icon_next_check_at", Value: debugTime(d.IconNextCheckAt, now)},
	)
	if i := d.Icon; i.SiteID != "" {
		fields = append(fields,
			debugField{Name: "icon source_url", Value: i.SourceURL},
			debugField{Name: "icon type", Value: fmt.Sprintf("%s %d×%d, %s", i.ContentType, i.Width, i.Height, debugBytes(d.IconBytes))},
			debugField{Name: "icon hash", Value: i.Hash},
			debugField{Name: "icon etag", Value: i.ETag},
			debugField{Name: "icon last_modified", Value: i.LastModified},
			debugField{Name: "icon fetched_at", Value: debugTime(i.FetchedAt, now)},
		)
	} else {
		fields = append(fields, debugField{Name: "icon", Value: "none (letter avatar)"})
	}
	return fields
}

// siteFeedDebugLine is a feed's line in its site's debug panel: its title,
// then its status, last attempt and article count.
func siteFeedDebugLine(f rss.Feed, d rss.FeedDebug, now time.Time) debugField {
	attempt := "never attempted"
	if !f.LastAttempt.IsZero() {
		attempt = "last attempt " + debugTime(f.LastAttempt, now)
	}
	return debugField{
		Name:  cmp.Or(f.Title, f.Url),
		Value: fmt.Sprintf("%s · %s · %d articles", feedStatus(f), attempt, d.Articles),
	}
}

// feedDebugFields are the rows of a feed's debug panel: derived values, then
// every column of the feed in schema order.
func feedDebugFields(f rss.Feed, d rss.FeedDebug, now time.Time) []debugField {
	fields := []debugField{{Name: "status", Value: feedStatus(f), Note: "derived from gone_at, consecutive_failures and last_attempt_at"}}
	if !f.RefreshRequestedAt.IsZero() {
		fields = append(fields, debugField{Name: "refresh requested", Value: debugTime(f.RefreshRequestedAt, now), Note: "waiting for a refresh worker"})
	}
	fields = append(fields,
		debugField{Name: "articles", Value: strconv.Itoa(d.Articles)},
		debugField{Name: "newest created_at", Value: debugTime(d.NewestCreatedAt, now), Note: "when the newest article was stored"},
		debugField{Name: "newest timeline_at", Value: debugTime(d.NewestTimelineAt, now)},
		debugField{Name: "enrich jobs", Value: strconv.Itoa(d.EnrichJobs), Note: "articles waiting for their web page's metadata"},

		debugField{Name: "id", Value: f.ID},
		debugField{Name: "site_id", Value: f.SiteID},
		debugField{Name: "url", Value: f.Url},
		debugField{Name: "site_url", Value: f.SiteUrl},
		debugField{Name: "title", Value: f.Title},
		debugField{Name: "description", Value: f.Description},
		debugField{Name: "last_fetched_at", Value: debugTime(f.LastFetched, now), Note: "last successful fetch"},
		debugField{Name: "last_attempt_at", Value: debugTime(f.LastAttempt, now)},
		debugField{Name: "last_error", Value: f.LastError, Note: internalNote},
		debugField{Name: "consecutive_failures", Value: strconv.Itoa(f.ConsecutiveFailures)},
		debugField{Name: "next_fetch_at", Value: debugTime(f.NextFetch, now)},
		debugField{Name: "etag", Value: f.ETag},
		debugField{Name: "last_modified", Value: f.LastModified},
		debugField{Name: "gone_at", Value: debugTime(f.GoneAt, now), Note: "set when the feed answered 410"},
		debugField{Name: "refresh_requested_at", Value: debugTime(f.RefreshRequestedAt, now)},
		debugField{Name: "created_at", Value: debugTime(f.CreatedAt, now)},
	)
	return fields
}
