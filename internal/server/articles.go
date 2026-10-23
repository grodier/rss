package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

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

	saved, err := s.services.SaveService.SavedArticleIDs(r.Context(), userID, []string{article.ID})
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}

	data := struct {
		Nav       navData
		Article   rss.Article
		Feed      rss.Feed
		Site      rss.Site
		Icons     map[string]string
		LeadImage string
		Body      template.HTML
		Saved     bool
		ReturnTo  string
	}{
		Nav:       s.nav(r, ""),
		Article:   article,
		Feed:      feed,
		Site:      site,
		Icons:     s.pageIcons(r, []string{site.ID}),
		LeadImage: leadImage,
		Body:      sanitize.HTML(raw, base),
		Saved:     saved[article.ID],
		ReturnTo:  r.URL.RequestURI(),
	}

	if err := s.renderHTML(w, http.StatusOK, "article.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
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
	s.saveAction(w, r, true)
}

// articleUnsaveHandler removes the user's save of an article and of every
// copy of it. Unsaving an article that isn't saved is a no-op.
func (s *Server) articleUnsaveHandler(w http.ResponseWriter, r *http.Request) {
	s.saveAction(w, r, false)
}

// saveAction runs what the save and unsave handlers share: it saves (or, if
// save is false, unsaves) the article named in the URL, then sends the user back to the row they
// posted from (return_to, anchored on #article-{id}). app.js posts the same
// form with fetch and ignores the redirect.
func (s *Server) saveAction(w http.ResponseWriter, r *http.Request, save bool) {
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

	action := s.services.SaveService.Unsave
	if save {
		action = s.services.SaveService.Save
	}
	userID, _ := s.authenticatedUserID(r)
	if err := action(r.Context(), userID, id); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return
	}

	http.Redirect(w, r, withFragment(safeReturnPath(form.ReturnTo, "/"), "article-"+id), http.StatusSeeOther)
}

// articleHideHandler hides an article, and every copy of it, from the
// user's timeline. It stays on its feed page. Hiding doesn't mark it read or
// unsave it. Without JavaScript the row is gone when the page reloads, so a
// flash says where it went; app.js posts the same form with fetch and
// collapses the row instead.
func (s *Server) articleHideHandler(w http.ResponseWriter, r *http.Request) {
	form, ok := s.hideAction(w, r, true)
	if !ok {
		return
	}
	s.sessionManager.Put(r.Context(), "flash", "Hidden from your timeline. It's still on its feed page.")
	http.Redirect(w, r, safeReturnPath(form.ReturnTo, "/"), http.StatusSeeOther)
}

// articleUnhideHandler puts an article, and every copy of it, back on the
// user's timeline, at its original position. Unhiding an article that isn't
// hidden is a no-op.
func (s *Server) articleUnhideHandler(w http.ResponseWriter, r *http.Request) {
	form, ok := s.hideAction(w, r, false)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	http.Redirect(w, r, withFragment(safeReturnPath(form.ReturnTo, "/"), "article-"+id), http.StatusSeeOther)
}

// hideAction runs what the hide and unhide handlers share: it hides (or, if
// hide is false, unhides) the article named in the URL and returns the
// posted form. On failure it has written the response and ok is false.
func (s *Server) hideAction(w http.ResponseWriter, r *http.Request, hide bool) (form returnForm, ok bool) {
	id := chi.URLParam(r, "id")
	if !validator.Matches(id, validator.UUIDRX) {
		s.notFoundResponse(w, r)
		return returnForm{}, false
	}

	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return returnForm{}, false
	}

	action := s.services.HideService.Unhide
	if hide {
		action = s.services.HideService.Hide
	}
	userID, _ := s.authenticatedUserID(r)
	if err := action(r.Context(), userID, id); err != nil {
		if errors.Is(err, rss.ErrNoRecord) {
			s.notFoundResponse(w, r)
		} else {
			s.serverErrorHTML(w, r, err)
		}
		return returnForm{}, false
	}
	return form, true
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
	Feed     rss.Feed   // links the row to its feed; zero on the feed's own page
	AlsoIn   []rss.Feed // other subscribed feeds with a copy; timeline only
	Read     bool       // this user has opened the article
	Saved    bool       // this user has saved the article (or a copy)
	Hideable bool       // the row is on the timeline, so it has a hide control
	Hidden   bool       // this user hid the article (or a copy) from their timeline; feed pages only
	Icon     string     // the URL of Feed's site icon, or "" for its letter
	ReturnTo string     // where the row's buttons send the user back to
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

// fillHidden sets Hidden on each row userID has hidden from their timeline.
func (s *Server) fillHidden(ctx context.Context, userID string, rows []articleRow) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Article.ID
	}
	hidden, err := s.services.HideService.HiddenArticleIDs(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Hidden = hidden[rows[i].Article.ID]
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
