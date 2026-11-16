package server

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// Discover's search (#645): the reader's feeds, their saved articles and
// sites, by kind, in tabs. See "Feed discovery" in docs/decisions.md.
const (
	// searchTabLimit is how many results the Feeds and Saved tabs list. Each
	// source is read with one more, to tell "50+" from 50.
	searchTabLimit = 50
	// searchTopLimit is how many results Top lists, ranked.
	searchTopLimit = 10
)

// The ranking of Top and the Feeds tab: how well the title matches first,
// a match only elsewhere (a site, host, feed title, description or
// excerpt) below any title match; then the reader's own things; then
// something recent. Only the reader's own data, never other readers' ("The
// feed most people follow" was rejected as cross-reader data, #628).
const (
	scoreTitle     = 10 // per point of the title's matchRank
	scoreElsewhere = 5  // matched only outside the title
	boostFollowing = 3
	boostSaved     = 2
	boostRecent    = 1 // posted (a feed's latest article) or saved lately
	recentWithin   = 14 * 24 * time.Hour
)

// matchRank is how well text matches q, ignoring case: 4 all of it, 3 its
// start, 2 the start of a word in it (after a space), 1 anywhere else in
// it, 0 not at all. psql's matchOrder orders the same way, so a source's
// best matches are within its limit.
func matchRank(text, q string) int {
	t, lq := strings.ToLower(text), strings.ToLower(strings.TrimSpace(q))
	switch {
	case lq == "" || !strings.Contains(t, lq):
		return 0
	case t == lq:
		return 4
	case strings.HasPrefix(t, lq):
		return 3
	case strings.Contains(t, " "+lq):
		return 2
	}
	return 1
}

// searchScore scores a result: titleRank is its title's matchRank,
// elsewhere whether it matched outside the title, boost its kind's.
func searchScore(titleRank int, elsewhere bool, boost int, recent bool) int {
	score := titleRank*scoreTitle + boost
	if titleRank == 0 && elsewhere {
		score += scoreElsewhere
	}
	if recent {
		score += boostRecent
	}
	return score
}

// rankResults sorts results by score, then followed feeds, saved articles
// and other feeds, then title.
func rankResults(results []searchResult) {
	order := func(r searchResult) int {
		switch {
		case r.Following:
			return 0
		case r.Saved:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(results, func(a, b searchResult) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(order(a), order(b)),
			strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
		)
	})
}

// searchResult is a row of Discover's search (partials/search_result.html):
// a feed the reader follows, a site to follow, or a saved article.
type searchResult struct {
	Href   string
	Title  string
	Avatar avatarView
	// Desc is a site's tagline or a saved article's excerpt.
	Desc string
	// Facts are the line under it, joined with " · ".
	Facts []string
	// Warning is a followed feed's problem, after the facts.
	Warning string
	// Saved marks a saved article, whose facts lead with Saved's bookmark.
	Saved bool
	// Following marks a feed the reader follows ("✓ Following"); Follow is
	// a site's one feed they don't follow yet. A site with several has
	// neither: its row opens its page to choose (nothing guesses which,
	// #629).
	Following bool
	Follow    *followButton
	Score     int
}

// searchTab is one of the results' tabs.
type searchTab struct {
	ID, Name string
	Count    int  // its results, up to searchTabLimit
	Over     bool // more than searchTabLimit
	Href     string
	Current  bool
}

// CountText is the count as shown: "8", or "50+".
func (t searchTab) CountText() string {
	if t.Over {
		return fmt.Sprintf("%d+", t.Count)
	}
	return fmt.Sprint(t.Count)
}

// discoverResults is what Discover shows for a query.
type discoverResults struct {
	// Tabs are Top, Feeds and Saved; none for a reader who has saved
	// nothing, since Top would be Feeds.
	Tabs []searchTab
	// Scope is the tab shown: "top", "feeds" or "saved".
	Scope string
	Rows  []searchResult
	// More links the tabs with more than Top showed, after Top's list.
	More []searchTab
	// LookupQ is the address to offer Look up for, and AppLink says it's an
	// address on the app itself: a row at the top, left out when the
	// address's site was found, whose own row is there.
	LookupQ string
	AppLink bool
	// Empty is the no-results line.
	Empty string
}

// Announce is what live search says when these results come in.
func (d discoverResults) Announce() string {
	return countOf(len(d.Rows), "result", "results")
}

// countOf says how many of something there are, for a screen reader:
// "No results", "1 result", "3 results".
func countOf(n int, one, many string) string {
	switch n {
	case 0:
		return "No " + many
	case 1:
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// discoverSearch reads and ranks Discover's results for q, a valid,
// non-empty query, in the tab ?scope= names. GET handlers never fetch or
// write on the user's behalf (see discoverHandler); this only reads.
func (s *Server) discoverSearch(r *http.Request, q string) (discoverResults, error) {
	ctx := r.Context()
	userID, _ := s.authenticatedUserID(r)
	now := time.Now()

	subs, err := s.services.SubscriptionService.Search(ctx, userID, q, searchTabLimit+1)
	if err != nil {
		return discoverResults{}, err
	}
	saved, err := s.services.SaveService.ListSaved(ctx, userID, q, rss.ArticleCursor{}, searchTabLimit+1)
	if err != nil {
		return discoverResults{}, err
	}
	found, siteKey, err := s.searchSites(r, q, searchTabLimit+1)
	if err != nil {
		return discoverResults{}, err
	}
	hasSaves, err := s.services.SaveService.HasSaves(ctx, userID)
	if err != nil {
		return discoverResults{}, err
	}

	// One icon lookup for every row's site.
	var siteIDs []string
	for _, sub := range subs {
		siteIDs = append(siteIDs, sub.Site.ID)
	}
	for _, site := range found.Sites {
		siteIDs = append(siteIDs, site.Site.ID)
	}
	for _, a := range saved {
		siteIDs = append(siteIDs, a.Feed.SiteID)
	}
	var icons map[string]string
	if len(siteIDs) > 0 {
		icons = s.pageIcons(r, siteIDs)
	}

	var feeds, savedRows []searchResult
	for _, sub := range subs {
		feeds = append(feeds, followedFeedResult(sub, icons[sub.Site.ID], q, now))
	}
	known := false
	for _, site := range found.Sites {
		if siteKey != "" && site.Site.Host == siteKey {
			known = true
		}
		if res, ok := siteResult(site, found.Subscribed, icons[site.Site.ID], q, r.URL.RequestURI()); ok {
			feeds = append(feeds, res)
		}
	}
	for _, a := range saved {
		savedRows = append(savedRows, savedResult(a, icons[a.Feed.SiteID], q, now))
	}
	rankResults(feeds)

	res := discoverResults{}
	if !known {
		res.LookupQ, res.AppLink = found.LookupQ, found.AppLink
	}
	cut := func(rows []searchResult) []searchResult { return rows[:min(len(rows), searchTabLimit)] }
	// Every source is read with one more than a tab lists, so a source that
	// returned searchTabLimit+1 has more than a tab shows.
	over := len(subs) > searchTabLimit || len(found.Sites) > searchTabLimit
	tab := func(id, name string, rows []searchResult, over bool) searchTab {
		return searchTab{ID: id, Name: name, Count: min(len(rows), searchTabLimit), Over: over, Href: discoverSearchURL(q, id)}
	}

	if !hasSaves {
		res.Scope, res.Rows = "feeds", cut(feeds)
		if len(res.Rows) == 0 {
			res.Empty = "No results for “" + q + "”."
		}
		return res, nil
	}

	top := append(slices.Clone(feeds), savedRows...)
	rankResults(top)
	res.Tabs = []searchTab{
		{ID: "top", Name: "Top", Href: discoverSearchURL(q, "top")},
		tab("feeds", "Feeds", feeds, over),
		tab("saved", "Saved", savedRows, len(saved) > searchTabLimit),
	}
	res.Scope = r.URL.Query().Get("scope")
	switch res.Scope {
	case "feeds":
		res.Rows, res.Empty = cut(feeds), "No feeds match “"+q+"”."
	case "saved":
		res.Rows, res.Empty = cut(savedRows), "No saved articles match “"+q+"”."
	default:
		res.Scope = "top"
		res.Rows, res.Empty = top[:min(len(top), searchTopLimit)], "No results for “"+q+"”."
		shownSaved := 0
		for _, row := range res.Rows {
			if row.Saved {
				shownSaved++
			}
		}
		for _, t := range res.Tabs[1:] {
			shown := shownSaved
			if t.ID == "feeds" {
				shown = len(res.Rows) - shownSaved
			}
			if t.Count > shown || t.Over {
				res.More = append(res.More, t)
			}
		}
	}
	for i := range res.Tabs {
		res.Tabs[i].Current = res.Tabs[i].ID == res.Scope
	}
	if len(res.Rows) > 0 {
		res.Empty = ""
	}
	return res, nil
}

// discoverSearchURL is the results' URL for q in scope.
func discoverSearchURL(q, scope string) string {
	return "/discover?" + url.Values{"q": {q}, "scope": {scope}}.Encode()
}

// followedFeedResult is a feed the reader follows: its name (theirs, else
// its title), its site's when it differs, its posting rate and latest
// article, and whether it's gone or failing.
func followedFeedResult(sub rss.Subscription, icon, q string, now time.Time) searchResult {
	name := cmp.Or(sub.Title, sub.Feed.Title, sub.Feed.Url)
	res := searchResult{Href: "/feeds/" + sub.Feed.ID, Title: name, Avatar: avatar(sub.Feed.SiteID, name, icon), Following: true}
	if site := cmp.Or(sub.Site.Title, sub.Site.Host); site != name {
		res.Facts = append(res.Facts, "From "+site)
	}
	res.Facts = append(res.Facts, postingRate(sub.Feed.PostsPerWeek))
	if !sub.LatestArticleAt.IsZero() {
		res.Facts = append(res.Facts, "Latest "+since(sub.LatestArticleAt, now))
	}
	switch {
	case !sub.Feed.GoneAt.IsZero():
		res.Warning = "No longer exists"
	case sub.Feed.ConsecutiveFailures >= failingThreshold:
		res.Warning = "Failing to update"
	}
	elsewhere := matchRank(sub.Feed.Title, q) > 0 || matchRank(sub.Site.Title, q) > 0 || matchRank(sub.Site.Host, q) > 0
	res.Score = searchScore(matchRank(name, q), elsewhere, boostFollowing, !sub.LatestArticleAt.IsZero() && now.Sub(sub.LatestArticleAt) < recentWithin)
	return res
}

// siteResult is a found site as a row, unless the reader follows all its
// feeds (ok is false: those are in the results as their followed feeds).
// A site with one feed they don't follow gets Follow for it, with the
// feed's own title when it isn't the site's name; one with several says
// how many, and its row opens its page to choose.
func siteResult(site rss.SiteWithFeeds, subscribed map[string]bool, icon, q, returnTo string) (searchResult, bool) {
	var open []rss.Feed
	for _, f := range site.Feeds {
		if !subscribed[f.ID] {
			open = append(open, f)
		}
	}
	if len(site.Feeds) > 0 && len(open) == 0 {
		return searchResult{}, false
	}
	followed := len(site.Feeds) - len(open)
	df := rss.DiscoverFeed{Site: site.Site}
	name := df.Name()
	res := searchResult{Href: "/sites/" + site.Site.ID, Title: name, Desc: site.Site.Description, Avatar: avatar(site.Site.ID, name, icon)}
	switch {
	case len(open) == 1:
		df.Feed = open[0]
		if t := df.FeedTitle(); t != "" {
			res.Facts = append(res.Facts, t)
		}
		res.Facts = append(res.Facts, site.Site.Host)
		switch followed {
		case 0:
		case 1:
			res.Facts = append(res.Facts, "You follow its other feed")
		default:
			res.Facts = append(res.Facts, fmt.Sprintf("You follow its other %d feeds", followed))
		}
		res.Follow = &followButton{FeedID: df.Feed.ID, Label: df.Label(), ReturnTo: returnTo}
	case len(open) > 1:
		res.Facts = append(res.Facts, site.Site.Host)
		if followed > 0 {
			res.Facts = append(res.Facts, fmt.Sprintf("You follow %d of %d feeds", followed, len(site.Feeds)))
		} else {
			res.Facts = append(res.Facts, fmt.Sprintf("%d feeds", len(open)))
		}
	default:
		res.Facts = append(res.Facts, site.Site.Host, "No feeds found yet")
	}
	// The site search only returns sites that matched somewhere.
	res.Score = searchScore(matchRank(name, q), true, 0, false)
	return res, true
}

// savedResult is a saved article: its title, excerpt, when it was saved
// and its feed.
func savedResult(a rss.SavedArticle, icon, q string, now time.Time) searchResult {
	title := a.Article.DisplayTitle()
	res := searchResult{
		Href:   "/articles/" + a.Article.ID,
		Title:  title,
		Desc:   a.Article.DisplayExcerpt(),
		Avatar: avatar(a.Feed.SiteID, a.Feed.Title, icon),
		Facts:  []string{"Saved " + since(a.SavedAt, now), a.Feed.Title},
		Saved:  true,
	}
	// ListSaved only returns articles that matched somewhere.
	res.Score = searchScore(matchRank(title, q), true, boostSaved, now.Sub(a.SavedAt) < recentWithin)
	return res
}

// Discover's suggestions (#666): while the reader types, live search
// fetches /discover?q=… without ?scope=, which only a submit sends, and
// gets one list of feeds to go to instead of the results. See "Feed
// discovery" in docs/decisions.md.
const (
	// suggestLimit is how many feeds the suggestions list.
	suggestLimit = 6
	// suggestSubsLimit and suggestSitesLimit are how many subscriptions
	// and sites they read: more sites, since some are left out as fully
	// followed.
	suggestSubsLimit  = 6
	suggestSitesLimit = 12
)

// discoverSuggestions is what Discover shows while the reader types.
type discoverSuggestions struct {
	// Rows are up to suggestLimit feeds, followed or not, ranked as the
	// Feeds tab; a followed one has Following set, and none has Follow.
	Rows []searchResult
	// LookupQ and AppLink are as the results' (discoverResults).
	LookupQ string
	AppLink bool
	// Q is the query, and SearchHref its results' Top tab, for the last
	// row, "Search everything".
	Q          string
	SearchHref string
}

// Announce is what live search says when these suggestions come in.
func (d discoverSuggestions) Announce() string {
	return countOf(len(d.Rows), "feed", "feeds")
}

// discoverSuggest reads and ranks Discover's suggestions for q, a valid,
// non-empty query: only the reader's subscriptions and the site search,
// as saved articles wait for Enter. It only reads.
func (s *Server) discoverSuggest(r *http.Request, q string) (discoverSuggestions, error) {
	userID, _ := s.authenticatedUserID(r)
	now := time.Now()

	subs, err := s.services.SubscriptionService.Search(r.Context(), userID, q, suggestSubsLimit)
	if err != nil {
		return discoverSuggestions{}, err
	}
	found, siteKey, err := s.searchSites(r, q, suggestSitesLimit)
	if err != nil {
		return discoverSuggestions{}, err
	}

	var siteIDs []string
	for _, sub := range subs {
		siteIDs = append(siteIDs, sub.Site.ID)
	}
	for _, site := range found.Sites {
		siteIDs = append(siteIDs, site.Site.ID)
	}
	var icons map[string]string
	if len(siteIDs) > 0 {
		icons = s.pageIcons(r, siteIDs)
	}

	var rows []searchResult
	for _, sub := range subs {
		rows = append(rows, followedFeedSuggestion(sub, icons[sub.Site.ID], q, now))
	}
	known := false
	for _, site := range found.Sites {
		if siteKey != "" && site.Site.Host == siteKey {
			known = true
		}
		if res, ok := siteSuggestion(site, found.Subscribed, icons[site.Site.ID], q); ok {
			rows = append(rows, res)
		}
	}
	rankResults(rows)

	res := discoverSuggestions{Rows: rows[:min(len(rows), suggestLimit)], Q: q, SearchHref: discoverSearchURL(q, "top")}
	if !known {
		res.LookupQ, res.AppLink = found.LookupQ, found.AppLink
	}
	return res, nil
}

// followedFeedSuggestion is a feed the reader follows as a suggestion: as
// its result, with only its site's name when it differs and its latest
// article as facts.
func followedFeedSuggestion(sub rss.Subscription, icon, q string, now time.Time) searchResult {
	res := followedFeedResult(sub, icon, q, now)
	res.Facts, res.Warning = nil, ""
	if site := cmp.Or(sub.Site.Title, sub.Site.Host); site != res.Title {
		res.Facts = append(res.Facts, "From "+site)
	}
	if !sub.LatestArticleAt.IsZero() {
		res.Facts = append(res.Facts, "Latest "+since(sub.LatestArticleAt, now))
	}
	return res
}

// siteSuggestion is a found site as a suggestion, unless the reader follows
// all its feeds: as its result, without its tagline or Follow. As there, a
// site is the feeds the reader doesn't follow yet: one's own title when it
// isn't the site's name, then the host; or the host and how many.
func siteSuggestion(site rss.SiteWithFeeds, subscribed map[string]bool, icon, q string) (searchResult, bool) {
	res, ok := siteResult(site, subscribed, icon, q, "")
	if !ok {
		return searchResult{}, false
	}
	var open []rss.Feed
	for _, f := range site.Feeds {
		if !subscribed[f.ID] {
			open = append(open, f)
		}
	}
	res.Desc, res.Follow, res.Facts = "", nil, nil
	if len(open) == 1 {
		if t := (rss.DiscoverFeed{Site: site.Site, Feed: open[0]}).FeedTitle(); t != "" {
			res.Facts = append(res.Facts, t)
		}
	}
	res.Facts = append(res.Facts, site.Site.Host)
	if len(open) > 1 {
		res.Facts = append(res.Facts, fmt.Sprintf("%d feeds", len(open)))
	}
	return res, true
}
