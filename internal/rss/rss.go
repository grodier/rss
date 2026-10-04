// Package rss holds the application's domain types and errors.
package rss

import "time"

type Feed struct {
	ID          string
	Url         string
	SiteUrl     string
	Title       string
	Description string
	LastFetched time.Time
	CreatedAt   time.Time
}

type User struct {
	ID             string
	Name           string
	Email          string
	HashedPassword []byte
	CreatedAt      time.Time
}
