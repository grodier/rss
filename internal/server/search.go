package server

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// searchPerGroup is how many results each of Search's groups shows.
const searchPerGroup = 3

type searchPageData struct {
	Nav      navData
	Form     searchForm
	Searched bool
	// Feeds are the reader's subscriptions whose name matches (Your feeds).
	Feeds []myFeedRow
	// Saved are the reader's saved articles that match (Saved).
	Saved []rss.SavedArticle
	// Found are the sites that match (New sites), and the lookup offer.
	Found foundSites

	// Layout is the design checkpoint's option for how results unfold
	// (#645), from ?layout= (see searchLayouts). Temporary: the option
	// picked replaces the switch, and the fields below go with the others.
	Layout string
	// Groups are the non-empty groups as compact rows, for every layout
	// but "full".
	Groups []searchGroup
	// Mixed is every group's rows in one list, each labelled with its
	// kind, for "mixed".
	Mixed []searchRow
	// Scope is the group shown, for "scopes": ?scope=, else the first
	// group with results.
	Scope string
}

// searchLayouts are the design checkpoint's options for how Search's
// results unfold (#645). Temporary.
var searchLayouts = map[string]bool{
	"full":    true, // each group in its own page's rows, as first built
	"compact": true, // the groups, each as compact rows of one shape
	"mixed":   true, // one list of compact rows, each labelled with its kind
	"scopes":  true, // one group at a time, under tabs
}

// searchGroup is one of Search's groups as compact rows.
type searchGroup struct {
	ID    string // "feeds", "saved" or "sites"
	Name  string
	Rows  []searchRow
	Href  string // this group's tab, for "scopes"
	Label string // a row's kind, for "mixed"
}

// searchRow is a compact result row (partials/search_result.html): an avatar,
// a title linking to the result, and a line of facts.
type searchRow struct {
	Href      string
	Title     string
	Avatar    avatarView
	Kind      string   // what the result is, for "mixed"; else ""
	Meta      []string // the facts, joined with " · "
	Warning   string   // a fact that's a problem, such as a failing feed
	Following bool     // a site with a feed the reader follows
}

// Shown is the scope's group, for "scopes".
func (d searchPageData) Shown() searchGroup {
	for _, g := range d.Groups {
		if g.ID == d.Scope {
			return g
		}
	}
	return searchGroup{}
}

// compactGroups builds the non-empty groups' compact rows.
func (s *Server) compactGroups(r *http.Request, d searchPageData, saved []rss.SavedArticle) []searchGroup {
	now := time.Now()
	var groups []searchGroup

	if len(d.Feeds) > 0 {
		g := searchGroup{ID: "feeds", Name: "Your feeds", Label: "Feed you follow"}
		for _, f := range d.Feeds {
			row := searchRow{Href: "/feeds/" + f.Sub.Feed.ID, Title: f.Name(), Avatar: avatar(f.Sub.Feed.SiteID, f.Name(), f.Icon)}
			if f.Site != "" {
				row.Meta = append(row.Meta, "From "+f.Site)
			}
			if f.Sub.LatestArticleAt.IsZero() {
				row.Meta = append(row.Meta, "No articles yet")
			} else {
				row.Meta = append(row.Meta, "Latest "+since(f.Sub.LatestArticleAt, now))
			}
			switch {
			case !f.Sub.Feed.GoneAt.IsZero():
				row.Warning = "No longer exists"
			case f.Failing():
				row.Warning = "Failing to update"
			}
			g.Rows = append(g.Rows, row)
		}
		groups = append(groups, g)
	}

	if len(saved) > 0 {
		siteIDs := make([]string, len(saved))
		for i, a := range saved {
			siteIDs[i] = a.Feed.SiteID
		}
		icons := s.pageIcons(r, siteIDs)
		g := searchGroup{ID: "saved", Name: "Saved", Label: "Saved"}
		for _, a := range saved {
			g.Rows = append(g.Rows, searchRow{
				Href:   "/articles/" + a.Article.ID,
				Title:  a.Article.DisplayTitle(),
				Avatar: avatar(a.Feed.SiteID, a.Feed.Title, icons[a.Feed.SiteID]),
				Meta:   []string{a.Feed.Title, "Saved " + since(a.SavedAt, now)},
			})
		}
		groups = append(groups, g)
	}

	if len(d.Found.Sites) > 0 {
		g := searchGroup{ID: "sites", Name: "New sites", Label: "Site"}
		for _, site := range d.Found.Sites {
			name := cmp.Or(site.Site.Title, site.Site.Host)
			row := searchRow{Href: "/sites/" + site.Site.ID, Title: name, Avatar: avatar(site.Site.ID, name, d.Found.Icons[site.Site.ID])}
			row.Meta = append(row.Meta, site.Site.Host)
			switch n := len(site.Feeds); n {
			case 0:
			case 1:
				row.Meta = append(row.Meta, "1 feed")
			default:
				row.Meta = append(row.Meta, fmt.Sprintf("%d feeds", n))
			}
			for _, f := range site.Feeds {
				if d.Found.Subscribed[f.ID] {
					row.Following = true
				}
			}
			g.Rows = append(g.Rows, row)
		}
		groups = append(groups, g)
	}

	for i := range groups {
		v := url.Values{"q": {d.Form.Q}, "layout": {d.Layout}, "scope": {groups[i].ID}}
		groups[i].Href = "/search?" + v.Encode()
	}
	return groups
}

// Empty reports whether a search found nothing in any group.
func (d searchPageData) Empty() bool {
	return len(d.Feeds) == 0 && len(d.Saved) == 0 && len(d.Found.Sites) == 0
}

// Announce is what live search says when these results come in: how many
// results there are across the groups.
func (d searchPageData) Announce() string {
	if d.Layout == "scopes" {
		return countOf(len(d.Shown().Rows), "result", "results")
	}
	return countOf(len(d.Feeds)+len(d.Saved)+len(d.Found.Sites), "result", "results")
}

// searchHandler is the whole app's search: the reader's feeds, their saved
// articles and new sites, the first searchPerGroup of each. Like
// discoverHandler it only reads.
func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
	data := searchPageData{
		Nav:    s.nav(r, "search"),
		Form:   searchForm{Q: strings.TrimSpace(r.URL.Query().Get("q"))},
		Layout: r.URL.Query().Get("layout"),
	}
	if !searchLayouts[data.Layout] {
		data.Layout = "full"
	}
	// One group at a time shows more of it (temporary, see searchLayouts).
	perGroup := searchPerGroup
	if data.Layout == "scopes" {
		perGroup = 10
	}
	status := http.StatusOK

	data.Form.CheckField(validator.MaxChars(data.Form.Q, searchMaxChars), "q", "Search must be 200 characters or fewer")

	switch {
	case !data.Form.Valid():
		status = http.StatusUnprocessableEntity
	case data.Form.Q != "":
		userID, _ := s.authenticatedUserID(r)
		q := data.Form.Q

		subs, err := s.services.SubscriptionService.Search(r.Context(), userID, q, perGroup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		saved, err := s.services.SaveService.ListSaved(r.Context(), userID, q, rss.ArticleCursor{}, perGroup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		found, err := s.searchSites(r, q, perGroup)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}

		siteIDs := make([]string, len(subs))
		for i, sub := range subs {
			siteIDs[i] = sub.Site.ID
		}
		icons := s.pageIcons(r, siteIDs)
		data.Feeds = make([]myFeedRow, len(subs))
		for i, sub := range subs {
			data.Feeds[i] = newMyFeedRow(sub, icons[sub.Site.ID], false, r.URL.RequestURI())
		}
		data.Saved = saved
		data.Found = found
		data.Searched = true

		if data.Layout != "full" {
			data.Groups = s.compactGroups(r, data, saved)
		}
		for _, g := range data.Groups {
			for _, row := range g.Rows {
				row.Kind = g.Label
				data.Mixed = append(data.Mixed, row)
			}
		}
		if data.Layout == "scopes" {
			data.Scope = r.URL.Query().Get("scope")
			if data.Shown().ID == "" && len(data.Groups) > 0 {
				data.Scope = data.Groups[0].ID
			}
		}
	}

	if err := s.renderHTML(w, status, "search.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}
