// Package seed reads the lists of sites the admin pastes into the seed
// list, the sites the app looks up before anyone searches for them; the
// list itself is the seed_sites table. See "Feed discovery" and "Admin" in
// docs/decisions.md.
package seed

import (
	"strings"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
)

// Entry is one site in a seed list.
type Entry struct {
	URL     string // discovery.ParseInput of the line, as a string
	SiteKey string // discovery.SiteKey of URL
}

// The outcome of one line of an upload (Line.Outcome).
const (
	LineAdd        = "add"         // can be added
	LineOnList     = "on-list"     // its site is already on the seed list (set by the store)
	LineDuplicate  = "duplicate"   // the same site as an earlier line
	LineBadAddress = "bad-address" // not a website address (discovery.ParseInput)
	LineBadSection = "bad-section" // names no section
	LineAppLink    = "app-link"    // an address on the reader's own site (-app-url)
)

// Line is one non-blank, non-comment line of an upload.
type Line struct {
	N         int    // its line number, from 1
	Text      string // as typed, trimmed
	Entry            // URL and SiteKey; empty for a bad address
	SectionID string // "" for no section
	Outcome   string
	SameAs    int // for LineDuplicate, the earlier line's N
}

// Parse reads an upload: one address per line, optionally followed by a
// comma or tab and a section's slug or name (any case). appSiteKey is the
// reader's own site key, whose addresses are refused.
//
// The address ends at the line's first comma or tab, since an address
// has no spaces and seldom a comma, while a section's name may have both.
// Blank lines and lines starting with "#" are skipped but counted in the
// line numbers. A line is a duplicate only of an earlier line that can be
// added, so fixing the first of two lines for one site isn't needed to add
// the second.
func Parse(text string, sections []rss.Section, appSiteKey string) []Line {
	var lines []Line
	seen := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		l := Line{N: i + 1, Text: t}
		addr, section := t, ""
		if j := strings.IndexAny(t, ",\t"); j >= 0 {
			addr, section = strings.TrimSpace(t[:j]), strings.TrimSpace(t[j+1:])
		}
		u, err := discovery.ParseInput(addr)
		if err != nil {
			l.Outcome = LineBadAddress
			lines = append(lines, l)
			continue
		}
		l.Entry = Entry{URL: u.String(), SiteKey: discovery.SiteKey(u)}
		switch {
		case appSiteKey != "" && l.SiteKey == appSiteKey:
			l.Outcome = LineAppLink
		case section != "" && !findSection(sections, section, &l.SectionID):
			l.Outcome = LineBadSection
		case seen[l.SiteKey] > 0:
			l.Outcome, l.SameAs = LineDuplicate, seen[l.SiteKey]
		default:
			l.Outcome = LineAdd
			seen[l.SiteKey] = l.N
		}
		lines = append(lines, l)
	}
	return lines
}

// Section returns the section text after a line's address: what it named,
// for a LineBadSection's message. "" when it named none.
func (l Line) Section() string {
	if j := strings.IndexAny(l.Text, ",\t"); j >= 0 {
		return strings.TrimSpace(l.Text[j+1:])
	}
	return ""
}

// findSection sets *id to the ID of the section whose slug or name is
// text, ignoring case, and reports whether there is one.
func findSection(sections []rss.Section, text string, id *string) bool {
	for _, s := range sections {
		if strings.EqualFold(s.Slug, text) || strings.EqualFold(s.Name, text) {
			*id = s.ID
			return true
		}
	}
	return false
}
