package discovery

import (
	"net/url"
	"regexp"
	"strings"
)

// platform is what discovery knows about a host shared by many sites
// (#725): how an address names a site there, the platform's name and the
// kind of site its keys name. Later platform work adds fields here rather
// than lists of its own keyed by host (#848).
type platform struct {
	host string // keyHost form: lowercase, no "www." ("medium.com")
	name string // shown after a site's own name ("Alice · Medium"); see Platform
	kind Kind   // the kind of site its keys with a path name; see KindOf

	// rootPath returns the path of u's site root (segs is u's path split
	// on "/", without the leading and trailing "/"), in u's case, and
	// whether the platform's names in it are case-sensitive; "" when u is
	// one of the platform's own pages. Nil: the host keys like any host
	// without an entry (the "@name" rule, atNameRootPath).
	rootPath func(u *url.URL, segs []string) (path string, keepCase bool)

	// ownName, if set, cleans a title of the platform's own affixes before
	// OwnName's shared rules, for a feed's title (fromFeed) as for a page's:
	// Medium's "Stories by … on Medium", GitHub's "GitHub - ", " · GitHub"
	// and a repository's description.
	ownName func(key, name string, fromFeed bool) string

	// feeds returns the addresses of the feeds the platform publishes for
	// the site whose root is root and key is key, in the order to try them;
	// nil when it publishes none or the key is the platform's own page.
	// Discovery tries them when the site's page lists no feed or answers
	// with an error status (#849).
	feeds func(root *url.URL, key string) []string

	// aliases are other hosts (keyHost form) serving the same sites at the
	// same paths; addresses on them are rewritten to mainHost (#851).
	aliases []string
	// mainHost is the host addresses are rewritten to, "www." included
	// where the platform uses it ("www.youtube.com").
	mainHost string

	// address, if set, returns the address that u, on the platform's own
	// host, stands for when that's another site's (segs as for rootPath):
	// a Tumblr blog's path on tumblr.com names the blog's own host. Nil, or
	// nil returned: u is left as it is.
	address func(u *url.URL, segs []string) *url.URL
}

// platforms are the hosts with their own rules, in no particular order.
var platforms = []platform{
	{
		host:     "medium.com",
		name:     "Medium",
		kind:     KindWebsites,
		rootPath: mediumRootPath,
		ownName:  mediumOwnName,
		feeds:    mediumFeeds,
	},
	{
		host:     "youtube.com",
		name:     "YouTube",
		kind:     KindVideos,
		rootPath: youtubeRootPath,
		feeds:    youtubeFeeds,
		aliases:  []string{"m.youtube.com"},
		mainHost: "www.youtube.com",
	},
	{
		host:     "reddit.com",
		name:     "Reddit",
		kind:     KindWebsites,
		rootPath: redditRootPath,
		feeds:    redditFeeds,
		aliases:  []string{"old.reddit.com", "new.reddit.com", "np.reddit.com", "m.reddit.com", "i.reddit.com"},
		mainHost: "www.reddit.com",
	},
	{
		host:     "github.com",
		name:     "GitHub",
		kind:     KindWebsites,
		rootPath: githubRootPath,
		ownName:  githubOwnName,
		feeds:    githubFeeds,
	},
	{
		host:     "bsky.app",
		name:     "Bluesky",
		kind:     KindSocial,
		rootPath: blueskyRootPath,
		feeds:    blueskyFeeds,
	},
	{
		// Blogs are on <blog>.tumblr.com, ordinary sites with no rule; a
		// blog's address on tumblr.com is rewritten to it (tumblrAddress).
		host:    "tumblr.com",
		name:    "Tumblr",
		kind:    KindWebsites,
		address: tumblrAddress,
	},
}

// platformsByHost indexes platforms by host, for platformFor.
var platformsByHost = func() map[string]platform {
	m := make(map[string]platform, len(platforms))
	for _, p := range platforms {
		m[p.host] = p
	}
	return m
}()

// aliasesByHost indexes platforms by their aliases, for canonicalAddress.
var aliasesByHost = func() map[string]platform {
	m := make(map[string]platform)
	for _, p := range platforms {
		for _, a := range p.aliases {
			m[a] = p
		}
	}
	return m
}()

// canonicalAddress rewrites u in place to its platform's main address: an
// alias host becomes the main host (old.reddit.com -> www.reddit.com), and
// a Tumblr blog's path on tumblr.com becomes its own host
// (tumblr.com/staff/123/a-post -> https://staff.tumblr.com/). Any other
// address is left as it is.
func canonicalAddress(u *url.URL) {
	if aliasHost(u) {
		return
	}
	if p, ok := platformFor(keyHost(u)); ok && p.address != nil {
		if a := p.address(u, strings.Split(strings.Trim(u.Path, "/"), "/")); a != nil {
			*u = *a
		}
	}
}

// aliasHost rewrites u's host in place to its platform's main host when it
// is an alias, keeping the scheme, path and query, and reports whether it
// was one.
func aliasHost(u *url.URL) bool {
	p, ok := aliasesByHost[keyHost(u)]
	if ok {
		u.Host = p.mainHost
	}
	return ok
}

// canonicalCopy is a copy of u rewritten by canonicalAddress.
func canonicalCopy(u *url.URL) *url.URL {
	c := *u
	canonicalAddress(&c)
	return &c
}

// AliasHosts are the hosts (keyHost form) whose sites now key under another
// host: the platforms' aliases, and the hosts whose addresses can name
// another host's site (tumblr.com). For SiteRepository.Rekey.
func AliasHosts() []string {
	var hosts []string
	for _, p := range platforms {
		hosts = append(hosts, p.aliases...)
		if p.address != nil {
			hosts = append(hosts, p.host)
		}
	}
	return hosts
}

// platformFor returns host's entry (host in keyHost form).
func platformFor(host string) (platform, bool) {
	p, ok := platformsByHost[host]
	return p, ok
}

// platformFeeds returns the feeds a platform publishes for the site with
// root and key (see platform.feeds): the host's entry's, else, for a key
// that names an "@name" site on any other host, "/@name.rss" (Mastodon).
func platformFeeds(root *url.URL, key string) []string {
	if !hasPath(key) {
		return nil
	}
	if p, ok := platformFor(KeyHost(key)); ok {
		if p.feeds == nil {
			return nil
		}
		return p.feeds(root, key)
	}
	segs := rootSegs(root)
	if len(segs) != 1 || !isAtName(segs[0]) {
		return nil
	}
	return []string{root.Scheme + "://" + root.Host + "/" + segs[0] + ".rss"}
}

// rootSegs is a site root's path split on "/", without the leading and
// trailing "/", in the root's case, each segment escaped for a path.
func rootSegs(root *url.URL) []string {
	segs := strings.Split(strings.Trim(root.Path, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return segs
}

// seg is segs[i], or "" past its end.
func seg(segs []string, i int) string {
	if i < len(segs) {
		return segs[i]
	}
	return ""
}

// atNameRootPath is the rule for every host without its own: a first path
// segment "@name" names an account (Mastodon and the fediverse, Threads, …).
// /@name/…, /@name.rss, /@name.atom
func atNameRootPath(_ *url.URL, segs []string) (string, bool) {
	name := seg(segs, 0)
	if len(segs) == 1 {
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".rss"), ".atom")
	}
	if isAtName(name) {
		return "/" + name, false
	}
	return "", false
}

// isAtName reports whether seg is a path segment naming an account, "@name".
func isAtName(seg string) bool {
	return len(seg) > 1 && seg[0] == '@'
}

// Medium.

// mediumFeed is a Medium author's feed title, which names a site found
// from the feed itself: "Stories by Alice on Medium".
var mediumFeed = regexp.MustCompile(`^Stories by (.+) on Medium$`)

// mediumRootPath: /@name/…, /feed/@name
func mediumRootPath(_ *url.URL, segs []string) (string, bool) {
	if isAtName(seg(segs, 0)) {
		return "/" + seg(segs, 0), false
	}
	if seg(segs, 0) == "feed" && isAtName(seg(segs, 1)) {
		return "/" + seg(segs, 1), false
	}
	return "", false
}

// mediumOwnName drops "Stories by … on Medium", which an author's feed is
// titled and which their page's title can be too.
func mediumOwnName(_, name string, _ bool) string {
	if m := mediumFeed.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// mediumFeeds: an author's feed, /feed/@name.
func mediumFeeds(root *url.URL, _ string) []string {
	segs := rootSegs(root)
	if len(segs) != 1 || !isAtName(segs[0]) {
		return nil
	}
	return []string{"https://medium.com/feed/" + segs[0]}
}

// YouTube.

// youtubeRootPath: /@handle/…, /channel/ID/…, /feeds/videos.xml?channel_id=ID,
// /c/name/…, /user/name/…; channel IDs are case-sensitive.
func youtubeRootPath(u *url.URL, segs []string) (string, bool) {
	switch {
	case isAtName(seg(segs, 0)):
		return "/" + seg(segs, 0), false
	case seg(segs, 0) == "channel" && seg(segs, 1) != "":
		return "/channel/" + seg(segs, 1), true
	case seg(segs, 0) == "feeds" && seg(segs, 1) == "videos.xml" && len(segs) == 2:
		if id := u.Query().Get("channel_id"); id != "" && !strings.Contains(id, "/") {
			return "/channel/" + id, true
		}
	case (seg(segs, 0) == "c" || seg(segs, 0) == "user") && seg(segs, 1) != "":
		return "/" + seg(segs, 0) + "/" + seg(segs, 1), false
	}
	return "", false
}

// youtubeFeeds: a channel's videos, by its ID. A /@handle, /c/name or
// /user/name page's own feed link names its channel, and is found there.
func youtubeFeeds(root *url.URL, _ string) []string {
	segs := rootSegs(root)
	if len(segs) != 2 || segs[0] != "channel" {
		return nil
	}
	id, err := url.PathUnescape(segs[1])
	if err != nil {
		return nil
	}
	return []string{"https://www.youtube.com/feeds/videos.xml?channel_id=" + url.QueryEscape(id)}
}

// Reddit.

// redditRootPath: /r/name/… (/r/name/.rss, /r/name.rss), /user/name/…,
// /u/name/…
func redditRootPath(_ *url.URL, segs []string) (string, bool) {
	name := strings.TrimSuffix(seg(segs, 1), ".rss")
	switch {
	case name == "":
	case seg(segs, 0) == "r":
		return "/r/" + name, false
	case seg(segs, 0) == "user" || seg(segs, 0) == "u":
		return "/user/" + name, false
	}
	return "", false
}

// redditFeeds: a subreddit's or a user's posts, /r/name/.rss and
// /user/name/.rss.
func redditFeeds(root *url.URL, _ string) []string {
	segs := rootSegs(root)
	if len(segs) != 2 || (segs[0] != "r" && segs[0] != "user") {
		return nil
	}
	return []string{"https://www.reddit.com/" + segs[0] + "/" + segs[1] + "/.rss"}
}

// GitHub.

// githubReserved are first path segments on github.com that are GitHub's own
// pages, not an owner's.
var githubReserved = map[string]bool{
	"orgs": true, "topics": true, "features": true, "marketplace": true,
	"explore": true, "sponsors": true, "settings": true, "about": true,
	"login": true, "site": true, "collections": true, "trending": true,
}

// githubRootPath: /owner/repo/…, /owner, /owner.atom
func githubRootPath(_ *url.URL, segs []string) (string, bool) {
	owner := seg(segs, 0)
	if len(segs) == 1 {
		owner = strings.TrimSuffix(owner, ".atom")
	}
	switch {
	case owner == "" || githubReserved[strings.ToLower(owner)]:
	case seg(segs, 1) != "":
		return "/" + owner + "/" + seg(segs, 1), false
	default:
		return "/" + owner, false
	}
	return "", false
}

// githubOwnName drops a page title's "GitHub - " and " · GitHub" ("GitHub -
// owner/repo: description", "owner · GitHub") and a repository's
// description after "owner/repo: "; an owner's page has no description to
// cut. A feed's title is the feed's own name, kept as it is.
func githubOwnName(key, name string, fromFeed bool) string {
	if fromFeed {
		return name
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "GitHub - "), " · GitHub")
	if strings.Count(key, "/") == 2 {
		if repo, _, ok := strings.Cut(name, ": "); ok {
			name = repo
		}
	}
	return name
}

// githubFeeds: a repository's releases, tags and commits, or an owner's
// public activity, /owner.atom.
func githubFeeds(root *url.URL, _ string) []string {
	segs := rootSegs(root)
	switch {
	case len(segs) == 2:
		repo := "https://github.com/" + segs[0] + "/" + segs[1]
		return []string{repo + "/releases.atom", repo + "/tags.atom", repo + "/commits.atom"}
	case len(segs) == 1 && segs[0] != "":
		return []string{"https://github.com/" + segs[0] + ".atom"}
	}
	return nil
}

// Tumblr.

// tumblrReserved are first path segments on tumblr.com that are Tumblr's own
// pages, not a blog's.
var tumblrReserved = map[string]bool{
	"about": true, "activity": true, "apps": true, "blog": true,
	"changes": true, "communities": true, "customize": true,
	"dashboard": true, "developers": true, "docs": true, "explore": true,
	"following": true, "help": true, "inbox": true, "jobs": true,
	"likes": true, "live": true, "login": true, "logout": true,
	"messages": true, "new": true, "oauth": true, "policy": true,
	"privacy": true, "reblog": true, "register": true, "search": true,
	"settings": true, "share": true, "tagged": true, "themes": true,
	"tumblelog": true,
}

// tumblrAddress: /blog/…, /blog/view/blog/… -> https://blog.tumblr.com/,
// when blog isn't one of Tumblr's own pages and is a valid host label.
func tumblrAddress(_ *url.URL, segs []string) *url.URL {
	blog := strings.ToLower(seg(segs, 0))
	if blog == "blog" && seg(segs, 1) == "view" {
		blog = strings.ToLower(seg(segs, 2))
	} else if tumblrReserved[blog] {
		return nil
	}
	if !validLabel(blog) {
		return nil
	}
	return &url.URL{Scheme: "https", Host: blog + ".tumblr.com", Path: "/"}
}

// Bluesky.

// blueskyRootPath: /profile/handle/… (/profile/handle/rss)
func blueskyRootPath(_ *url.URL, segs []string) (string, bool) {
	if seg(segs, 0) == "profile" && seg(segs, 1) != "" {
		return "/profile/" + seg(segs, 1), false
	}
	return "", false
}

// blueskyFeeds: a profile's posts, /profile/handle/rss.
func blueskyFeeds(root *url.URL, _ string) []string {
	segs := rootSegs(root)
	if len(segs) != 2 || segs[0] != "profile" {
		return nil
	}
	return []string{"https://bsky.app/profile/" + segs[1] + "/rss"}
}
