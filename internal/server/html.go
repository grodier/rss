package server

import (
	"bytes"
	"cmp"
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

	"github.com/grodier/rss/internal/discovery"
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
	"postingRate": postingRate,
	"sinceDate":   func(t time.Time) sinceView { return newSinceView(t, time.Now()) },
	"siteName":    siteName,
	"platform":    discovery.Platform,
}

// siteName is how a site is named wherever it's listed: its title, else its
// key, followed for a site on a shared host by its platform ("Alice ·
// Medium"), so a reader always sees where it is (#728). An untitled site's
// key already says.
func siteName(s rss.Site) string {
	name := cmp.Or(s.Title, s.Host)
	if p := discovery.Platform(s.Host); p != "" && name != s.Host {
		name += " · " + p
	}
	return name
}

// sinceRelativeFor is how long since shows a time as relative ("3 weeks
// ago"); past it, a month and year ("Sep 2025") is quicker to judge than a
// count of months, and says more.
const sinceRelativeFor = 60 * 24 * time.Hour

// since says how recently t was, for "Latest post {when}" and "Saved
// {when}" (through the since-date partial): "today" (or a future t), "yesterday", "N days ago" up to two
// weeks, then weeks, and from sinceRelativeFor its UTC month and year
// ("Sep 2025", which local-times.ts shows in the reader's format through
// the since-date partial). Days are 24-hour spans, not calendar days: the
// server doesn't know the reader's time zone.
func since(t, now time.Time) string {
	d := now.Sub(t)
	if d >= sinceRelativeFor {
		return t.UTC().Format("Jan 2006")
	}
	days := int(d / (24 * time.Hour))
	switch {
	case days < 1:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 14:
		return fmt.Sprintf("%d days ago", days)
	default:
		return fmt.Sprintf("%d weeks ago", days/7)
	}
}

// sinceView is what the since-date partial shows: since's text, and whether
// it's relative ("3 days ago", which the script leaves alone) or a month and
// year (which it shows in the reader's format).
type sinceView struct {
	Text     string
	Relative bool
}

func newSinceView(t, now time.Time) sinceView {
	return sinceView{Text: since(t, now), Relative: now.Sub(t) < sinceRelativeFor}
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
	Section string // "home", "saved", "feeds", "discover", "settings" or ""
	User    rss.User
	Debug   bool
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

// Layout is the data-layout attribute of the page's <html>: "cards" if the
// user chose Cards, "" for Headlines, which the stylesheets draw without the
// attribute (so pages that don't know the user, like a shared timeline, get
// it too).
func (n navData) Layout() string {
	if n.User.Layout == rss.LayoutCards {
		return rss.LayoutCards
	}
	return ""
}

// nav returns the navData for a logged-in page, with the user from
// authenticatedSession.
func (s *Server) nav(r *http.Request, section string) navData {
	sess, _ := s.authenticatedSession(r)
	return navData{Section: section, User: sess.User, Debug: s.debugEnabled(r)}
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
