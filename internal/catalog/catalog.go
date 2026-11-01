// Package catalog reads the site catalog, hand-picked lists of sites by topic
// embedded in the binary, and seeds it into the database: browse.txt, the
// sites Browse lists on the Search page, and seed.txt, the many more that are
// only searchable. See "Feed discovery" in docs/decisions.md.
package catalog

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/grodier/rss/internal/discovery"
)

// Entry is one site in the catalog.
type Entry struct {
	Topic    string // the [Topic] heading it's under
	URL      string // discovery.ParseInput of the line, as a string
	SiteKey  string // discovery.SiteKey of URL
	Position int    // order in the catalog, from 0
	Browse   bool   // listed in browse.txt, so shown on Browse
}

var (
	//go:embed browse.txt
	browseFile []byte
	//go:embed seed.txt
	seedFile []byte
)

// Parse reads a catalog file: [Topic] headings, each followed by one website
// address per line. Blank lines and lines starting with "#" are ignored. It's
// an error for an address to come before any heading, not to parse with
// discovery.ParseInput, or to share its site key with an earlier entry, and
// for a topic to have no entries or to appear twice.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	topic := ""
	topicLine := 0
	topicEntries := 0
	seenTopics := map[string]bool{}
	seenKeys := map[string]int{}

	endTopic := func() error {
		if topic != "" && topicEntries == 0 {
			return fmt.Errorf("line %d: topic %q has no sites", topicLine, topic)
		}
		return nil
	}

	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if err := endTopic(); err != nil {
				return nil, err
			}
			name, ok := strings.CutSuffix(line[1:], "]")
			name = strings.TrimSpace(name)
			if !ok || name == "" {
				return nil, fmt.Errorf("line %d: %q isn't a [Topic] heading", n, line)
			}
			if seenTopics[name] {
				return nil, fmt.Errorf("line %d: topic %q appears twice", n, name)
			}
			seenTopics[name] = true
			topic, topicLine, topicEntries = name, n, 0
			continue
		}

		if topic == "" {
			return nil, fmt.Errorf("line %d: %q comes before any [Topic] heading", n, line)
		}
		u, err := discovery.ParseInput(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %q isn't a website address", n, line)
		}
		key := discovery.SiteKey(u)
		if prev, ok := seenKeys[key]; ok {
			return nil, fmt.Errorf("line %d: %q is the same site (%s) as line %d", n, line, key, prev)
		}
		seenKeys[key] = n
		entries = append(entries, Entry{Topic: topic, URL: u.String(), SiteKey: key, Position: len(entries)})
		topicEntries++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := endTopic(); err != nil {
		return nil, err
	}
	return entries, nil
}

// Load reads the catalog's two lists: browse, the sites Browse shows, and
// seed, the sites that are only searchable. It returns browse's entries,
// marked Browse, then seed's, with positions numbered across both. Each list
// must Parse, and it's an error for a site key to be in both.
func Load(browse, seed io.Reader) ([]Entry, error) {
	b, err := Parse(browse)
	if err != nil {
		return nil, fmt.Errorf("browse.txt: %w", err)
	}
	s, err := Parse(seed)
	if err != nil {
		return nil, fmt.Errorf("seed.txt: %w", err)
	}
	inBrowse := make(map[string]bool, len(b))
	for i := range b {
		b[i].Browse = true
		inBrowse[b[i].SiteKey] = true
	}
	for i := range s {
		if inBrowse[s[i].SiteKey] {
			return nil, fmt.Errorf("seed.txt: %s is also in browse.txt; list a site once", s[i].SiteKey)
		}
		s[i].Position = len(b) + i
	}
	return append(b, s...), nil
}

// Entries returns the embedded catalog's entries (Load of browse.txt and
// seed.txt); it panics if they don't load, which TestEmbeddedCatalog
// prevents.
func Entries() []Entry {
	entries, err := Load(bytes.NewReader(browseFile), bytes.NewReader(seedFile))
	if err != nil {
		panic("catalog: " + err.Error())
	}
	return entries
}

// RetryAfter is how long after a catalog site's lookup failed or found no
// feeds Sync queues it again: the time a reader's lookup result is kept for
// (lookupFailedTTL in internal/server), so a site isn't fetched more often
// than a reader could make it.
const RetryAfter = time.Hour

// Result is what Sync did.
type Result struct {
	// Queued is how many lookups it queued for sites never looked up.
	Queued int
	// Retried lists the site keys whose lookup had failed or found no feeds
	// and was queued again.
	Retried []string
	// Missing lists the site keys whose lookup failed or found no feeds
	// less than RetryAfter ago, so weren't retried: they're left out of
	// Browse until a later Sync retries them.
	Missing []string
}

// Store stores the catalog; *psql.CatalogRepository satisfies it.
type Store interface {
	// Replace replaces the stored catalog with entries, in one transaction,
	// and queues a lookup for each entry whose site key has none, or whose
	// lookup failed or found no feeds over retryAfter ago. A lookup that
	// found a site, or is pending or running, is left as it is.
	Replace(ctx context.Context, entries []Entry, retryAfter time.Duration) (Result, error)
}

// Sync replaces the stored catalog with entries and queues lookups of the
// sites that have never been looked up, or whose lookup failed or found no
// feeds over RetryAfter ago. Sites already found aren't fetched again, so
// running it on every deploy costs no outbound requests for them.
func Sync(ctx context.Context, store Store, entries []Entry) (Result, error) {
	res, err := store.Replace(ctx, entries, RetryAfter)
	if err != nil {
		return Result{}, fmt.Errorf("store catalog: %w", err)
	}
	return res, nil
}
