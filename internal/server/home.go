package server

import (
	"net/http"

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

	// Home's leave-outs come with muting (#362); until then it's every
	// subscription.
	s.renderTimeline(w, r, userID, rss.Timeline{Home: true})
}

// timelineTabs is the data of the timeline-tabs partial: Home, then the
// user's custom timelines, with the one shown (CurrentID, "" for Home)
// marked.
type timelineTabs struct {
	Timelines []rss.Timeline
	CurrentID string
}

// renderTimeline renders timeline.html: a page of current's articles for
// userID (Home when current.Home), with the tab row.
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

	items, err := s.services.ArticleService.ListTimeline(r.Context(), userID, current.Filter, before, timelinePageSize+1)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	next := ""
	if len(items) > timelinePageSize {
		items = items[:timelinePageSize]
		last := items[len(items)-1].Article
		next = encodeCursor(rss.ArticleCursor{At: last.TimelineAt, ID: last.ID})
	}

	rows := make([]articleRow, len(items))
	for i, item := range items {
		rows[i] = articleRow{Article: item.Article, Feed: item.Feed, AlsoIn: item.AlsoIn, ReturnTo: r.URL.RequestURI()}
	}
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
	s.fillIcons(r, rows)

	path := "/"
	if !current.Home {
		path = "/timelines/" + current.ID
	}
	filter := current.Filter

	data := struct {
		Nav           navData
		Flash         string
		ConfirmNotice *confirmNotice
		Timeline      rss.Timeline
		Tabs          timelineTabs
		Path          string // the timeline's URL, for the pager
		NoSources     bool   // it shows only some feeds but has none to show
		Rows          []articleRow
		Before        bool
		Next          string
	}{
		Nav:           s.nav(r, "home"),
		Timeline:      current,
		Tabs:          timelineTabs{Timelines: timelines, CurrentID: current.ID},
		Path:          path,
		NoSources:     filter.OnlyIncluded && len(filter.IncludeTagIDs) == 0 && len(filter.IncludeFeedIDs) == 0,
		Flash:         s.sessionManager.PopString(r.Context(), "flash"),
		ConfirmNotice: s.confirmNotice(r),
		Rows:          rows,
		Before:        !before.IsZero(),
		Next:          next,
	}

	if err := s.renderHTML(w, http.StatusOK, "timeline.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
