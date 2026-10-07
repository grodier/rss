// Package rss holds the application's domain types and errors.
package rss

import "time"

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

	GoneAt time.Time // zero unless the feed answered 410

	CreatedAt time.Time
}

// Article is an item of a feed. Summary and Content are raw HTML as
// published and must be sanitized before being rendered as HTML.
type Article struct {
	ID          string
	FeedID      string
	ExternalID  string
	URL         string
	Title       string
	Summary     string
	Content     string
	PublishedAt time.Time // zero if unknown
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FeedWithArticles is a feed and articles to save for it.
type FeedWithArticles struct {
	Feed     Feed
	Articles []Article
}

// FetchResult counts the articles a fetch saved.
type FetchResult struct {
	New         int  // articles inserted
	Updated     int  // existing articles whose url, title, summary, content or missing date changed
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
