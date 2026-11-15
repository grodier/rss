package rss

import (
	"strings"
	"time"
)

// Section is one of Discover's sections, a discover_sections row. A feed
// in Discover's catalog is under one of them; sections belong to Discover
// only, never to a reader's subscriptions ("First run and discovery" in
// docs/decisions.md). The rows are the list: code never names a section.
type Section struct {
	ID   string
	Slug string // in URLs: "film-and-tv"
	Name string // as shown: "Film and TV"
}

// DiscoverSection is a section as Discover shows it to one reader: some of
// its catalog feeds the reader doesn't follow.
type DiscoverSection struct {
	Name, Slug string
	Total      int // catalog feeds in the section the reader doesn't follow
	Feeds      []DiscoverFeed
}

// DiscoverFeed is a catalog feed as Discover shows it.
type DiscoverFeed struct {
	Feed Feed // its PostsPerWeek drives "About N posts a week"
	Site Site // name, host and icon
	// Latest is its newest articles, at most 3, newest first: ID, the
	// fields DisplayTitle reads, PublishedAt and CreatedAt only.
	Latest []Article
}

// LatestAt is when its newest article was published (or saved, if
// undated); zero if it has none.
func (f DiscoverFeed) LatestAt() time.Time {
	if len(f.Latest) == 0 {
		return time.Time{}
	}
	return f.Latest[0].FeedSortAt()
}

// Name is how Discover names the feed's site: its title, else its host.
func (f DiscoverFeed) Name() string {
	if f.Site.Title != "" {
		return f.Site.Title
	}
	return f.Site.Host
}

// FeedTitle is the feed's own title when Discover shows it under the site's
// name: "" if it's empty or just the site's name again.
func (f DiscoverFeed) FeedTitle() string {
	if t := f.Feed.Title; t != "" && !strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(f.Name())) {
		return t
	}
	return ""
}
