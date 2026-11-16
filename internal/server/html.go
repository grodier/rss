package server

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
)

// avatarTones is how many avatar tints avatars.css defines (.tone-1 … .tone-6).
const avatarTones = 6

var templateFuncs = template.FuncMap{
	"asset":       asset,
	"assetTags":   assetTags,
	"avatar":      avatar,
	"avatarTone":  avatarTone,
	"debugPanel":  newDebugPanel,
	"discoverRow": newDiscoverRow,
	"followButton": func(feedID, label, returnTo string) followButton {
		return followButton{FeedID: feedID, Label: label, ReturnTo: returnTo}
	},
	"homeName":    func() string { return homeName },
	"monogram":    monogram,
	"myFeedRow":   newMyFeedRow,
	"postingRate": postingRate,
	"searchResultRow": func(r searchResult, mixed bool) any {
		return struct {
			Result searchResult
			Mixed  bool
		}{r, mixed}
	},
	"since":      func(t time.Time) string { return since(t, time.Now()) },
	"siteResult": newSiteResult,
}

// since says how long before now t was, by whole days, for "Latest {when}":
// "today" (or a future t), "yesterday", "N days ago" up to two weeks, then
// weeks up to two months, then months, then years.
func since(t, now time.Time) string {
	days := int(now.Sub(t) / (24 * time.Hour))
	switch {
	case days < 1:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 14:
		return fmt.Sprintf("%d days ago", days)
	case days < 60:
		return fmt.Sprintf("%d weeks ago", days/7)
	case days < 730:
		return fmt.Sprintf("%d months ago", days/30)
	default:
		return fmt.Sprintf("%d years ago", days/365)
	}
}

// avatarView is what the avatar partial shows: the site's icon, else its
// letter on its tint.
type avatarView struct {
	Tone    int
	Letter  string
	IconURL string
}

// avatar builds the avatar of a site, or of a feed from its site: id is the
// site's ID, name what the letter comes from, iconURL the site's icon URL
// (from siteIconURLs) or "".
func avatar(id, name, iconURL string) avatarView {
	return avatarView{Tone: avatarTone(id), Letter: monogram(name), IconURL: iconURL}
}

// avatarTone picks the avatar tint for an ID, from 1 to avatarTones: a site's
// ID, so a site and its feeds always get the same color, or a user's.
func avatarTone(siteID string) int {
	h := fnv.New32a()
	h.Write([]byte(siteID))
	return int(h.Sum32()%avatarTones) + 1
}

// monogram is the letter shown in an avatar: the first letter or digit of
// name, upper-cased, skipping "http(s)://" and "www." so a feed known only
// by its URL gets its host's letter. It is "" if name has none.
func monogram(name string) string {
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimPrefix(name, "www.")
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return ""
}

// navData is what the app-nav partial shows: the current section, the
// logged-in user, and whether debug mode is on.
type navData struct {
	Section string // "home", "saved", "feeds", "discover", "search", "settings" or ""
	User    rss.User
	Debug   bool
	// IA is the design checkpoint's ?ia= (#645): with "e", Discover is the
	// search place, so it takes the magnifier and Search's entries go.
	// Temporary.
	IA string
}

// Theme is the data-theme attribute of the page's <html>: "light" or "dark"
// if the user chose one, "" to follow the system.
func (n navData) Theme() string {
	switch n.User.Theme {
	case rss.ThemeLight, rss.ThemeDark:
		return n.User.Theme
	}
	return ""
}

// nav returns the navData for a logged-in page, with the user from
// authenticatedSession.
func (s *Server) nav(r *http.Request, section string) navData {
	sess, _ := s.authenticatedSession(r)
	return navData{Section: section, User: sess.User, Debug: s.debugEnabled(r), IA: searchIA(r)}
}

// parseTemplates parses the page templates once per process: they're
// embedded, so every Server can share them (tests build hundreds of Servers).
// Callers must not modify the map or the templates.
var parseTemplates = sync.OnceValues(func() (map[string]*template.Template, error) {
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	ts := map[string]*template.Template{}
	for _, page := range pages {
		name := filepath.Base(page)
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(ui.Templates, page, "templates/partials/*.html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		ts[name] = t
	}
	return ts, nil
})

func (s *Server) renderHTML(w http.ResponseWriter, status int, name string, data any) error {
	t, ok := s.templates[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
