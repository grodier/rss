package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
)

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
	Divider  bool             // the "You were here" line goes above this row
	Position time.Time        // the row's position in the reader's timeline (see rss.ArticleWithFeed); zero elsewhere
	Eager    bool             // load the card image at once: the first row with an image on a list's first page
	Follow   *followButton    // Follow in the foot, for a feed the reader doesn't follow; a section's Recent posts only
	NoAvatar bool             // name Feed without its avatar; a site's page, whose head has the site's
}

// Byline returns the row's authors for its kicker (rss.Article.ShortByline),
// or "" when the article's only author is the feed's name the kicker
// already shows.
func (r articleRow) Byline() string {
	authors := r.Article.Authors
	if r.Feed.ID != "" && len(authors) == 1 && strings.EqualFold(strings.TrimSpace(authors[0]), strings.TrimSpace(r.Feed.Title)) {
		return ""
	}
	return r.Article.ShortByline()
}

// markFirstImageEager sets Eager on the first row with a card image. Call it
// only for a list's first page: rows on later pages (before) are appended by
// infinite scroll, below the fold by definition. On a phone that first image
// is usually the page's largest element; the rest stay lazy, since they're
// publishers' full-size images most readers never scroll to, and the
// browser's lazy loading already fetches them as they near the viewport.
func markFirstImageEager(rows []articleRow) {
	for i := range rows {
		if rows[i].Article.CardImageURL() != "" {
			rows[i].Eager = true
			return
		}
	}
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
	return fillMark(ctx, userID, rows, s.services.ReadService.ReadArticleIDs, func(row *articleRow, on bool) { row.Read = on })
}

// fillSaved sets Saved on each row userID has saved.
func (s *Server) fillSaved(ctx context.Context, userID string, rows []articleRow) error {
	return fillMark(ctx, userID, rows, s.services.SaveService.SavedArticleIDs, func(row *articleRow, on bool) { row.Saved = on })
}

// fillStarred sets Starred on each row userID has starred.
func (s *Server) fillStarred(ctx context.Context, userID string, rows []articleRow) error {
	return fillMark(ctx, userID, rows, s.services.StarService.StarredArticleIDs, func(row *articleRow, on bool) { row.Starred = on })
}

// fillMark sets a mark on each row whose article marked reports, with set.
func fillMark(ctx context.Context, userID string, rows []articleRow,
	marked func(ctx context.Context, userID string, articleIDs []string) (map[string]bool, error),
	set func(row *articleRow, on bool)) error {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.Article.ID
	}
	on, err := marked(ctx, userID, ids)
	if err != nil {
		return err
	}
	for i := range rows {
		set(&rows[i], on[rows[i].Article.ID])
	}
	return nil
}

// followButton is Follow for a feed (partials/discover_feed.html's
// follow-form), in one of two kinds, by the page it's on (see "Following a
// feed" in docs/decisions.md):
//   - On a page that manages the feed, its own and its site's (Manage),
//     Following is a pressed button that unfollows, by way of a
//     confirmation when the subscription has a name or tags
//     (feedUnsubscribeHandler).
//   - In a list for finding feeds (Discover's rows, a section's Feeds and
//     Recent posts, search results), a followed feed shows "✓ Following"
//     linking to its page, where it's managed, and Follow turns into that
//     label once pressed.
type followButton struct {
	FeedID   string
	Label    string // rss.DiscoverFeed.Label
	ReturnTo string
	// Following is whether the reader follows the feed.
	Following bool
	// Manage makes Following a pressed button that unfollows, rather than
	// a label.
	Manage bool
	// Reload posts it as a plain form, without save-star.ts's toggle in
	// place, for a page whose other controls depend on following (the
	// feed's page, whose Edit is only for a followed feed).
	Reload bool
}
