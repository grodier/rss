package server

import (
	"cmp"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/sanitize"
	"github.com/grodier/rss/internal/validator"
)

// articleHandler shows an article's content, else its summary, with a link to
// the original. Any logged-in user can open any article.
func (s *Server) articleHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	article, err := s.services.ArticleService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	// Opening the article page marks it read. GET handlers otherwise never
	// write, since CrossOriginProtection doesn't cover GET (see feedHandler).
	// This write is idempotent and only records that this user opened this
	// page, so a forged request gains nothing beyond a read marker.
	userID, _ := s.authenticatedUserID(r)
	if err := s.services.ReadService.MarkRead(r.Context(), userID, article.ID); err != nil {
		s.logError(r, err) // the page still renders
	}

	feed, err := s.services.FeedService.GetByID(r.Context(), article.FeedID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	site, err := s.services.SiteService.GetByID(r.Context(), feed.SiteID)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	// Relative URLs in the body resolve against the article's address, else
	// the feed's.
	base, err := url.Parse(article.URL)
	if err != nil || article.URL == "" {
		base, _ = url.Parse(feed.Url)
	}
	raw := article.Content
	if raw == "" {
		raw = article.Summary
	}

	// The card image goes above the text unless the text already shows it.
	var leadImage string
	if img := article.CardImageURL(); img != "" && !sanitize.ShowsImage(raw, base, img) {
		leadImage = img
	}

	body := sanitize.HTML(raw, base)

	saved, err := s.services.SaveService.SavedArticleIDs(r.Context(), userID, []string{article.ID})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	starred, err := s.services.StarService.StarredArticleIDs(r.Context(), userID, []string{article.ID})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	var debug *articleDebugView
	if s.debugEnabled(r) {
		d, err := s.services.DebugService.ArticleDebug(r.Context(), userID, article.ID)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		debug = newArticleDebugView(article, d, body, leadImage, time.Now())
		// Rendering only: the list line's link opens the panel.
		debug.Open = r.URL.Query().Get("debug") == "1"
	}

	data := struct {
		Nav       navData
		Article   rss.Article
		Feed      rss.Feed
		Site      rss.Site
		ReadOn    string
		Icons     map[string]string
		LeadImage string
		Body      template.HTML
		Saved     bool
		Starred   bool
		ReturnTo  string
		Debug     *articleDebugView
	}{
		Nav:       s.nav(r, ""),
		Article:   article,
		Feed:      feed,
		Site:      site,
		ReadOn:    readOnLabel(article, site),
		Icons:     s.pageIcons(r, []string{site.ID}),
		LeadImage: leadImage,
		Body:      body,
		Saved:     saved[article.ID],
		Starred:   starred[article.ID],
		ReturnTo:  r.URL.RequestURI(),
		Debug:     debug,
	}

	if err := s.renderHTML(w, http.StatusOK, "article.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// readOnLabel returns the name the article page's "Read on" button shows:
// the article's host (without "www.") when its URL is on another site than
// site, else the site's title, else its host. Hosts on a subdomain of each
// other count as the same site.
func readOnLabel(article rss.Article, site rss.Site) string {
	host := strings.ToLower(article.URLHost())
	if host == "" || host == site.Host ||
		strings.HasSuffix(host, "."+site.Host) || strings.HasSuffix(site.Host, "."+host) {
		return cmp.Or(site.Title, site.Host)
	}
	return host
}

// articleReadHandler records that the user clicked through to the original
// article from a row: the row's source link pings it (<a ping>, or a beacon
// sent by app.js). The request body is ignored. CrossOriginProtection rejects
// cross-site POSTs; pings and beacons from our own pages are same-origin.
func (s *Server) articleReadHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := s.services.ReadService.MarkRead(r.Context(), userID, id); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// articleSaveHandler saves an article for the user, and every copy of it
// with it. Saving is quiet: no flash, and it doesn't mark the article read.
func (s *Server) articleSaveHandler(w http.ResponseWriter, r *http.Request) {
	s.toggleAction(w, r, s.services.SaveService.Save)
}

// articleUnsaveHandler removes the user's save of an article and of every
// copy of it. Unsaving an article that isn't saved is a no-op.
func (s *Server) articleUnsaveHandler(w http.ResponseWriter, r *http.Request) {
	s.toggleAction(w, r, s.services.SaveService.Unsave)
}

// articleStarHandler stars an article for the user, and every copy of it
// with it. Like saving, starring is quiet and doesn't mark the article read.
func (s *Server) articleStarHandler(w http.ResponseWriter, r *http.Request) {
	s.toggleAction(w, r, s.services.StarService.Star)
}

// articleUnstarHandler removes the user's star of an article and of every
// copy of it. Unstarring an article that isn't starred is a no-op.
func (s *Server) articleUnstarHandler(w http.ResponseWriter, r *http.Request) {
	s.toggleAction(w, r, s.services.StarService.Unstar)
}

// toggleAction runs what the save, unsave, star and unstar handlers share:
// it calls do with the user and the article named in the URL, then sends the
// user back to the row they posted from (return_to, anchored on
// #article-{id}). app.js posts the same form with fetch and ignores the
// redirect.
func (s *Server) toggleAction(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, userID, articleID string) error) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return
	}

	var form returnForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	userID, _ := s.authenticatedUserID(r)
	if err := do(r.Context(), userID, id); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	http.Redirect(w, r, withFragment(safeReturnPath(form.ReturnTo, "/"), "article-"+id), http.StatusSeeOther)
}

// withFragment returns path with its fragment, if any, replaced by fragment.
func withFragment(path, fragment string) string {
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path = path[:i]
	}
	return path + "#" + fragment
}

// articleRow is one article in a list of articles.
type articleRow struct {
	Article  rss.Article
	Feed     rss.Feed         // links the row to its feed; zero on the feed's own page
	AlsoIn   []rss.Feed       // other subscribed feeds with a copy; timeline only
	Read     bool             // this user has opened the article
	Saved    bool             // this user has saved the article (or a copy)
	Starred  bool             // this user has starred the article (or a copy)
	Icon     string           // the URL of Feed's site icon, or "" for its letter
	ReturnTo string           // where the row's Save and Star buttons send the user back to
	Debug    *articleRowDebug // the row's debug line; nil unless debug mode is on
}

// fillIcons sets Icon on each row whose feed's site has an icon. Icons are
// decoration: on a store error it logs and leaves the letters.
func (s *Server) fillIcons(r *http.Request, rows []articleRow) {
	var siteIDs []string
	for _, row := range rows {
		if row.Feed.ID != "" {
			siteIDs = append(siteIDs, row.Feed.SiteID)
		}
	}
	if len(siteIDs) == 0 {
		return
	}
	icons := s.pageIcons(r, siteIDs)
	for i := range rows {
		rows[i].Icon = icons[rows[i].Feed.SiteID]
	}
}

// fillRead sets Read on each row userID has read.
func (s *Server) fillRead(ctx context.Context, userID string, rows []articleRow) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Article.ID
	}
	read, err := s.services.ReadService.ReadArticleIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Read = read[rows[i].Article.ID]
	}
	return nil
}

// fillSaved sets Saved on each row userID has saved.
func (s *Server) fillSaved(ctx context.Context, userID string, rows []articleRow) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Article.ID
	}
	saved, err := s.services.SaveService.SavedArticleIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Saved = saved[rows[i].Article.ID]
	}
	return nil
}

// fillStarred sets Starred on each row userID has starred.
func (s *Server) fillStarred(ctx context.Context, userID string, rows []articleRow) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Article.ID
	}
	starred, err := s.services.StarService.StarredArticleIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Starred = starred[rows[i].Article.ID]
	}
	return nil
}
