package server

// The design checkpoint's options for how Search unfolds (#645), switched
// with ?layout=typeahead and ?ia=. Temporary: the option picked moves into
// search.go and discover.go, and this file goes.
//
// Round 5 (typeahead): while typing, compact suggestions (the reader's
// feeds, then new sites); on Enter, tabs: Top (ranked, mixed) · Following ·
// Saved · New, with roomier rows and Follow in place.
//
// ?ia= is where the box lives:
//
//	e: Discover is the search place, as X's and Bluesky's Explore: its box
//	   searches everything and shows the results in place of the sections;
//	   the nav's Discover takes the magnifier, and Search's own entries go
//	f: the same box on Discover and on Search, as LinkedIn's on every page;
//	   results on Search

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/grodier/rss/internal/rss"
)

// searchOptionsLimit is how many results a scope lists.
const searchOptionsLimit = 50

// searchTopLimit is how many results Top lists, ranked.
const searchTopLimit = 10

// suggestFeeds and suggestSites are how many of the reader's feeds and of
// new sites the suggestions show while typing.
const (
	suggestFeeds = 4
	suggestSites = 3
)

func searchLayout(r *http.Request) string {
	if r.URL.Query().Get("layout") == "typeahead" || searchIA(r) != "" {
		return "typeahead"
	}
	return "full"
}

// searchIA is ?ia=: "e", "f", or "" for neither (round 4's Search page).
func searchIA(r *http.Request) string {
	if ia := r.URL.Query().Get("ia"); ia == "e" || ia == "f" {
		return ia
	}
	return ""
}

// suggesting reports whether a query is being typed, not yet submitted:
// submitting sends the form's scope; live search fetches the form's action,
// which has none.
func suggesting(r *http.Request, layout string) bool {
	return layout == "typeahead" && r.URL.Query().Get("scope") == ""
}

// searchBase is the page the box's results live on, and the parameters its
// links keep.
func searchBase(r *http.Request) (string, url.Values) {
	switch searchIA(r) {
	case "e":
		return "/discover", url.Values{"ia": {"e"}}
	case "f":
		return "/search", url.Values{"ia": {"f"}}
	}
	return "/search", url.Values{"layout": {"typeahead"}}
}

// searchHref is the results page for q in scope ("" while typing).
func searchHref(r *http.Request, q, scope string) string {
	base, v := searchBase(r)
	if q != "" {
		v.Set("q", q)
	}
	if scope != "" {
		v.Set("scope", scope)
	}
	return base + "?" + v.Encode()
}

// mockRecent is the recent searches, from ?recent= (comma-separated). A
// mock for the screenshots: the real list would live in the browser's
// storage, never on the server.
func mockRecent(r *http.Request) []searchScope {
	if r.URL.Query().Get("recent") == "" {
		return nil
	}
	var recent []searchScope
	for _, q := range strings.Split(r.URL.Query().Get("recent"), ",") {
		recent = append(recent, searchScope{Name: q, Href: searchHref(r, q, "top")})
	}
	return recent
}

// searchOptions is what the typeahead layout shows.
type searchOptions struct {
	// Suggest says a query is being typed: Shown is the reader's feeds
	// that match and Fresh new sites, under a row to search everything
	// (Everything, Top's URL).
	Suggest    bool
	Everything string
	Fresh      []searchResult

	Scopes []searchScope
	Scope  string // the current scope's ID
	// Shown is the current scope's results, or the suggested feeds.
	Shown []searchResult
	// Mixed says the current scope mixes kinds, so each row shows its own.
	Mixed bool
	// More links the scopes with results Top left out, after Top's list.
	More []searchScope
	// Lookup says the current view offers Look up for an address.
	Lookup bool
	// Empty is the current scope's no-results line, when it found nothing.
	Empty string
	// Recent is the recent searches, before typing (mockRecent).
	Recent []searchScope
}

// searchScope is one of the tabs, or a recent search.
type searchScope struct {
	ID    string
	Name  string
	Count int  // how many results, up to searchOptionsLimit
	Over  bool // more than searchOptionsLimit
	Href  string
}

// CountText is the count as shown: "3", or "50+".
func (sc searchScope) CountText() string {
	if sc.Over {
		return fmt.Sprintf("%d+", sc.Count)
	}
	return fmt.Sprint(sc.Count)
}

// searchResult is one result: what it is, how well it matched, and what
// its row shows.
type searchResult struct {
	Kind      string // "following", "saved" or "new"
	KindLabel string // "Following", "Saved" or "New"
	Href      string
	Title     string
	Sub       string // a new feed's own title, under its site's name
	Desc      string // a saved article's excerpt, a new site's description
	Avatar    avatarView
	Meta      []string
	Warning   string
	// Follow is a new site's one feed the reader doesn't follow yet, which
	// they can follow in place; nil when there's no feed, or a choice of
	// several (Choose).
	Follow *followButton
	Choose bool
	Score  int
}

// matchRank is how well text matches q, ignoring case: 4 all of it, 3 its
// start, 2 the start of a word in it, 1 inside a word, 0 not at all.
func matchRank(text, q string) int {
	t, lq := strings.ToLower(text), strings.ToLower(q)
	i := strings.Index(t, lq)
	switch {
	case lq == "" || i < 0:
		return 0
	case t == lq:
		return 4
	case i == 0:
		return 3
	}
	prev, _ := utf8.DecodeLastRuneInString(t[:i])
	if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
		return 2
	}
	return 1
}

// The ranking of Top: how well the title matches first, a match elsewhere
// (a site, host, feed, excerpt or description) well below any title match;
// then the reader's own things before new ones; then something recent.
// Only the reader's own data, never other readers' ("The feed most people
// follow" was rejected as cross-reader data, #628).
const (
	scoreTitle     = 10 // per point of the title's matchRank
	scoreElsewhere = 5  // matched only outside the title
	boostFollowing = 3
	boostSaved     = 2
	boostRecent    = 1 // saved, or posted, in the last 14 days
	recentWithin   = 14 * 24 * time.Hour
)

func score(titleRank int, elsewhere bool, boost int, recent bool) int {
	s := titleRank*scoreTitle + boost
	if titleRank == 0 && elsewhere {
		s += scoreElsewhere
	}
	if recent {
		s += boostRecent
	}
	return s
}

var kindOrder = map[string]int{"following": 0, "saved": 1, "new": 2}

// ranked sorts results by score, then kind, then title.
func ranked(results []searchResult) []searchResult {
	out := slices.Clone(results)
	slices.SortStableFunc(out, func(a, b searchResult) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(kindOrder[a.Kind], kindOrder[b.Kind]),
			strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
		)
	})
	return out
}

// newSearchOptions builds the suggestions, or the tabs and their results,
// from the groups' results.
func (s *Server) newSearchOptions(r *http.Request, d searchPageData) searchOptions {
	now := time.Now()
	q := d.Form.Q
	var following, saved, fresh []searchResult

	for _, f := range d.Feeds {
		name := f.Name()
		res := searchResult{Kind: "following", KindLabel: "Following", Href: "/feeds/" + f.Sub.Feed.ID, Title: name, Avatar: avatar(f.Sub.Feed.SiteID, name, f.Icon)}
		if f.Site != "" {
			res.Meta = append(res.Meta, "From "+f.Site)
		}
		if !suggesting(r, d.Layout) {
			res.Meta = append(res.Meta, postingRate(f.Sub.Feed.PostsPerWeek))
		}
		if !f.Sub.LatestArticleAt.IsZero() {
			res.Meta = append(res.Meta, "Latest "+since(f.Sub.LatestArticleAt, now))
		}
		switch {
		case !f.Sub.Feed.GoneAt.IsZero():
			res.Warning = "No longer exists"
		case f.Failing():
			res.Warning = "Failing to update"
		}
		elsewhere := matchRank(f.Sub.Site.Title, q) > 0 || matchRank(f.Sub.Site.Host, q) > 0 || matchRank(f.Sub.Feed.Title, q) > 0
		res.Score = score(matchRank(name, q), elsewhere, boostFollowing, now.Sub(f.Sub.LatestArticleAt) < recentWithin)
		following = append(following, res)
	}

	if len(d.Saved) > 0 {
		siteIDs := make([]string, len(d.Saved))
		for i, a := range d.Saved {
			siteIDs[i] = a.Feed.SiteID
		}
		icons := s.pageIcons(r, siteIDs)
		for _, a := range d.Saved {
			title := a.Article.DisplayTitle()
			res := searchResult{
				Kind: "saved", KindLabel: "Saved", Href: "/articles/" + a.Article.ID, Title: title,
				Desc:   a.Article.DisplayExcerpt(),
				Avatar: avatar(a.Feed.SiteID, a.Feed.Title, icons[a.Feed.SiteID]),
				Meta:   []string{a.Feed.Title, "Saved " + since(a.SavedAt, now)},
			}
			res.Score = score(matchRank(title, q), true, boostSaved, now.Sub(a.SavedAt) < recentWithin)
			saved = append(saved, res)
		}
	}

	// New is what the reader can follow: a site's one feed they don't
	// follow yet, with Follow in place, or a site with several to choose
	// from on its page (nothing guesses which, #629). A site whose feeds
	// they all follow is in Following already.
	returnTo := r.URL.RequestURI()
	for _, site := range d.Found.Sites {
		var open []rss.Feed
		for _, f := range site.Feeds {
			if !d.Found.Subscribed[f.ID] {
				open = append(open, f)
			}
		}
		if len(site.Feeds) > 0 && len(open) == 0 {
			continue
		}
		followed := len(site.Feeds) - len(open)
		df := rss.DiscoverFeed{Site: site.Site}
		name := df.Name()
		res := searchResult{Kind: "new", KindLabel: "New", Href: "/sites/" + site.Site.ID, Title: name, Desc: site.Site.Description, Avatar: avatar(site.Site.ID, name, d.Found.Icons[site.Site.ID])}
		res.Meta = append(res.Meta, site.Site.Host)
		switch {
		case len(open) == 1:
			df.Feed = open[0]
			res.Sub = df.FeedTitle()
			res.Follow = &followButton{FeedID: df.Feed.ID, Label: df.Label(), ReturnTo: returnTo}
			if followed > 0 {
				res.Meta = append(res.Meta, map[bool]string{true: "You follow its other feed", false: fmt.Sprintf("You follow its other %d feeds", followed)}[followed == 1])
			}
		case len(open) > 1:
			res.Choose = true
			if followed > 0 {
				res.Meta = append(res.Meta, fmt.Sprintf("Following %d of %d feeds", followed, len(site.Feeds)))
			} else {
				res.Meta = append(res.Meta, fmt.Sprintf("%d feeds", len(open)))
			}
		default:
			res.Meta = append(res.Meta, "No feeds found yet")
		}
		res.Score = score(matchRank(name, q), true, 0, false)
		fresh = append(fresh, res)
	}

	var o searchOptions
	if suggesting(r, d.Layout) {
		o.Suggest, o.Everything, o.Lookup = true, searchHref(r, q, "top"), true
		o.Shown = following[:min(len(following), suggestFeeds)]
		o.Fresh = fresh[:min(len(fresh), suggestSites)]
		return o
	}

	cut := func(results []searchResult) []searchResult { return results[:min(len(results), searchOptionsLimit)] }
	following, saved, fresh = cut(following), cut(saved), cut(fresh)
	scope := func(id, name string, results []searchResult) searchScope {
		return searchScope{ID: id, Name: name, Count: min(len(results), searchOptionsLimit), Over: len(results) > searchOptionsLimit, Href: searchHref(r, q, id)}
	}
	top := ranked(append(append(slices.Clone(following), saved...), fresh...))
	topScope := scope("top", "Top", top)
	topScope.Count, topScope.Over = len(following)+len(saved)+len(fresh), false
	o.Scopes = []searchScope{topScope, scope("following", "Following", following), scope("saved", "Saved", saved), scope("new", "New", fresh)}

	current := r.URL.Query().Get("scope")
	if !slices.ContainsFunc(o.Scopes, func(sc searchScope) bool { return sc.ID == current }) {
		current = "top"
	}
	o.Scope = current
	switch current {
	case "top":
		o.Shown, o.Mixed, o.Lookup = top[:min(len(top), searchTopLimit)], true, true
		o.Empty = "No results for “" + q + "”."
		if len(top) > searchTopLimit {
			shown := map[string]int{}
			for _, res := range o.Shown {
				shown[res.Kind]++
			}
			for _, sc := range o.Scopes[1:] {
				if sc.Count > shown[sc.ID] {
					o.More = append(o.More, sc)
				}
			}
		}
	case "following":
		o.Shown = following
		o.Empty = "None of the feeds you follow match “" + q + "”."
	case "saved":
		o.Shown = saved
		o.Empty = "No saved articles match “" + q + "”."
	case "new":
		o.Shown, o.Lookup = fresh, true
		o.Empty = "No new sites match “" + q + "”."
	}
	if len(o.Shown) > 0 {
		o.Empty = ""
	}
	return o
}
