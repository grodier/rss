// Package rss holds the application's domain types and errors.
package rss

import (
	"strings"
	"time"
)

type Feed struct {
	ID          string
	SiteID      string
	Url         string
	SiteUrl     string
	Title       string
	Description string
	LastFetched time.Time

	LastAttempt         time.Time // zero if never attempted
	LastError           string    // internal detail; never shown to users
	ConsecutiveFailures int
	NextFetch           time.Time

	ETag         string // of the last 2xx response; "" if none
	LastModified string // of the last 2xx response; "" if none

	GoneAt             time.Time // zero unless the feed answered 410
	RefreshRequestedAt time.Time // zero unless a refresh was requested and its outcome isn't recorded yet

	CreatedAt time.Time
}

// Article is an item of a feed. Summary and Content are raw HTML as
// published and must be sanitized before being rendered as HTML.
type Article struct {
	ID           string
	FeedID       string
	ExternalID   string
	URL          string
	CanonicalURL string // see ingest.CanonicalURL; "" if the article has no usable URL
	ImageURL     string // feed-declared image; "" if none
	Title        string
	Summary      string
	Content      string
	Excerpt      string    // plain text, at most ingest.ExcerptLen runes plus "…"
	PublishedAt  time.Time // zero if unknown
	TimelineAt   time.Time // position in timelines; see psql.timelineAt
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// displayTitleLen is the most runes of an excerpt DisplayTitle uses.
const displayTitleLen = 80

// DisplayTitle returns Title; for an untitled article, the start of its
// excerpt (at most 80 runes, cut at a word boundary, with "…" if cut);
// else "(untitled)".
func (a Article) DisplayTitle() string {
	if a.Title != "" {
		return a.Title
	}
	if a.Excerpt != "" {
		return Shorten(a.Excerpt, displayTitleLen)
	}
	return "(untitled)"
}

// Shorten returns s unchanged if it has at most n runes. Otherwise it cuts
// s at the last space at or before n runes (or at n if there is none),
// trims trailing space and appends "…".
func Shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := n
	for i := n; i > 0; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

// FeedWithArticles is a feed and articles to save for it.
type FeedWithArticles struct {
	Feed     Feed
	Articles []Article
}

// FetchResult counts the articles a fetch saved.
type FetchResult struct {
	New         int  // articles inserted
	Updated     int  // existing articles whose url, image, title, summary, content or missing date changed
	NotModified bool // the server answered 304
}

// Site is a website that feeds are discovered from. Host is its site key:
// the lowercase host without a leading "www." (see discovery.SiteKey).
type Site struct {
	ID          string
	Host        string
	URL         string
	Title       string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Subscription is a feed a user follows, with its site.
type Subscription struct {
	Feed            Feed
	Site            Site
	LatestArticleAt time.Time // newest article's published_at, else created_at; zero if the feed has no articles
}

// SiteWithFeeds is a site and its feeds.
type SiteWithFeeds struct {
	Site  Site
	Feeds []Feed
}

type User struct {
	ID             string
	Name           string
	Email          string
	HashedPassword []byte
	CreatedAt      time.Time
}

// LookupStatus is the state of a site lookup.
type LookupStatus string

const (
	LookupPending LookupStatus = "pending"
	LookupRunning LookupStatus = "running"
	LookupDone    LookupStatus = "done"
	LookupFailed  LookupStatus = "failed"
)

// Lookup is a background lookup of a site's feeds. It is both a job in the
// lookup queue and the cached result for its site key.
type Lookup struct {
	ID          string
	SiteKey     string
	URL         string
	Status      LookupStatus
	SiteID      string // "" if none
	Error       string // internal detail; never shown to users
	Attempts    int
	RequestedAt time.Time
	StartedAt   time.Time // zero if never started
	FinishedAt  time.Time // zero if not finished
}

// ArticleCursor is a position in a sorted list of articles: the sort time
// and ID of the last article already shown. The zero value is the start.
type ArticleCursor struct {
	At time.Time
	ID string
}

func (c ArticleCursor) IsZero() bool { return c.ID == "" }

// ArticleWithFeed is an article and the feed it's from.
type ArticleWithFeed struct {
	Article Article
	Feed    Feed
}
