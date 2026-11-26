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
	// mediumFeed is a Medium author's feed title, which names a site found
	// from the feed itself: "Stories by Alice on Medium".
	mediumFeed = regexp.MustCompile(`^Stories by (.+) on Medium$`)
)

// OwnName is a site's title as the site names itself (#728), for a key with
// a path; the platform is shown apart from the name (Platform). A title from
// the platform's page (fromFeed false) loses the platform's suffix, a
// trailing handle and, on GitHub, "GitHub - " and a repository's
// description after "owner/repo:", so "Alice – Medium" -> "Alice". A title
// from a feed is the feed's own name, so only Medium's "Stories by … on
// Medium" goes: "Kurzgesagt – In a Nutshell" keeps its dash. A title that
// would end up empty, and any title of a key without a path, is returned
// as it is.
func OwnName(key, title string, fromFeed bool) string {
	if !hasPath(key) {
		return title
	}
	name := strings.TrimSpace(title)
	if m := mediumFeed.FindStringSubmatch(name); m != nil {
		name = m[1]
	}
	if fromFeed {
		if name == "" {
			return title
		}
		return name
	}
	if KeyHost(key) == "github.com" {
		// "GitHub - owner/repo: description", or "owner · GitHub".
		name = strings.TrimSuffix(strings.TrimPrefix(name, "GitHub - "), " · GitHub")
	}
	name = platformSuffix.ReplaceAllString(name, "")
	name = handleSuffix.ReplaceAllString(name, "")
	if strings.Count(key, "/") == 2 && KeyHost(key) == "github.com" {
		// A repository's "owner/repo: description"; an owner's page has
		// no description to cut.
		if repo, _, ok := strings.Cut(name, ": "); ok {
			name = repo
		}
	}
	if name = strings.TrimSpace(name); name == "" {
		return title
	}
	return name
}

// platform is a host whose site keys have their own rules (rootPath): its
// name, shown next to a site's name (Platform), and the kind of site its
// keys name (KindOf).
type platform struct {
	name string
	kind Kind
}

// platforms are the hosts whose site keys have their own rules (rootPath).
var platforms = map[string]platform{
	"medium.com":  {"Medium", KindWebsites},
	"youtube.com": {"YouTube", KindVideos},
	"reddit.com":  {"Reddit", KindWebsites},
	"github.com":  {"GitHub", KindWebsites},
	"bsky.app":    {"Bluesky", KindSocial},
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
	if p, ok := platforms[host]; ok {
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
	if p, ok := platforms[host]; ok {
		return p.kind
	}
	if isAtName(path) {
		return KindSocial
	}
	return KindWebsites
}
