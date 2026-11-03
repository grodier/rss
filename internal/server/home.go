package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// timelinePageSize is how many articles a timeline page shows.
const timelinePageSize = 30

// homeName is what users see Home called: its tab, heading and page title,
// and its row on /timelines (the homeName template function). The nav's
// "Home" isn't it: that opens the start tab, whichever it is.
const homeName = "All feeds"

// homeTabID stands for Home where a tab needs an ID: its URL
// (/timelines/all), its row's fragment and move route on /timelines, and
// the tab cookie. Internally Home is the rss.Timeline with no ID.
const homeTabID = "all"

// tabCookie names the cookie that remembers the tab the user was last on,
// for a visit (rss.VisitGap): homeTabID or a custom timeline's ID.
const tabCookie = "tab"

// homeHandler shows the landing page, or, when logged in, the start tab:
// the one the tab cookie names, if it's still one of the user's tabs, else
// the first. It's rendered in place, without a redirect, so opening the app
// costs one request; its own address stays its tab's (see renderTimeline).
func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticatedUserID(r)
	if !ok {
		data := struct{ Flash string }{Flash: s.sessionManager.PopString(r.Context(), "flash")}
		if err := s.renderHTML(w, http.StatusOK, "home.html", data); err != nil {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	tabs, err := s.services.TimelineService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	var start rss.Timeline // Home, should the list be empty
	if len(tabs) > 0 {
		start = tabs[0]
	}
	if c, err := r.Cookie(tabCookie); err == nil {
		for _, t := range tabs {
			if tabID(t) == c.Value {
				start = t
				break
			}
		}
	}
	if start.ID != "" {
		// List has no filters. A timeline deleted since falls back to Home.
		t, err := s.services.TimelineService.Get(r.Context(), userID, start.ID)
		switch {
		case err == nil:
			start = t
		case errors.Is(err, rss.ErrNoRecord):
			start = rss.Timeline{}
		default:
			s.serverErrorHTML(w, r, err)
			return
		}
	}
	s.renderTimelineWith(w, r, userID, start, tabs)
}

// allTimelineHandler shows Home at its own address, /timelines/all, since
// / shows the start tab, which may be another.
func (s *Server) allTimelineHandler(w http.ResponseWriter, r *http.Request) {
	userID, _ := s.authenticatedUserID(r)
	s.renderTimeline(w, r, userID, rss.Timeline{})
}

// tabID returns t's ID, or homeTabID for Home.
func tabID(t rss.Timeline) string {
	if t.ID == "" {
		return homeTabID
	}
	return t.ID
}

// timelineTabs is the data of the timeline-tabs partial: the user's tabs
// in order, Home among them, with the one shown (CurrentID, "" for Home)
// marked.
type timelineTabs struct {
	Timelines []rss.Timeline
	CurrentID string
}

// renderTimeline renders timeline.html: a page of current's articles for
// userID (Home when current has no ID), with the tab row.
func (s *Server) renderTimeline(w http.ResponseWriter, r *http.Request, userID string, current rss.Timeline) {
	tabs, err := s.services.TimelineService.List(r.Context(), userID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	s.renderTimelineWith(w, r, userID, current, tabs)
}

// renderTimelineWith is renderTimeline with the user's tabs already read.
// Every timeline page sets the tab cookie to current, for the rest of the
// visit, so / comes back to it. That's upkeep in the browser, not a write:
// a session value would be one, since sessions are stored in Postgres.
func (s *Server) renderTimelineWith(w http.ResponseWriter, r *http.Request, userID string, current rss.Timeline, tabs []rss.Timeline) {
	before, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That link to older articles isn't valid.")
		return
	}
	// The timeline's own line places "You were here"; starred-only
	// timelines, ordered by star time, have none.
	var line time.Time
	if !current.Filter.StarredOnly {
		line = s.timelineDivider(r, userID, current.ID, time.Now())
	}

	rows, next, err := s.timelineRows(r, userID, current.Filter, before)
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
	// newest on the page: the first row's. Starred-only timelines don't
	// report one.
	var seenUpTo time.Time
	if !current.Filter.StarredOnly {
		fillDivider(rows, before, line)
		if before.IsZero() && len(rows) > 0 {
			seenUpTo = rows[0].Position
		}
	}

	home := current.ID == ""
	path := "/timelines/" + tabID(current)
	filter := current.Filter

	data := struct {
		Nav           navData
		Flash         string
		ConfirmNotice *confirmNotice
		Timeline      rss.Timeline
		Home          bool // current is Home
		Tabs          timelineTabs
		TabID         string // the timeline the beacon reports for: homeTabID or its ID
		Path          string // the timeline's URL, for the pager
		NoSources     bool   // it shows only some feeds but has none to show
		Rows          []articleRow
		Before        bool
		Next          string
		SeenUpTo      time.Time // the position app.js reports when the page is shown or hidden; zero for none
	}{
		Nav:           s.nav(r, "home"),
		Timeline:      current,
		Home:          home,
		Tabs:          timelineTabs{Timelines: tabs, CurrentID: current.ID},
		TabID:         tabID(current),
		Path:          path,
		NoSources:     filter.OnlyIncluded && len(filter.IncludeTagIDs) == 0 && len(filter.IncludeFeedIDs) == 0,
		Flash:         s.sessionManager.PopString(r.Context(), "flash"),
		ConfirmNotice: s.confirmNotice(r),
		Rows:          rows,
		Before:        !before.IsZero(),
		Next:          next,
		SeenUpTo:      seenUpTo,
	}

	http.SetCookie(w, &http.Cookie{
		Name:     tabCookie,
		Value:    tabID(current),
		Path:     "/",
		MaxAge:   int(rss.VisitGap / time.Second),
		HttpOnly: true,
		Secure:   s.sessionManager.Cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	if err := s.renderHTML(w, http.StatusOK, "timeline.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// timelineDivider returns where the "You were here" line goes on userID's
// timeline timelineID ("" for Home) for a page rendered at now (see
// rss.TimelineMark.Divider). The line is decoration: on a store error it
// logs and returns zero, so the page shows no line.
func (s *Server) timelineDivider(r *http.Request, userID, timelineID string, now time.Time) time.Time {
	mark, err := s.services.ReadService.TimelineLine(r.Context(), userID, timelineID)
	if err != nil {
		s.logError(r, err)
		return time.Time{}
	}
	return mark.Divider(now)
}

// fillDivider sets Divider on the row the "You were here" line goes above:
// the first one positioned at or below line, if line isn't zero and
// something is above it (an earlier row, or the page before, when before is
// positioned above line).
func fillDivider(rows []articleRow, before rss.ArticleCursor, line time.Time) {
	if line.IsZero() {
		return
	}
	newer := !before.IsZero() && before.At.After(line)
	for i := range rows {
		if !rows[i].Position.After(line) {
			rows[i].Divider = newer
			return
		}
		newer = true
	}
}

// timelineSeenHandler records the newest timeline position the reader has
// had on screen, and on which timeline (homeTabID or a custom timeline's
// ID), sent by app.js's beacon when a timeline page is shown or hidden. It
// moves that timeline's "You were here" line at the reader's next visit,
// and what arrives next is positioned above it (see psql's saveArticles).
func (s *Server) timelineSeenHandler(w http.ResponseWriter, r *http.Request) {
	var form struct {
		Timeline string `form:"timeline"`
		UpTo     string `form:"up_to"`
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
	timelineID := form.Timeline
	if timelineID == homeTabID {
		timelineID = ""
	} else if !validator.Matches(timelineID, validator.UUIDRX) {
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That timeline isn't valid.")
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.ReadService.MarkSeen(r.Context(), userID, timelineID, upTo); err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// timelineRows returns a page of the articles filter shows userID, in the
// reader's position order, starting after before, and the cursor of the
// next page ("" if it's the last). A starred-only timeline lists stars,
// newest starred first, with Starred set; any other has Starred still to
// fill.
func (s *Server) timelineRows(r *http.Request, userID string, filter rss.TimelineFilter, before rss.ArticleCursor) ([]articleRow, string, error) {
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

	items, err := s.services.ArticleService.ListTimeline(r.Context(), userID, filter, before, timelinePageSize+1)
	if err != nil {
		return nil, "", err
	}
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1]
		next = encodeCursor(rss.ArticleCursor{At: last.Position, ID: last.Article.ID})
	}
	rows := make([]articleRow, len(items))
	for i, item := range items {
		rows[i] = articleRow{Article: item.Article, Feed: item.Feed, AlsoIn: item.AlsoIn, Position: item.Position, ReturnTo: r.URL.RequestURI()}
	}
	if err := s.fillStarred(r.Context(), userID, rows); err != nil {
		return nil, "", err
	}
	return rows, next, nil
}
