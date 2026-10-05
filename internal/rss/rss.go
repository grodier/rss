// Package rss holds the application's domain types and errors.
package rss

import "time"

type Feed struct {
	ID          string
	SiteID      string // empty when the feed has no site
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
