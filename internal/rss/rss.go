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
	CreatedAt   time.Time
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
