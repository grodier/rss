package discovery

import (
	"net/url"
	"strings"
)

// SiteKeyRulesVersion is the version of SiteKey's rules. Bump it in any PR
// that changes what key an address gets, so the next deploy re-keys the
// sites already stored (www migrate's re-key step).
const SiteKeyRulesVersion = 2

// SiteKey identifies a site: its lowercase host without port and without a
// leading "www.", followed on a host shared by many sites by the path that
// names the site (sitePath). "https://www.Example.com:443/a" -> "example.com",
// "https://medium.com/@Alice/a-post" -> "medium.com/@alice". An alias host's
// address keys as its platform's main host's (canonicalAddress):
// "https://old.reddit.com/r/golang" -> "reddit.com/r/golang".
func SiteKey(u *url.URL) string {
	u = canonicalCopy(u)
	return keyHost(u) + sitePath(u)
}

// SiteRoot is the address of u's site: its home page, or on a shared host
// the page that names it. It keeps u's scheme and host, "www." included:
// "https://medium.com/@alice/a-post" -> "https://medium.com/@alice". Like
// CanonicalFeedURL, the scheme and host are lowercase, without the scheme's
// default port. An alias host's root is on its platform's main host
// (canonicalAddress).
func SiteRoot(u *url.URL) *url.URL {
	u = canonicalCopy(u)
	path, _ := rootPath(u)
	if path == "" {
		path = "/"
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: path}
	canonicalize(root)
	return root
}

// IsHomePage reports whether u is its site's root (SiteRoot) with no query;
// a trailing "/" doesn't matter. An alias host's address is read as its
// platform's main address (canonicalAddress).
func IsHomePage(u *url.URL) bool {
	u = canonicalCopy(u)
	if u.RawQuery != "" {
		return false
	}
	root, _ := rootPath(u)
	return strings.TrimSuffix(u.Path, "/") == root
}

// FeedSiteKey is the key and root of the site a stored feed belongs to under
// the current rules, as discovery's direct branch derives them: from the
// feed's link (siteURL) when it's a site's address (channelLink), else from
// the feed's own URL. Both are "" when neither parses as an address with a
// host.
func FeedSiteKey(siteURL, feedURL string) (key, root string) {
	if u, ok := channelLink(siteURL); ok {
		return SiteKey(u), SiteRoot(u).String()
	}
	if u, err := url.Parse(feedURL); err == nil && u.Host != "" {
		return SiteKey(u), SiteRoot(u).String()
	}
	return "", ""
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

// rootPath returns the path of u's site root on a shared host, in u's case
// ("/@Alice" for "https://medium.com/@Alice/a-post"), and whether the
// platform's names in it are case-sensitive; "" when u belongs to its host's
// own site. A host with its own rule (platforms) uses only that rule; every
// other host uses the @name rule (atNameRootPath).
func rootPath(u *url.URL) (path string, keepCase bool) {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if p, ok := platformFor(keyHost(u)); ok && p.rootPath != nil {
		return p.rootPath(u, segs)
	}
	return atNameRootPath(u, segs)
}
