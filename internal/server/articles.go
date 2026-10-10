package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"

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

	data := struct {
		Article rss.Article
		Feed    rss.Feed
		Site    rss.Site
		Body    template.HTML
	}{
		Article: article,
		Feed:    feed,
		Site:    site,
		Body:    sanitize.HTML(raw, base),
	}

	if err := s.renderHTML(w, http.StatusOK, "article.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// articleRow is one article in a list of articles.
type articleRow struct {
	Article rss.Article
	Feed    rss.Feed // links the row to its feed; zero on the feed's own page
	Read    bool     // this user has opened the article
}

// markRead sets Read on each row userID has read.
func (s *Server) markRead(ctx context.Context, userID string, rows []articleRow) error {
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
