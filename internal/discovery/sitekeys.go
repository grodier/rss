package discovery

import (
	"net/url"
	"strings"
)

// SiteKey identifies a site: its lowercase host without port and without a
// leading "www.", followed on a host shared by many sites by the path that
// names the site (sitePath). "https://www.Example.com:443/a" -> "example.com",
// "https://medium.com/@Alice/a-post" -> "medium.com/@alice".
func SiteKey(u *url.URL) string {
	return keyHost(u) + sitePath(u)
}

// SiteRoot is the address of u's site: its home page, or on a shared host
// the page that names it. It keeps u's scheme and host, "www." included:
// "https://medium.com/@alice/a-post" -> "https://medium.com/@alice". Like
// CanonicalFeedURL, the scheme and host are lowercase, without the scheme's
// default port.
func SiteRoot(u *url.URL) *url.URL {
	path, _ := rootPath(u)
	if path == "" {
		path = "/"
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: path}
	canonicalize(root)
	return root
}

// IsHomePage reports whether u is its site's root (SiteRoot) with no query;
// a trailing "/" doesn't matter.
func IsHomePage(u *url.URL) bool {
	if u.RawQuery != "" {
		return false
	}
	root, _ := rootPath(u)
	return strings.TrimSuffix(u.Path, "/") == root
}

// KeyHost is a site key's host: "medium.com/@alice" -> "medium.com".
func KeyHost(key string) string {
	host, _, _ := strings.Cut(key, "/")
	return host
}

// keyHost is u's lowercase host without port and without a leading "www.".
func keyHost(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// sitePath returns the path that names u's site on a shared host ("/@alice"),
// or "" when u belongs to its host's own site. It is lowercase, except where
// the platform's names are case-sensitive (YouTube channel IDs).
func sitePath(u *url.URL) string {
	path, keepCase := rootPath(u)
	if keepCase {
		return path
	}
	return strings.ToLower(path)
}

// githubReserved are first path segments on github.com that are GitHub's own
// pages, not an owner's.
var githubReserved = map[string]bool{
	"orgs": true, "topics": true, "features": true, "marketplace": true,
	"explore": true, "sponsors": true, "settings": true, "about": true,
	"login": true, "site": true, "collections": true, "trending": true,
}

// rootPath returns the path of u's site root on a shared host, in u's case
// ("/@Alice" for "https://medium.com/@Alice/a-post"), and whether the
// platform's names in it are case-sensitive; "" when u belongs to its host's
// own site. A host with its own rule uses only that rule; every other host
// uses the @name rule (Mastodon and the fediverse, Threads, …).
func rootPath(u *url.URL) (path string, keepCase bool) {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	seg := func(i int) string {
		if i < len(segs) {
			return segs[i]
		}
		return ""
	}

	switch keyHost(u) {
	case "medium.com":
		// /@name/…, /feed/@name
		if isAtName(seg(0)) {
			return "/" + seg(0), false
		}
		if seg(0) == "feed" && isAtName(seg(1)) {
			return "/" + seg(1), false
		}
	case "youtube.com":
		// /@handle/…, /channel/ID/…, /feeds/videos.xml?channel_id=ID,
		// /c/name/…, /user/name/…
		switch {
		case isAtName(seg(0)):
			return "/" + seg(0), false
		case seg(0) == "channel" && seg(1) != "":
			return "/channel/" + seg(1), true
		case seg(0) == "feeds" && seg(1) == "videos.xml" && len(segs) == 2:
			if id := u.Query().Get("channel_id"); id != "" && !strings.Contains(id, "/") {
				return "/channel/" + id, true
			}
		case (seg(0) == "c" || seg(0) == "user") && seg(1) != "":
			return "/" + seg(0) + "/" + seg(1), false
		}
	case "reddit.com":
		// /r/name/… (/r/name/.rss, /r/name.rss), /user/name/…, /u/name/…
		name := strings.TrimSuffix(seg(1), ".rss")
		switch {
		case name == "":
		case seg(0) == "r":
			return "/r/" + name, false
		case seg(0) == "user" || seg(0) == "u":
			return "/user/" + name, false
		}
	case "github.com":
		// /owner/repo/…, /owner, /owner.atom
		owner := seg(0)
		if len(segs) == 1 {
			owner = strings.TrimSuffix(owner, ".atom")
		}
		switch {
		case owner == "" || githubReserved[strings.ToLower(owner)]:
		case seg(1) != "":
			return "/" + owner + "/" + seg(1), false
		default:
			return "/" + owner, false
		}
	case "bsky.app":
		// /profile/handle/… (/profile/handle/rss)
		if seg(0) == "profile" && seg(1) != "" {
			return "/profile/" + seg(1), false
		}
	default:
		// /@name/…, /@name.rss, /@name.atom
		name := seg(0)
		if len(segs) == 1 {
			name = strings.TrimSuffix(strings.TrimSuffix(name, ".rss"), ".atom")
		}
		if isAtName(name) {
			return "/" + name, false
		}
	}
	return "", false
}

// isAtName reports whether seg is a path segment naming an account, "@name".
func isAtName(seg string) bool {
	return len(seg) > 1 && seg[0] == '@'
}
