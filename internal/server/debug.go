package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
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

// inspectTimeout bounds an inspector's outbound work, like a lookup job.
const inspectTimeout = 30 * time.Second

// inspectBodyMax is how much of a response body an inspector shows.
const inspectBodyMax = 256 << 10

// inspectItemMax is how much of an item's source, gofeed JSON, summary or
// content the feed inspector shows.
const inspectItemMax = 20000

// requireDebug answers 404 unless debugEnabled. For routes under /debug/.
func (s *Server) requireDebug(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.debugEnabled(r) {
			s.notFoundResponse(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extendWriteDeadline lets an inspector respond after the server's
// WriteTimeout: it sets this response's write deadline to inspectTimeout
// plus 5 s from now.
func extendWriteDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(inspectTimeout + 5*time.Second))
}

type feedInspectForm struct {
	Conditional bool   `form:"conditional"` // send the stored ETag and Last-Modified
	Item        string `form:"item"`        // external ID of an item to show first (#329); may be ""
}

// inspectResponse is the response an inspector got.
type inspectResponse struct {
	URL         string // final, after redirects
	Status      string
	Duration    string
	Size        string
	ContentType string
	Sniff       string
	Headers     []debugField // sorted by name
	Body        string       // cut at inspectBodyMax
	BodyNote    string       // says the body was cut; "" if it wasn't
}

// inspectMetaRow is a feed-level value as fetched and as stored.
type inspectMetaRow struct {
	Name    string
	Fetched string
	Stored  string
}

// inspectItem is an item on the feed inspector page.
type inspectItem struct {
	ExternalID string
	Outcome    string       // what a refresh would do with it (see itemOutcome)
	Title      string       // its parsed title; may be ""
	Fields     []debugField // what feedparse.Parse returns for it
	Stored     []debugField // the stored values that differ; nil if none
	Raw        string       // its source; "" if it couldn't be matched
	Gofeed     string       // as gofeed parsed it
	Open       bool
}

// feedInspectView is the feed inspector page.
type feedInspectView struct {
	Nav        navData
	Feed       rss.Feed
	FetchedAt  string
	Request    []debugField
	Response   *inspectResponse // nil if the fetch failed
	Error      string           // why there's nothing (more) to show; "" if the feed was parsed
	Meta       []inspectMetaRow
	Channel    []feedparse.Field
	ItemFields []feedparse.Field
	ItemTotal  int
	Focus      *inspectItem // the item named by the form's item; nil if none
	Items      []inspectItem
}

// feedInspectHandler fetches a feed live and shows the response, the fields
// of the document and what a refresh would do with each item. It saves
// nothing: no articles, no fetch outcome, no validators.
func (s *Server) feedInspectHandler(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")
	if !validator.Matches(feedID, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	var form feedInspectForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
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

	if err := extendWriteDeadline(w); err != nil {
		// The fetch may still finish within WriteTimeout.
		s.logError(r, fmt.Errorf("extend write deadline: %w", err))
	}
	ctx, cancel := context.WithTimeout(r.Context(), inspectTimeout)
	defer cancel()

	header := http.Header{}
	if form.Conditional {
		if feed.ETag != "" {
			header.Set("If-None-Match", feed.ETag)
		}
		if feed.LastModified != "" {
			header.Set("If-Modified-Since", feed.LastModified)
		}
	}

	start := time.Now()
	view := feedInspectView{
		Nav:       s.nav(r, ""),
		Feed:      feed,
		FetchedAt: start.UTC().Format(time.RFC3339),
		Request:   inspectRequestFields(feed.Url, header),
	}
	resp, err := s.services.Fetcher.Get(ctx, feed.Url, header)
	elapsed := time.Since(start)
	switch {
	case err != nil:
		view.Error = "Fetch failed: " + err.Error()
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		view.Response = newInspectResponse(resp, elapsed)
		view.Error = view.Response.Status
	default:
		view.Response = newInspectResponse(resp, elapsed)
		if err := s.inspectFeed(ctx, &view, resp, form.Item); err != nil {
			if !errors.Is(err, feedparse.ErrNotFeed) {
				s.serverErrorHTML(w, r, err)
				return
			}
			view.Error = "Not a feed: " + err.Error()
		}
	}

	if err := s.renderHTML(w, http.StatusOK, "debug_feed.html", view); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// inspectFeed fills view from a 2xx response: the parsed document, and each
// item with what a refresh would do with it, the item whose external ID is
// focus first. Errors wrap feedparse.ErrNotFeed when the body isn't a feed.
func (s *Server) inspectFeed(ctx context.Context, view *feedInspectView, resp *fetch.Response, focus string) error {
	insp, err := feedparse.Inspect(resp.URL, resp.Body)
	if err != nil {
		return err
	}

	articles := ingest.Articles(insp.Feed.Items)
	kept := make(map[string]rss.Article, len(articles))
	ids := make([]string, len(articles))
	for i, a := range articles {
		kept[a.ExternalID] = a
		ids[i] = a.ExternalID
	}
	stored, err := s.services.DebugService.ArticlesByExternalID(ctx, view.Feed.ID, ids)
	if err != nil {
		return err
	}

	m := insp.Feed.Meta
	view.Meta = []inspectMetaRow{
		{Name: "format", Fetched: m.Format},
		{Name: "title", Fetched: m.Title, Stored: view.Feed.Title},
		{Name: "description", Fetched: m.Description, Stored: view.Feed.Description},
		{Name: "site URL", Fetched: m.SiteURL, Stored: view.Feed.SiteUrl},
	}
	view.Channel = insp.Channel
	view.ItemFields = insp.ItemFields
	view.ItemTotal = len(insp.Items)

	seen := map[string]bool{}
	for _, it := range insp.Items {
		id := ingest.ExternalID(it.Item)
		item := inspectItem{
			ExternalID: id,
			Title:      it.Item.Title,
			Fields:     parsedItemFields(it.Item),
			Raw:        cutText(it.Raw, inspectItemMax),
			Gofeed:     cutText(it.Gofeed, inspectItemMax),
		}
		a, isKept := kept[id]
		switch {
		case id == "":
			item.Outcome = "dropped: no ID, link or text"
		case seen[id]:
			item.Outcome = "dropped: duplicate of an earlier item"
		case !isKept:
			item.Outcome = fmt.Sprintf("dropped: over the %d-article limit", ingest.MaxArticles)
		default:
			st, ok := stored[id]
			diffs := compareArticle(a, st)
			switch {
			case !ok:
				item.Outcome = "new"
			case len(diffs) == 0:
				item.Outcome = "unchanged"
			default:
				item.Outcome = "differs: " + strings.Join(diffs, ", ")
				item.Stored = storedDiffFields(st, diffs)
			}
		}
		if id != "" {
			seen[id] = true
		}

		if focus != "" && id == focus && view.Focus == nil {
			item.Open = true
			view.Focus = &item
			continue
		}
		view.Items = append(view.Items, item)
	}
	return nil
}

// inspectRequestFields describes an inspector's request: its URL and the
// conditional headers sent ("none" if none).
func inspectRequestFields(rawURL string, header http.Header) []debugField {
	fields := []debugField{{Name: "url", Value: rawURL}}
	sent := false
	for _, name := range []string{"If-None-Match", "If-Modified-Since"} {
		if v := header.Get(name); v != "" {
			fields = append(fields, debugField{Name: name, Value: v})
			sent = true
		}
	}
	if !sent {
		fields = append(fields, debugField{Name: "conditional headers", Value: "none"})
	}
	return fields
}

// newInspectResponse describes resp, which took elapsed.
func newInspectResponse(resp *fetch.Response, elapsed time.Duration) *inspectResponse {
	ct := resp.Header.Get("Content-Type")
	out := &inspectResponse{
		Status:      fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		Duration:    elapsed.Round(time.Millisecond).String(),
		Size:        debugBytes(len(resp.Body)),
		ContentType: ct,
		Sniff:       "not a feed",
		Body:        cutText(string(resp.Body), inspectBodyMax),
	}
	if resp.URL != nil {
		out.URL = resp.URL.String()
	}
	if feedparse.Sniff(ct, resp.Body) {
		out.Sniff = "feed"
	}
	if len(resp.Body) > inspectBodyMax {
		out.BodyNote = fmt.Sprintf("Showing the first %d KiB of %s.", inspectBodyMax>>10, debugBytes(len(resp.Body)))
	}
	names := make([]string, 0, len(resp.Header))
	for name := range resp.Header {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		out.Headers = append(out.Headers, debugField{Name: name, Value: strings.Join(resp.Header.Values(name), ", ")})
	}
	return out
}

// cutText returns s cut at max bytes on a UTF-8 boundary, with invalid
// UTF-8 replaced.
func cutText(s string, max int) string {
	if len(s) > max {
		i := max
		for i > 0 && !utf8.RuneStart(s[i]) {
			i--
		}
		s = s[:i]
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// parsedItemFields are the fields of a parsed item, as feedparse.Item names
// them.
func parsedItemFields(it feedparse.Item) []debugField {
	published := ""
	if !it.Published.IsZero() {
		published = it.Published.UTC().Format(time.RFC3339)
	}
	return []debugField{
		{Name: "ID", Value: it.ID},
		{Name: "URL", Value: it.URL},
		{Name: "ImageURL", Value: it.ImageURL},
		{Name: "MediaType", Value: it.MediaType},
		{Name: "Title", Value: it.Title},
		{Name: "Summary", Value: cutText(it.Summary, inspectItemMax), Note: debugBytes(len(it.Summary))},
		{Name: "Content", Value: cutText(it.Content, inspectItemMax), Note: debugBytes(len(it.Content))},
		{Name: "Published", Value: published},
		{Name: "PublishedDateOnly", Value: strconv.FormatBool(it.PublishedDateOnly)},
	}
}

// compareArticle returns the columns in which a fetched article differs from
// the stored one, in column order. A changed summary or content is reported
// with its lengths, before and after ("summary (120 → 98 bytes)").
func compareArticle(fetched, stored rss.Article) []string {
	var diffs []string
	text := func(name, f, s string) {
		if f != s {
			diffs = append(diffs, name)
		}
	}
	long := func(name, f, s string) {
		if f != s {
			diffs = append(diffs, fmt.Sprintf("%s (%d → %d bytes)", name, len(s), len(f)))
		}
	}
	text("url", fetched.URL, stored.URL)
	text("canonical_url", fetched.CanonicalURL, stored.CanonicalURL)
	text("image_url", fetched.ImageURL, stored.ImageURL)
	text("media_type", fetched.MediaType, stored.MediaType)
	text("title", fetched.Title, stored.Title)
	long("summary", fetched.Summary, stored.Summary)
	long("content", fetched.Content, stored.Content)
	text("excerpt", fetched.Excerpt, stored.Excerpt)
	// Postgres keeps microseconds.
	if !fetched.PublishedAt.Truncate(time.Microsecond).Equal(stored.PublishedAt) {
		diffs = append(diffs, "published_at")
	}
	if fetched.PublishedDateOnly != stored.PublishedDateOnly {
		diffs = append(diffs, "published_date_only")
	}
	return diffs
}

// storedDiffFields are the stored values of the columns compareArticle
// reported; summary and content by their length.
func storedDiffFields(stored rss.Article, diffs []string) []debugField {
	fields := make([]debugField, 0, len(diffs))
	for _, d := range diffs {
		name, _, _ := strings.Cut(d, " ")
		var v string
		switch name {
		case "url":
			v = stored.URL
		case "canonical_url":
			v = stored.CanonicalURL
		case "image_url":
			v = stored.ImageURL
		case "media_type":
			v = stored.MediaType
		case "title":
			v = stored.Title
		case "summary":
			v = debugBytes(len(stored.Summary))
		case "content":
			v = debugBytes(len(stored.Content))
		case "excerpt":
			v = stored.Excerpt
		case "published_at":
			if !stored.PublishedAt.IsZero() {
				v = stored.PublishedAt.UTC().Format(time.RFC3339)
			}
		case "published_date_only":
			v = strconv.FormatBool(stored.PublishedDateOnly)
		}
		fields = append(fields, debugField{Name: name, Value: v})
	}
	return fields
}
