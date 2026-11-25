// Package rss holds the application's domain types and errors.
package rss

import (
	"fmt"
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
	Categories  []string // what it says it's about (see feedparse.Meta.Categories); shown only to admins; nil if none
	LastFetched time.Time

	LastAttempt         time.Time // zero if never attempted
	LastError           string    // internal detail; never shown to users
	ConsecutiveFailures int
	NextFetch           time.Time

	ETag         string // of the last 2xx response; "" if none
	LastModified string // of the last 2xx response; "" if none

	GoneAt             time.Time // zero unless the feed answered 410
	RefreshRequestedAt time.Time // zero unless a refresh was requested and its outcome isn't recorded yet

	PostsPerWeek float64 // articles published in the last 30 days, per week

	CreatedAt time.Time
}

// FeedFacts is what a site's page shows about one of its feeds beyond the
// feed itself.
type FeedFacts struct {
	Section  Section   // its section in Discover's catalog; zero if it isn't there
	LatestAt time.Time // when its newest article was published (or saved, if undated); zero if it has none
}

// Article is an item of a feed. Summary and Content are raw HTML as
// published and must be sanitized before being rendered as HTML.
type Article struct {
	ID           string
	FeedID       string
	ExternalID   string
	URL          string
	CanonicalURL string   // see ingest.CanonicalURL; "" if the article has no usable URL
	ImageURL     string   // feed-declared image; "" if none
	MediaType    string   // see feedparse.Item.MediaType
	Authors      []string // see feedparse.Item.Authors
	Title        string
	Summary      string
	Content      string
	Excerpt      string // plain text, at most ingest.ExcerptLen runes plus "…"
	// Set by enrichment (internal/enrich) from the article's web page, when
	// the feed left out its image, title or excerpt; never the feed's values.
	PageImageURL    string // og:image of the article's web page; "" if none or not fetched
	PageTitle       string // og:title, else <title>, of the article's web page
	PageDescription string // the page's description, shortened like Excerpt
	// ImageFailedAt is when a reader's browser first reported that ImageURL
	// failed to load (see ReportsImageFailure); zero if none has.
	ImageFailedAt time.Time
	PublishedAt   time.Time // zero if unknown
	// PublishedDateOnly reports that the feed's date had no time of day:
	// PublishedAt is midnight UTC and only its date is meaningful.
	PublishedDateOnly bool
	TimelineAt        time.Time // position in timelines; see psql.timelineAt
	Backlog           bool      // placed as backlog (see psql.isBacklog), so readers never get their own position for it
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// UnfollowedRefreshInterval is the shortest a feed with no subscribers
// waits between fetches, whatever its posting rate, so feeds kept fresh for
// Discover cost about one fetch a day each (see psql.FeedRepository.ClaimDue).
const UnfollowedRefreshInterval = 24 * time.Hour

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

// Byline returns the article's authors as one phrase ("A", "A and B",
// "A, B and C"); "" if it has none.
func (a Article) Byline() string {
	switch n := len(a.Authors); n {
	case 0:
		return ""
	case 1:
		return a.Authors[0]
	default:
		return strings.Join(a.Authors[:n-1], ", ") + " and " + a.Authors[n-1]
	}
}

// ShortByline returns Byline for up to two authors, else the first author
// and how many others ("A and 2 others"), for rows where space is short.
func (a Article) ShortByline() string {
	if n := len(a.Authors); n > 2 {
		return fmt.Sprintf("%s and %d others", a.Authors[0], n-1)
	}
	return a.Byline()
}

// CardImageURL returns the feed's image for the article, else the image of
// its web page, with https for http since the CSP's img-src allows only
// https: images (sanitize.HTML does the same for images in article HTML);
// "" if neither. A change here updates server.articleProvenance too.
func (a Article) CardImageURL() string {
	if a.ImageURL != "" {
		return httpsImage(a.ImageURL)
	}
	return httpsImage(a.PageImageURL)
}

// CardImageFallbackURL returns the image to try when CardImageURL fails to
// load in the browser: the web page's image, over https, when the feed's
// image is the one shown and the page's is a different one; else "".
func (a Article) CardImageFallbackURL() string {
	if a.ImageURL == "" || a.PageImageURL == "" {
		return ""
	}
	if fb := httpsImage(a.PageImageURL); fb != a.CardImageURL() {
		return fb
	}
	return ""
}

// ReportsImageFailure reports whether the browser should tell the server
// when the card image fails to load: the image shown is the feed's, and no
// failure of it has been reported yet. The server then has the article's
// web page fetched for an image to stand in (psql's ReportImageFailed),
// since an article with a feed image is otherwise never enriched.
func (a Article) ReportsImageFailure() bool {
	return a.ImageURL != "" && a.ImageFailedAt.IsZero()
}

// httpsImage returns img with https for an http scheme, else unchanged.
func httpsImage(img string) string {
	u, err := url.Parse(img)
	if err != nil || u.Scheme != "http" {
		return img
	}
	u.Scheme = "https"
	return u.String()
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
	Behind      bool // a later save (not one placed as a first save) of two or more articles, all new: the feed may have published more than its file holds
}

// Site is a website that feeds are discovered from. Host is its site key:
// the lowercase host without a leading "www.", followed on a host shared by
// many sites by the path that names the site ("medium.com/@alice"; see
// discovery.SiteKey). URL is its root (discovery.SiteRoot).
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

// SiteWithFeeds is a site and its feeds.
type SiteWithFeeds struct {
	Site  Site
	Feeds []Feed
}

// SiteGroup is the sites on one shared host that matched a search only
// through their key, handle, platform or feeds, counted rather than listed
// (#739).
type SiteGroup struct {
	Host  string // discovery.KeyHost of their keys: "bsky.app"
	Count int
}
