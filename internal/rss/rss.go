// Package rss holds the application's domain types and errors.
package rss

import (
	"net/url"
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
	MediaType    string // see feedparse.Item.MediaType
	Title        string
	Summary      string
	Content      string
	Excerpt      string // plain text, at most ingest.ExcerptLen runes plus "…"
	// Set by enrichment (internal/enrich) from the article's web page, when
	// the feed left out its image, title or excerpt; never the feed's values.
	PageImageURL    string    // og:image of the article's web page; "" if none or not fetched
	PageTitle       string    // og:title, else <title>, of the article's web page
	PageDescription string    // the page's description, shortened like Excerpt
	PublishedAt     time.Time // zero if unknown
	// PublishedDateOnly reports that the feed's date had no time of day:
	// PublishedAt is midnight UTC and only its date is meaningful.
	PublishedDateOnly bool
	TimelineAt        time.Time // position in timelines; see psql.timelineAt
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FeedSortAt is the article's position on its feed page: PublishedAt, else CreatedAt.
func (a Article) FeedSortAt() time.Time {
	if !a.PublishedAt.IsZero() {
		return a.PublishedAt
	}
	return a.CreatedAt
}

// URLHost returns the host of the article's URL without a leading "www.",
// or "" if it has no parseable http(s) URL.
func (a Article) URLHost() string {
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// displayTitleLen is the most runes of an excerpt DisplayTitle uses.
const displayTitleLen = 80

// DisplayTitle returns Title; else the title of the article's web page; else
// the start of DisplayExcerpt (at most 80 runes, cut at a word boundary, with
// "…" if cut); else "(untitled)". A change here updates
// server.articleProvenance too.
func (a Article) DisplayTitle() string {
	if a.Title != "" {
		return a.Title
	}
	if a.PageTitle != "" {
		return a.PageTitle
	}
	if e := a.DisplayExcerpt(); e != "" {
		return Shorten(e, displayTitleLen)
	}
	return "(untitled)"
}

// DisplayExcerpt returns Excerpt, else PageDescription. A change here updates
// server.articleProvenance too.
func (a Article) DisplayExcerpt() string {
	if a.Excerpt != "" {
		return a.Excerpt
	}
	return a.PageDescription
}

// CardImageURL returns the feed's image for the article, else the image of
// its web page; "" if neither. A change here updates server.articleProvenance
// too.
func (a Article) CardImageURL() string {
	if a.ImageURL != "" {
		return a.ImageURL
	}
	return a.PageImageURL
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

// ArticlePage is what enrichment read from an article's web page.
type ArticlePage struct {
	ImageURL    string
	Title       string
	Description string
}

// EnrichJob is an article whose web page is to be fetched.
type EnrichJob struct {
	ArticleID string
	URL       string
	Attempts  int // including this one
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

// SiteIcon is a site's icon as stored (a site_icons row).
type SiteIcon struct {
	SiteID       string
	SourceURL    string
	ContentType  string // image/png, image/jpeg, image/gif or image/x-icon
	Width        int
	Height       int
	Data         []byte
	Hash         string // hex SHA-256 of Data
	ETag         string
	LastModified string
	FetchedAt    time.Time
}

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

// Subscription is a feed a user follows, with its site.
type Subscription struct {
	Feed            Feed
	Site            Site
	Title           string    // the user's name for the feed; "" uses Feed.Title
	LatestArticleAt time.Time // newest article's published_at, else created_at; zero if the feed has no articles
}

// SiteWithFeeds is a site and its feeds.
type SiteWithFeeds struct {
	Site  Site
	Feeds []Feed
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

// Import is an OPML file a user uploaded.
type Import struct {
	ID        string
	UserID    string
	Filename  string
	CreatedAt time.Time
}

// ImportStatus is the state of one feed of an import.
type ImportStatus string

const (
	ImportPending           ImportStatus = "pending"
	ImportRunning           ImportStatus = "running"
	ImportSubscribed        ImportStatus = "subscribed"
	ImportAlreadySubscribed ImportStatus = "already_subscribed"
	ImportFailed            ImportStatus = "failed"
)

// ImportFailure says why a feed couldn't be imported; "" unless failed.
type ImportFailure string

const (
	ImportInvalidURL  ImportFailure = "invalid_url"
	ImportUnreachable ImportFailure = "unreachable"
	ImportNotFeed     ImportFailure = "not_feed"
)

// ImportFeed is one feed of an import: a job in the import queue and its result.
type ImportFeed struct {
	ImportID string
	UserID   string // the import's user; set by ClaimNext
	Position int
	URL      string
	Title    string
	Status   ImportStatus
	Failure  ImportFailure
	FeedID   string // "" until known
	Error    string // internal detail; never shown to users
	Attempts int
	// FeedTitle is the title of the feed FeedID names, "" if none; set by
	// ImportRepository.Get.
	FeedTitle string
}

// ImportSummary is an import with its feeds counted by status, for lists.
type ImportSummary struct {
	Import  Import
	Total   int
	Pending int // pending or running
	Failed  int
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
	AlsoIn  []Feed // other subscribed feeds with a copy of the article; timeline only
}

// SavedArticle is an article a user saved, with its feed.
type SavedArticle struct {
	Article Article
	Feed    Feed
	SavedAt time.Time
}

// User is an account.
type User struct {
	ID               string
	Name             string
	Email            string
	EmailConfirmedAt time.Time // zero until the email is confirmed
	CreatedAt        time.Time
}

// Token is a one-time email link (a user_tokens row).
type Token struct {
	Hash      []byte
	UserID    string
	Purpose   string // TokenConfirmEmail, TokenResetPassword or TokenChangeEmail
	Email     string // the address it was sent to
	ExpiresAt time.Time
}

// Token purposes.
const (
	TokenConfirmEmail  = "confirm_email"
	TokenResetPassword = "reset_password"
	TokenChangeEmail   = "change_email"
)

// Session is one logged-in device (a user_sessions row) with its user.
type Session struct {
	ID         string
	User       User
	LastSeenAt time.Time
}
