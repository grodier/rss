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
}

// platforms are the hosts with their own rules, in no particular order.
var platforms = []platform{
	{
		host:     "medium.com",
		name:     "Medium",
		kind:     KindWebsites,
		rootPath: mediumRootPath,
		ownName:  mediumOwnName,
	},
	{
		host:     "youtube.com",
		name:     "YouTube",
		kind:     KindVideos,
		rootPath: youtubeRootPath,
	},
	{
		host:     "reddit.com",
		name:     "Reddit",
		kind:     KindWebsites,
		rootPath: redditRootPath,
	},
	{
		host:     "github.com",
		name:     "GitHub",
		kind:     KindWebsites,
		rootPath: githubRootPath,
		ownName:  githubOwnName,
	},
	{
		host:     "bsky.app",
		name:     "Bluesky",
		kind:     KindSocial,
		rootPath: blueskyRootPath,
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

// platformFor returns host's entry (host in keyHost form).
func platformFor(host string) (platform, bool) {
	p, ok := platformsByHost[host]
	return p, ok
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

// Bluesky.

// blueskyRootPath: /profile/handle/… (/profile/handle/rss)
func blueskyRootPath(_ *url.URL, segs []string) (string, bool) {
	if seg(segs, 0) == "profile" && seg(segs, 1) != "" {
		return "/profile/" + seg(segs, 1), false
	}
	return "", false
}
