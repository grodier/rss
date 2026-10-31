package server

import (
	"net/http"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// timelinePageSize is how many articles a timeline page shows.
const timelinePageSize = 30

func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticatedUserID(r)
	if !ok {
		data := struct{ Flash string }{Flash: s.sessionManager.PopString(r.Context(), "flash")}
		if err := s.renderHTML(w, http.StatusOK, "home.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	s.renderTimeline(w, r, userID, rss.Timeline{})
}

// timelineTabs is the data of the timeline-tabs partial: Home, then the
// user's custom timelines, with the one shown (CurrentID, "" for Home)
// marked.
type timelineTabs struct {
	Timelines []rss.Timeline
	CurrentID string
}

// renderTimeline renders timeline.html: a page of current's articles for
// userID (Home when current has no ID), with the tab row.
func (s *Server) renderTimeline(w http.ResponseWriter, r *http.Request, userID string, current rss.Timeline) {
	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
		return
	}

	timelines, err := s.services.TimelineService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// The mark orders the timeline (see ListTimeline) and places the "You
	// were here" line; starred-only timelines, ordered by star time, have
	// neither.
	now := time.Now()
	var anchors rss.TimelineAnchors
	if !current.Filter.StarredOnly {
		anchors = s.timelineAnchors(r, userID, now)
	}

	rows, next, err := s.timelineRows(r, userID, current.Filter, anchors, before)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if err := s.fillRead(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	if err := s.fillSaved(r.Context(), userID, rows); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.fillDebug(r, rows)
	s.fillIcons(r, rows)

	// The position the page's beacon reports (first pages only) is the
	// newest on the page, which needn't be the first row: a late arrival can
	// go first. Starred-only timelines don't report one.
	var seenUpTo time.Time
	if !current.Filter.StarredOnly {
		fillDivider(rows, before)
		if before.IsZero() {
			for _, row := range rows {
				if row.Article.TimelineAt.After(seenUpTo) {
					seenUpTo = row.Article.TimelineAt
				}
			}
		}
	}

	home := current.ID == ""
	path := "/"
	if !home {
		path = "/timelines/" + current.ID
	}
	filter := current.Filter

	data := struct {
		Nav           navData
		Flash         string
		ConfirmNotice *confirmNotice
		Timeline      rss.Timeline
		Home          bool // current is Home
		Tabs          timelineTabs
		Path          string // the timeline's URL, for the pager
		NoSources     bool   // it shows only some feeds but has none to show
		Rows          []articleRow
		Before        bool
		Next          string
		SeenUpTo      time.Time // the position app.js reports when the page is hidden; zero for none
		SeenAsOf      time.Time // when the page was rendered, which app.js reports with SeenUpTo
	}{
		Nav:           s.nav(r, "home"),
		Timeline:      current,
		Home:          home,
		Tabs:          timelineTabs{Timelines: timelines, CurrentID: current.ID},
		Path:          path,
		NoSources:     filter.OnlyIncluded && len(filter.IncludeTagIDs) == 0 && len(filter.IncludeFeedIDs) == 0,
		Flash:         s.sessionManager.PopString(r.Context(), "flash"),
		ConfirmNotice: s.confirmNotice(r),
		Rows:          rows,
		Before:        !before.IsZero(),
		Next:          next,
		SeenUpTo:      seenUpTo,
		SeenAsOf:      now,
	}

	if err := s.renderHTML(w, http.StatusOK, "timeline.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelineAnchors returns the anchors of userID's timeline for a page
// rendered at now (see rss.TimelineMark.Anchors). The mark only orders the
// timeline: on a store error it logs and returns zero anchors, so the page
// shows publish order without a line.
func (s *Server) timelineAnchors(r *http.Request, userID string, now time.Time) rss.TimelineAnchors {
	mark, err := s.services.ReadService.TimelineMark(r.Context(), userID)
	if err != nil {
		s.logError(r, err)
		return rss.TimelineAnchors{}
	}
	return mark.Anchors(now)
}

// fillDivider sets Divider on the row the "You were here" line goes above:
// the first one in group 0, if something is above it (an earlier row in
// group 1 or 2, or the page before, when before is in group 1 or 2).
func fillDivider(rows []articleRow, before rss.ArticleCursor) {
	newer := !before.IsZero() && before.Group > 0
	for i := range rows {
		if rows[i].Group == 0 {
			rows[i].Divider = newer
			return
		}
		newer = true
	}
}

// timelineSeenHandler records the newest timeline position the reader has
// had on screen and when that page was rendered, sent by app.js's beacon
// when a timeline page is hidden. It moves the "You were here" divider at
// the reader's next visit, and what a reload puts first.
func (s *Server) timelineSeenHandler(w http.ResponseWriter, r *http.Request) {
	var form struct {
		UpTo string `form:"up_to"`
		AsOf string `form:"as_of"`
	}
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	upTo, err := time.Parse(time.RFC3339Nano, form.UpTo)
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That timeline position isn't valid.")
		return
	}
	asOf, err := time.Parse(time.RFC3339Nano, form.AsOf)
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That timeline position isn't valid.")
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.ReadService.MarkSeen(r.Context(), userID, upTo, asOf); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// timelineRows returns a page of the articles filter shows userID, grouped
// by anchors, starting after before, and the cursor of the next page ("" if
// it's the last). A starred-only timeline lists stars, newest starred first,
// with Starred set; any other has Starred still to fill.
func (s *Server) timelineRows(r *http.Request, userID string, filter rss.TimelineFilter, anchors rss.TimelineAnchors, before rss.ArticleCursor) ([]articleRow, string, error) {
	next := ""
	if filter.StarredOnly {
		items, err := s.services.StarService.ListStarred(r.Context(), userID, filter, before, timelinePageSize+1)
		if err != nil {
			return nil, "", err
		}
		if len(items) > timelinePageSize {
			items = items[:timelinePageSize]
			last := items[len(items)-1]
			next = encodeCursor(rss.ArticleCursor{At: last.StarredAt, ID: last.Article.ID})
		}
		rows := make([]articleRow, len(items))
		for i, item := range items {
			rows[i] = articleRow{Article: item.Article, Feed: item.Feed, Starred: true, ReturnTo: r.URL.RequestURI()}
		}
		return rows, next, nil
	}

	items, err := s.services.ArticleService.ListTimeline(r.Context(), userID, filter, anchors, before, timelinePageSize+1)
	if err != nil {
		return nil, "", err
	}
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1]
		next = encodeCursor(rss.ArticleCursor{Group: last.Group, At: last.Article.TimelineAt, ID: last.Article.ID})
	}
	rows := make([]articleRow, len(items))
	for i, item := range items {
		rows[i] = articleRow{Article: item.Article, Feed: item.Feed, AlsoIn: item.AlsoIn, Group: item.Group, ReturnTo: r.URL.RequestURI()}
	}
	if err := s.fillStarred(r.Context(), userID, rows); err != nil {
		return nil, "", err
	}
	return rows, next, nil
}
