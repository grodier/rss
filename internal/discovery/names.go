package discovery

import (
	"regexp"
	"strings"
)

// The parts of a path-scoped site's title that name its platform or
// repeat its key, removed by OwnName.
var (
	// platformSuffix is the last " - ", " – ", " — " or " | " and what
	// follows it: "Alice – Medium", "Channel - YouTube".
	platformSuffix = regexp.MustCompile(`\s+[-–—|]\s+[^-–—|]*$`)
	// handleSuffix is a trailing "(@handle)": "Bob (@bob@mastodon.social)".
	handleSuffix = regexp.MustCompile(`\s*\(@[^()]*\)$`)
)

// OwnName is a site's title as the site names itself (#728), for a key with
// a path; the platform is shown apart from the name (Platform). A title
// first loses its platform's own affixes (the entry's ownName: Medium's
// "Stories by … on Medium", GitHub's "GitHub - " and a repository's
// description after "owner/repo:"). A title from the platform's page
// (fromFeed false) then loses the platform's suffix and a trailing handle,
// so "Alice – Medium" -> "Alice". A title from a feed is the feed's own
// name, so nothing else goes: "Kurzgesagt – In a Nutshell" keeps its dash.
// A title that
// would end up empty, and any title of a key without a path, is returned
// as it is.
func OwnName(key, title string, fromFeed bool) string {
	if !hasPath(key) {
		return title
	}
	name := strings.TrimSpace(title)
	if p, ok := platformFor(KeyHost(key)); ok && p.ownName != nil {
		name = p.ownName(key, name, fromFeed)
	}
	if !fromFeed {
		name = platformSuffix.ReplaceAllString(name, "")
		name = handleSuffix.ReplaceAllString(name, "")
	}
	if name = strings.TrimSpace(name); name == "" {
		return title
	}
	return name
}

// Platform names the platform a site key's site is on, shown next to its
// name wherever it's named (#728): "" for a key without a path, the
// platform's name for a host with its own rule ("Medium"), else the host
// ("mastodon.social"), since the software behind an @name host isn't known.
func Platform(key string) string {
	if !hasPath(key) {
		return ""
	}
	host := KeyHost(key)
	if p, ok := platformFor(host); ok {
		return p.name
	}
	return host
}

// Kind is what sort of site a site key names, for My feeds' filter (#862).
// Kinds are named by medium, not platform, so a new platform joins a kind.
type Kind string

const (
	KindWebsites Kind = "websites"
	KindVideos   Kind = "videos"
	KindSocial   Kind = "social"
)

// KindOf is the kind of the site with this key: videos for YouTube, social
// for Bluesky and Mastodon accounts, websites for everything else. A
// Mastodon account is a key whose path starts with "@" on a host without
// its own rules ("mastodon.social/@bob"; "medium.com/@alice" is Medium's).
func KindOf(key string) Kind {
	if !hasPath(key) {
		return KindWebsites
	}
	host, path, _ := strings.Cut(key, "/")
	if p, ok := platformFor(host); ok {
		return p.kind
	}
	if isAtName(path) {
		return KindSocial
	}
	return KindWebsites
}
