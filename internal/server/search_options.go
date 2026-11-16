package server

// The design checkpoint's options for how Search unfolds (#645), switched
// with ?layout=typeahead and ?ia=. Temporary: the option picked moves into
// search.go and discover.go, and this file goes.
//
// Round 6 (typeahead), with ?ia=e (Discover is the search place, as X's
// and Bluesky's Explore; ?ia=f keeps round 5's other option):
//
//   - While typing, one list of feeds that match, followed or not, each
//     with its state as Discover's rows show it: "✓ Following", or Follow
//     (Choose when a site has several feeds). ?typing=labels shows the
//     state as text instead of buttons.
//   - On Enter, tabs by kind: Top (ranked, mixed) · Feeds · Saved, with
//     following as each feed row's state rather than a tab. A reader with
//     nothing saved gets no tabs: Top would be Feeds. ?tabs=status splits
//     by state instead: Top · Following · Saved · To follow.

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

// suggestFeeds is how many feeds the suggestions show while typing, and
// suggestSites how many sites they read to find them.
const (
	suggestFeeds = 6
	suggestSites = 6
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
	base, v := "/search", url.Values{"layout": {"typeahead"}}
	switch searchIA(r) {
	case "e":
		base, v = "/discover", url.Values{"ia": {"e"}}
	case "f":
		v = url.Values{"ia": {"f"}}
	}
	for _, k := range []string{"tabs", "typing"} {
		if x := r.URL.Query().Get(k); x != "" {
			v.Set(k, x)
		}
	}
	return base, v
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
	// Suggest says a query is being typed: Shown is the feeds that match,
	// followed or not, under a row to search everything (Everything, Top's
	// URL).
	Suggest    bool
	Everything string
	// Labels shows a feed's state as text, not buttons (?typing=labels).
	Labels bool

	// Scopes are the tabs; none when the reader has only one kind.
	Scopes []searchScope
	Scope  string // the current scope's ID
	// Shown is the current scope's results, or the suggested feeds.
	Shown []searchResult
	// More links the scopes with results Top left out, after Top's list.
	More []searchScope
	// Lookup says the current view offers Look up for an address: a row
	// at the top, unless the address is a site already found (KnownSite),
	// whose own row is there with its state.
	Lookup    bool
	KnownSite bool
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

// searchResult is one result and what its row shows.
type searchResult struct {
	Kind    string // "feed" or "saved"
	Href    string
	Title   string
	Desc    string // a saved article's excerpt, a site's tagline
	Avatar  avatarView
	Meta    []string
	Warning string
	// The feed's state: Following, or Follow for a site's one feed the
	// reader doesn't follow yet, or Choose for a site with several (on its
	// page; nothing guesses which, #629).
	Following bool
	Follow    *followButton
	Choose    bool
	Score     int
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

// The ranking: how well the title matches first, a match elsewhere (a
// site, host, feed, excerpt or description) well below any title match;
// then the reader's own things before others; then something recent. Only
// the reader's own data, never other readers' ("The feed most people
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

// ranked sorts results by score, then followed feeds, saved articles and
// other feeds, then title.
func ranked(results []searchResult) []searchResult {
	order := func(r searchResult) int {
		switch {
		case r.Following:
			return 0
		case r.Kind == "saved":
			return 1
		}
		return 2
	}
	out := slices.Clone(results)
	slices.SortStableFunc(out, func(a, b searchResult) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(order(a), order(b)),
			strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
		)
	})
	return out
}

// newSearchOptions builds the suggestions, or the tabs and their results,
// from the groups' results. hasSaves says the reader has saved anything.
func (s *Server) newSearchOptions(r *http.Request, d searchPageData, hasSaves bool) searchOptions {
	now := time.Now()
	q := d.Form.Q
	suggest := suggesting(r, d.Layout)
	var followed, others, saved []searchResult

	for _, f := range d.Feeds {
		name := f.Name()
		res := searchResult{Kind: "feed", Following: true, Href: "/feeds/" + f.Sub.Feed.ID, Title: name, Avatar: avatar(f.Sub.Feed.SiteID, name, f.Icon)}
		if f.Site != "" {
			res.Meta = append(res.Meta, "From "+f.Site)
		}
		if !suggest {
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
		followed = append(followed, res)
	}

	// Feeds the reader doesn't follow: a site's one feed they don't follow
	// yet, with Follow, or a site with several to choose from. A site whose
	// feeds they all follow is there already, as its followed feeds.
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
		nFollowed := len(site.Feeds) - len(open)
		df := rss.DiscoverFeed{Site: site.Site}
		name := df.Name()
		res := searchResult{Kind: "feed", Href: "/sites/" + site.Site.ID, Title: name, Desc: site.Site.Description, Avatar: avatar(site.Site.ID, name, d.Found.Icons[site.Site.ID])}
		switch {
		case len(open) == 1:
			df.Feed = open[0]
			if t := df.FeedTitle(); t != "" {
				res.Meta = append(res.Meta, t)
			}
			res.Follow = &followButton{FeedID: df.Feed.ID, Label: df.Label(), ReturnTo: returnTo}
			res.Meta = append(res.Meta, site.Site.Host)
			if nFollowed > 0 {
				res.Meta = append(res.Meta, map[bool]string{true: "You follow its other feed", false: fmt.Sprintf("You follow its other %d feeds", nFollowed)}[nFollowed == 1])
			}
		case len(open) > 1:
			res.Choose = true
			res.Meta = append(res.Meta, site.Site.Host)
			if nFollowed > 0 {
				res.Meta = append(res.Meta, fmt.Sprintf("You follow %d of %d feeds", nFollowed, len(site.Feeds)))
			} else {
				res.Meta = append(res.Meta, fmt.Sprintf("%d feeds", len(open)))
			}
		default:
			res.Meta = append(res.Meta, site.Site.Host, "No feeds found yet")
		}
		res.Score = score(matchRank(name, q), true, 0, false)
		others = append(others, res)
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
				Kind: "saved", Href: "/articles/" + a.Article.ID, Title: title,
				Desc:   a.Article.DisplayExcerpt(),
				Avatar: avatar(a.Feed.SiteID, a.Feed.Title, icons[a.Feed.SiteID]),
				Meta:   []string{"Saved " + since(a.SavedAt, now), a.Feed.Title},
			}
			res.Score = score(matchRank(title, q), true, boostSaved, now.Sub(a.SavedAt) < recentWithin)
			saved = append(saved, res)
		}
	}

	feeds := ranked(append(slices.Clone(followed), others...))
	var o searchOptions
	if _, siteKey := s.addressOffer(q); siteKey != "" {
		for _, site := range d.Found.Sites {
			if site.Site.Host == siteKey {
				o.KnownSite = true
			}
		}
	}
	o.Labels = r.URL.Query().Get("typing") == "labels"
	if suggest {
		o.Suggest, o.Everything, o.Lookup = true, searchHref(r, q, "top"), true
		o.Shown = feeds[:min(len(feeds), suggestFeeds)]
		return o
	}

	cut := func(results []searchResult) []searchResult { return results[:min(len(results), searchOptionsLimit)] }
	scope := func(id, name string, results []searchResult) searchScope {
		return searchScope{ID: id, Name: name, Count: min(len(results), searchOptionsLimit), Over: len(results) > searchOptionsLimit, Href: searchHref(r, q, id)}
	}
	top := ranked(append(slices.Clone(feeds), saved...))
	lists := map[string][]searchResult{"top": top[:min(len(top), searchTopLimit)], "feeds": cut(feeds), "saved": cut(saved), "following": cut(followed), "tofollow": cut(others)}
	empty := map[string]string{
		"top":       "No results for “" + q + "”.",
		"feeds":     "No feeds match “" + q + "”.",
		"saved":     "No saved articles match “" + q + "”.",
		"following": "None of the feeds you follow match “" + q + "”.",
		"tofollow":  "No other feeds match “" + q + "”.",
	}
	switch {
	case !hasSaves && r.URL.Query().Get("tabs") != "status":
		// Nothing saved: Top would be Feeds, so there are no tabs.
		o.Scope = "feeds"
	case r.URL.Query().Get("tabs") == "status":
		o.Scopes = []searchScope{scope("top", "Top", top), scope("following", "Following", followed), scope("saved", "Saved", saved), scope("tofollow", "To follow", others)}
	default:
		o.Scopes = []searchScope{scope("top", "Top", top), scope("feeds", "Feeds", feeds), scope("saved", "Saved", saved)}
	}
	if len(o.Scopes) > 0 {
		o.Scopes[0].Count, o.Scopes[0].Over = len(feeds)+len(saved), false
		current := r.URL.Query().Get("scope")
		if !slices.ContainsFunc(o.Scopes, func(sc searchScope) bool { return sc.ID == current }) {
			current = "top"
		}
		o.Scope = current
	}
	o.Shown, o.Empty = lists[o.Scope], empty[o.Scope]
	o.Lookup = o.Scope != "saved" && o.Scope != "following"
	if o.Scope == "top" && len(top) > searchTopLimit {
		for _, sc := range o.Scopes[1:] {
			shown := 0
			for _, res := range o.Shown {
				if sc.ID == "saved" && res.Kind == "saved" || sc.ID == "feeds" && res.Kind == "feed" ||
					sc.ID == "following" && res.Following || sc.ID == "tofollow" && res.Kind == "feed" && !res.Following {
					shown++
				}
			}
			if sc.Count > shown {
				o.More = append(o.More, sc)
			}
		}
	}
	if len(o.Shown) > 0 {
		o.Empty = ""
	}
	return o
}
