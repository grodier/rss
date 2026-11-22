package discovery

import (
	"regexp"
	"strings"
)

// The parts of a path-scoped site's title that name its platform or
// repeat its key, removed by OwnName.
var (
	// platformSuffix is the last " - ", " – ", " — " or " | " and what
	// follows it: "Alice – Medium", "Channel - YouTube". psql's ownName
	// matches the same suffix in SQL, for search.
	platformSuffix = regexp.MustCompile(`\s+[-–—|]\s+[^-–—|]*$`)
	// handleSuffix is a trailing "(@handle)": "Bob (@bob@mastodon.social)".
	handleSuffix = regexp.MustCompile(`\s*\(@[^()]*\)$`)
	// mediumFeed is a Medium author's feed title, which names a site found
	// from the feed itself: "Stories by Alice on Medium".
	mediumFeed = regexp.MustCompile(`^Stories by (.+) on Medium$`)
)

// OwnName is a site's title as the site names itself (#728): for a key with
// a path, without the platform's suffix, a trailing handle, Medium's
// "Stories by … on Medium" or, on GitHub, "GitHub - " and the description
// after "owner/repo:", so "Alice – Medium" -> "Alice". The platform is shown
// apart from the name (Platform). A title that would end up empty, and any
// title of a key without a path, is returned as it is.
func OwnName(key, title string) string {
	if !hasPath(key) {
		return title
	}
	name := strings.TrimSpace(title)
	if m := mediumFeed.FindStringSubmatch(name); m != nil {
		name = m[1]
	}
	if KeyHost(key) == "github.com" {
		// "GitHub - owner/repo: description", or "owner · GitHub".
		name = strings.TrimSuffix(strings.TrimPrefix(name, "GitHub - "), " · GitHub")
	}
	name = platformSuffix.ReplaceAllString(name, "")
	name = handleSuffix.ReplaceAllString(name, "")
	if KeyHost(key) == "github.com" {
		if owner, _, ok := strings.Cut(name, ": "); ok {
			name = owner
		}
	}
	if name = strings.TrimSpace(name); name == "" {
		return title
	}
	return name
}

// platforms names the hosts whose site keys have their own rules (rootPath).
var platforms = map[string]string{
	"medium.com":  "Medium",
	"youtube.com": "YouTube",
	"reddit.com":  "Reddit",
	"github.com":  "GitHub",
	"bsky.app":    "Bluesky",
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
	if name, ok := platforms[host]; ok {
		return name
	}
	return host
}
