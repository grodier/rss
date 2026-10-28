package server

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

// sharedFeedLimit is how many articles a shared timeline's feed has.
const sharedFeedLimit = 50

// shareTokenRX matches a share token: 43 base64url characters
// (token.Random).
var shareTokenRX = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// sharedURLs returns the absolute URLs of the shared timeline with token:
// its page and its Atom feed. They're built from the base URL, never the
// request's Host.
func (s *Server) sharedURLs(token string) (page, feed string) {
	page = s.absoluteURL("/shared/" + token)
	return page, page + "/feed.atom"
}

// sharedTimelineFromURL returns the shared timeline whose token is in the
// URL, and its owner's ID. If ok is false it has already written the
// response: 404 for a malformed or unknown token.
func (s *Server) sharedTimelineFromURL(w http.ResponseWriter, r *http.Request) (t rss.Timeline, ownerID string, ok bool) {
	token := chi.URLParam(r, "token")
	if !shareTokenRX.MatchString(token) {
		s.notFoundResponse(w, r)
		return rss.Timeline{}, "", false
	}
	t, ownerID, err := s.services.TimelineService.GetShared(r.Context(), token)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return rss.Timeline{}, "", false
	}
	return t, ownerID, true
}

// sharedTimelineHandler shows a shared timeline's landing page: its name
// and description and how to subscribe to its feed, which it advertises. It
// doesn't list articles; the feed does. Public: no session.
func (s *Server) sharedTimelineHandler(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.sharedTimelineFromURL(w, r)
	if !ok {
		return
	}

	_, feedURL := s.sharedURLs(t.ShareToken)
	data := struct {
		Timeline rss.Timeline
		FeedPath string
		FeedURL  string
	}{
		Timeline: t,
		FeedPath: "/shared/" + t.ShareToken + "/feed.atom",
		FeedURL:  feedURL,
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if err := s.renderHTML(w, http.StatusOK, "shared_timeline.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// sharedFeedHandler serves a shared timeline's Atom feed (RFC 4287): its
// newest sharedFeedLimit articles with a link. Public: no session. A
// request whose If-None-Match has the feed's ETag gets a 304.
func (s *Server) sharedFeedHandler(w http.ResponseWriter, r *http.Request) {
	t, ownerID, ok := s.sharedTimelineFromURL(w, r)
	if !ok {
		return
	}
	items, err := s.services.ArticleService.ListShared(r.Context(), ownerID, t.Filter, sharedFeedLimit)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	pageURL, feedURL := s.sharedURLs(t.ShareToken)
	feed, updated := sharedAtomFeed(t, items, pageURL, feedURL)

	// Encode into a buffer first, so an error is a 500 rather than half a
	// document.
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	if err := xml.NewEncoder(&buf).Encode(feed); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	hash := hex.EncodeToString(sum[:16])

	h := w.Header()
	h.Set("Content-Type", "application/atom+xml; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=900")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("ETag", `"`+hash+`"`)
	h.Set("Last-Modified", updated.UTC().Format(http.TimeFormat))

	// Only If-None-Match, not If-Modified-Since: Last-Modified has whole
	// seconds, so a change within the same second would be missed.
	if etagMatches(r.Header.Get("If-None-Match"), hash) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		s.logError(r, err)
	}
}

// The Atom document of a shared timeline; see sharedAtomFeed.
type (
	atomFeed struct {
		XMLName   xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
		ID        string      `xml:"id"`
		Title     atomText    `xml:"title"`
		Subtitle  *atomText   `xml:"subtitle,omitempty"`
		Updated   string      `xml:"updated"`
		Links     []atomLink  `xml:"link"`
		Author    atomPerson  `xml:"author"`
		Generator string      `xml:"generator"`
		Entries   []atomEntry `xml:"entry"`
	}

	atomEntry struct {
		ID        string     `xml:"id"`
		Title     atomText   `xml:"title"`
		Links     []atomLink `xml:"link"`
		Published string     `xml:"published,omitempty"`
		Updated   string     `xml:"updated"`
		Summary   *atomText  `xml:"summary,omitempty"`
		Source    atomSource `xml:"source"`
	}

	atomSource struct {
		ID    string     `xml:"id"`
		Title atomText   `xml:"title"`
		Links []atomLink `xml:"link"`
	}

	// atomText is a text construct; Type is always "text", so readers never
	// take plain text for HTML.
	atomText struct {
		Type string `xml:"type,attr"`
		Text string `xml:",chardata"`
	}

	atomLink struct {
		Rel  string `xml:"rel,attr"`
		Type string `xml:"type,attr,omitempty"`
		Href string `xml:"href,attr"`
	}

	atomPerson struct {
		Name string `xml:"name"`
	}
)

func plainText(s string) atomText {
	return atomText{Type: "text", Text: s}
}

// atomTime formats t as Atom wants: RFC 3339, in UTC.
func atomTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// sharedAtomFeed returns the Atom feed of shared timeline t with items, and
// its updated time: the latest entry's, else the timeline's UpdatedAt. The
// author is the timeline's name, never its owner: Atom requires one.
func sharedAtomFeed(t rss.Timeline, items []rss.SharedArticle, pageURL, feedURL string) (atomFeed, time.Time) {
	feed := atomFeed{
		ID:    pageURL,
		Title: plainText(t.Name),
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: feedURL},
			{Rel: "alternate", Type: "text/html", Href: pageURL},
		},
		Author:    atomPerson{Name: t.Name},
		Generator: "Paper Round",
		Entries:   make([]atomEntry, 0, len(items)),
	}
	if t.Description != "" {
		sub := plainText(t.Description)
		feed.Subtitle = &sub
	}

	updated := t.UpdatedAt
	for i, item := range items {
		entry, entryUpdated := sharedAtomEntry(item, t.Filter.StarredOnly)
		feed.Entries = append(feed.Entries, entry)
		if i == 0 || entryUpdated.After(updated) {
			updated = entryUpdated
		}
	}
	feed.Updated = atomTime(updated)
	return feed, updated
}

// sharedAtomEntry returns item's entry and its updated time: the star time
// for a starred-only timeline, else the published date, else the timeline
// position.
func sharedAtomEntry(item rss.SharedArticle, starredOnly bool) (atomEntry, time.Time) {
	a, f := item.Article, item.Feed

	id := a.CanonicalURL
	if id == "" {
		id = "urn:uuid:" + a.ID
	}
	updated := a.PublishedAt
	switch {
	case starredOnly:
		updated = item.StarredAt
	case updated.IsZero():
		updated = a.TimelineAt
	}

	entry := atomEntry{
		ID:      id,
		Title:   plainText(a.DisplayTitle()),
		Links:   []atomLink{{Rel: "alternate", Href: a.URL}},
		Updated: atomTime(updated),
		Source: atomSource{
			ID:    f.Url,
			Title: plainText(cmp.Or(f.Title, f.Url)),
			Links: []atomLink{{Rel: "alternate", Href: cmp.Or(f.SiteUrl, f.Url)}},
		},
	}
	if !a.PublishedAt.IsZero() {
		entry.Published = atomTime(a.PublishedAt)
	}
	if excerpt := a.DisplayExcerpt(); excerpt != "" {
		sum := plainText(excerpt)
		entry.Summary = &sum
	}
	return entry, updated
}
