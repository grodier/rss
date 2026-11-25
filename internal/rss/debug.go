package rss

import "time"

// SiteDebug is what a site's debug panel shows beyond Site.
type SiteDebug struct {
	IconCheckedAt   time.Time // zero if the icon worker never looked
	IconNextCheckAt time.Time
	Icon            SiteIcon // zero (SiteID "") if the site has no icon; Data is left empty
	IconBytes       int      // size of the stored icon; 0 if none
}

// FeedDebug is what a feed's debug panel shows beyond Feed.
type FeedDebug struct {
	Articles         int
	NewestCreatedAt  time.Time // zero if the feed has no articles
	NewestTimelineAt time.Time // likewise
	EnrichJobs       int       // enrich_jobs rows of the feed's articles
}

// ArticleDebug is what an article's debug panel shows beyond Article.
type ArticleDebug struct {
	EnrichJob EnrichJobState // Exists is false if the article has no enrich_jobs row
	Copies    []ArticleCopy  // other articles with the same non-empty canonical_url, oldest first, at most 20
	ReadAt    time.Time      // this user's reads row for this article; zero if none
	SavedAt   time.Time      // this user's saves row for this article; zero if none
}

// EnrichJobState is an article's enrich_jobs row.
type EnrichJobState struct {
	Exists        bool
	Priority      int
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
}

// ArticleCopy is a copy of an article in another feed.
type ArticleCopy struct {
	ArticleID  string
	FeedID     string
	FeedTitle  string // the feed's title, else its URL
	CreatedAt  time.Time
	TimelineAt time.Time
}
