// Package seed reads the seed list, sites.txt, a list of sites embedded in
// the binary, and queues their lookups so search finds them before anyone
// has looked them up. See "Feed discovery" in docs/decisions.md.
package seed

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

// Entry is one site in the seed list.
type Entry struct {
	URL     string // discovery.ParseInput of the line, as a string
	SiteKey string // discovery.SiteKey of URL
}

//go:embed sites.txt
var sitesFile []byte

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

// Entries returns the embedded seed list's entries; it panics if sites.txt
// doesn't parse, which TestEmbeddedList prevents.
func Entries() []Entry {
	entries, err := Parse(bytes.NewReader(sitesFile))
	if err != nil {
		panic("seed: sites.txt: " + err.Error())
	}
	return entries
}

// RetryAfter is how long after a seeded site's lookup failed or found no
// feeds Run queues it again: the time a reader's lookup result is kept for
// (lookupFailedTTL in internal/server), so a site isn't fetched more often
// than a reader could make it.
const RetryAfter = time.Hour

// Result is what Run did.
type Result struct {
	// Queued is how many lookups it queued for sites never looked up.
	Queued int
	// Retried lists the site keys whose lookup had failed or found no feeds
	// and was queued again.
	Retried []string
	// Missing lists the site keys whose lookup failed or found no feeds
	// less than RetryAfter ago, so weren't retried: a later Run retries
	// them.
	Missing []string
}

// Store queues the seed list's lookups; *psql.SeedRepository satisfies it.
type Store interface {
	// Queue queues a lookup, behind readers', for each entry whose site key
	// has none, and re-queues each entry's lookup that failed or found no
	// feeds over retryAfter ago. A lookup that found a site, or is pending
	// or running, is left as it is.
	Queue(ctx context.Context, entries []Entry, retryAfter time.Duration) (Result, error)
}

// Run queues lookups of the seed list's sites that have never been looked
// up, or whose lookup failed or found no feeds over RetryAfter ago. Sites
// already found aren't fetched again, so it's safe to run on every deploy.
func Run(ctx context.Context, store Store, entries []Entry) (Result, error) {
	res, err := store.Queue(ctx, entries, RetryAfter)
	if err != nil {
		return Result{}, fmt.Errorf("queue seed lookups: %w", err)
	}
	return res, nil
}
