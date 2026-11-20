// Package seed reads lists of sites for the seed list, the sites the app
// looks up before anyone searches for them; the list itself is the
// seed_sites table, which the admin edits. See "Feed discovery" in
// docs/decisions.md.
package seed

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/grodier/rss/internal/discovery"
)

// Entry is one site in a seed list.
type Entry struct {
	URL     string // discovery.ParseInput of the line, as a string
	SiteKey string // discovery.SiteKey of URL
}

// Parse reads a seed list: one website address per line. Blank lines and
// lines starting with "#" are ignored, and an empty list is fine. It's an
// error for an address not to parse with discovery.ParseInput or to share
// its site key with an earlier one, since a site has one lookup.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	seen := map[string]int{}

	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := discovery.ParseInput(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %q isn't a website address", n, line)
		}
		key := discovery.SiteKey(u)
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("line %d: %q is the same site (%s) as line %d; list each site once", n, line, key, prev)
		}
		seen[key] = n
		entries = append(entries, Entry{URL: u.String(), SiteKey: key})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
