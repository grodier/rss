package server

// The design checkpoint's options for how Search's results unfold (#645),
// switched with ?layout=. Temporary: the option picked moves into
// search.go, and this file goes.

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
)

// searchOptionsLimit is how many results a scope lists in the options.
const searchOptionsLimit = 50

// searchAllLimit is how many results All lists, ranked.
const searchAllLimit = 10

// The options' layouts:
//
//	full: the groups, each in its own page's rows, as first built
//	tabs: All (ranked, mixed) · Following · Saved · New as underline tabs
//	segmented: the same scopes as a segmented control under the box
//	split: Yours (Following and Saved, ranked) · New, as underline tabs
//	typeahead: while typing, only the reader's feeds that match and a row to
//	  search everything; on Enter (?scope=), the tabs layout
var searchLayoutNames = []string{"full", "tabs", "segmented", "split", "typeahead"}

// suggestLimit is how many feeds the type-ahead option suggests.
const suggestLimit = 5

// suggesting reports whether the type-ahead option is suggesting: a query
// typed, not yet submitted. Submitting sends the form's scope (All, or the
// tab the reader was on); live search fetches the form's action, which
// has none.
func suggesting(r *http.Request, layout string) bool {
	return layout == "typeahead" && r.URL.Query().Get("scope") == ""
}

// mockRecent is the type-ahead option's recent searches, from ?recent=
// (comma-separated). A mock for the screenshots: the real list would live
// in the browser's storage, never on the server.
func mockRecent(r *http.Request, layout string) []searchScope {
	if layout != "typeahead" || r.URL.Query().Get("recent") == "" {
		return nil
	}
	var recent []searchScope
	for _, q := range strings.Split(r.URL.Query().Get("recent"), ",") {
		v := url.Values{"q": {q}, "layout": {layout}, "scope": {"all"}}
		recent = append(recent, searchScope{Name: q, Href: "/search?" + v.Encode()})
	}
	return recent
}

func searchLayout(r *http.Request) string {
	if l := r.URL.Query().Get("layout"); slices.Contains(searchLayoutNames, l) {
		return l
	}
	return "full"
}

// searchOptions is what the tabs, segmented and split layouts show.
type searchOptions struct {
	Scopes []searchScope
	Scope  string // the current scope's ID
	// Shown is the current scope's results.
	Shown []searchResult
	// Mixed says the current scope mixes kinds, so each row shows its own.
	Mixed bool
	// More links the scopes with results All left out, after All's list.
	More []searchScope
	// Lookup says the current scope offers Look up for an address: All,
	// New and Yours' neighbour New.
	Lookup bool
	// Empty is the current scope's no-results line, when it found nothing.
	Empty string

	// Suggest says the type-ahead option is suggesting: Shown is the
	// reader's feeds that match, under a row to search everything
	// (Everything, the All tab's URL).
	Suggest    bool
	Everything string
	// Recent is the type-ahead option's recent searches, before typing
	// (mockRecent).
	Recent []searchScope
}

// searchScope is one of the scopes: a tab or segment.
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

// searchResult is one row: what it is, how well it matched, and what it
// shows.
type searchResult struct {
	Kind      string // "following", "saved" or "new"
	KindLabel string // "Following", "Saved" or "New"
	Href      string
	Title     titleMatch
	Avatar    avatarView
	Meta      []string
	Warning   string
	Score     int
}

// titleMatch is a title split around the first match of the query, so the
// match can be marked.
type titleMatch struct {
	Before, Match, After string
}

func splitMatch(title, q string) titleMatch {
	lt, lq := strings.ToLower(title), strings.ToLower(q)
	i := strings.Index(lt, lq)
	// Lowering can change a string's length in bytes; then the offsets
	// don't carry over, so mark nothing.
	if i < 0 || lq == "" || len(lt) != len(title) {
		return titleMatch{Before: title}
	}
	return titleMatch{Before: title[:i], Match: title[i : i+len(q)], After: title[i+len(q):]}
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

// The ranking of a mixed scope (All, Yours): how well the title matches
// first, a match elsewhere (a site, host, feed or description) well below
// any title match; then the reader's own things before new ones; then
// something recent. Only the reader's own data, never other readers'
// ("The feed most people follow" was rejected as cross-reader data, #628).
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
			strings.Compare(strings.ToLower(a.Title.Before+a.Title.Match+a.Title.After), strings.ToLower(b.Title.Before+b.Title.Match+b.Title.After)),
		)
	})
	return out
}

// newSearchOptions builds the options' scopes and results from the
// groups' results (each fetched up to searchOptionsLimit+1).
func (s *Server) newSearchOptions(r *http.Request, d searchPageData) searchOptions {
	now := time.Now()
	q := d.Form.Q
	var following, saved, fresh []searchResult

	for _, f := range d.Feeds {
		name := f.Name()
		res := searchResult{Kind: "following", KindLabel: "Following", Href: "/feeds/" + f.Sub.Feed.ID, Title: splitMatch(name, q), Avatar: avatar(f.Sub.Feed.SiteID, name, f.Icon)}
		if f.Site != "" {
			res.Meta = append(res.Meta, "From "+f.Site)
		}
		if f.Sub.LatestArticleAt.IsZero() {
			res.Meta = append(res.Meta, "No articles yet")
		} else {
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
				Kind: "saved", KindLabel: "Saved", Href: "/articles/" + a.Article.ID, Title: splitMatch(title, q),
				Avatar: avatar(a.Feed.SiteID, a.Feed.Title, icons[a.Feed.SiteID]),
				Meta:   []string{a.Feed.Title, "Saved " + since(a.SavedAt, now)},
			}
			res.Score = score(matchRank(title, q), true, boostSaved, now.Sub(a.SavedAt) < recentWithin)
			saved = append(saved, res)
		}
	}

	// New is sites with a feed the reader doesn't follow yet: a site whose
	// feeds they all follow is in Following already, so it isn't new.
	for _, site := range d.Found.Sites {
		followed := 0
		for _, f := range site.Feeds {
			if d.Found.Subscribed[f.ID] {
				followed++
			}
		}
		if len(site.Feeds) > 0 && followed == len(site.Feeds) {
			continue
		}
		name := cmp.Or(site.Site.Title, site.Site.Host)
		res := searchResult{Kind: "new", KindLabel: "New", Href: "/sites/" + site.Site.ID, Title: splitMatch(name, q), Avatar: avatar(site.Site.ID, name, d.Found.Icons[site.Site.ID])}
		res.Meta = append(res.Meta, site.Site.Host)
		switch n := len(site.Feeds); {
		case followed > 0:
			res.Meta = append(res.Meta, fmt.Sprintf("Following %d of %d feeds", followed, n))
		case n == 1:
			res.Meta = append(res.Meta, "1 feed")
		case n > 1:
			res.Meta = append(res.Meta, fmt.Sprintf("%d feeds", n))
		}
		res.Score = score(matchRank(name, q), true, 0, false)
		fresh = append(fresh, res)
	}

	scope := func(id, name string, results []searchResult) searchScope {
		sc := searchScope{ID: id, Name: name, Count: min(len(results), searchOptionsLimit), Over: len(results) > searchOptionsLimit}
		v := url.Values{"q": {q}, "layout": {d.Layout}, "scope": {id}}
		sc.Href = "/search?" + v.Encode()
		return sc
	}
	cut := func(results []searchResult) []searchResult { return results[:min(len(results), searchOptionsLimit)] }
	following, saved, fresh = cut(following), cut(saved), cut(fresh)

	var o searchOptions
	current := r.URL.Query().Get("scope")
	if suggesting(r, d.Layout) {
		v := url.Values{"q": {q}, "layout": {d.Layout}, "scope": {"all"}}
		o.Suggest, o.Everything, o.Shown, o.Lookup = true, "/search?"+v.Encode(), following, true
		return o
	}
	if d.Layout == "split" {
		yours := ranked(append(slices.Clone(following), saved...))
		o.Scopes = []searchScope{scope("yours", "Yours", yours), scope("new", "New", fresh)}
		if current != "yours" && current != "new" {
			current = "yours"
			if len(yours) == 0 && len(fresh) > 0 {
				current = "new"
			}
		}
		o.Scope = current
		switch current {
		case "yours":
			o.Shown, o.Mixed = yours, true
			o.Empty = "Nothing of yours matches “" + q + "”."
		default:
			o.Shown, o.Lookup = fresh, true
			o.Empty = "No new sites match “" + q + "”."
		}
	} else {
		all := ranked(append(append(slices.Clone(following), saved...), fresh...))
		allScope := scope("all", "All", all)
		allScope.Count = len(following) + len(saved) + len(fresh)
		o.Scopes = []searchScope{allScope, scope("following", "Following", following), scope("saved", "Saved", saved), scope("new", "New", fresh)}
		if !slices.ContainsFunc(o.Scopes, func(sc searchScope) bool { return sc.ID == current }) {
			current = "all"
		}
		o.Scope = current
		switch current {
		case "all":
			o.Shown, o.Mixed, o.Lookup = all[:min(len(all), searchAllLimit)], true, true
			o.Empty = "No results for “" + q + "”."
			if len(all) > searchAllLimit {
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
	}
	if len(o.Shown) > 0 {
		o.Empty = ""
	}
	return o
}
