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
	PageImageURL    string    // og:image of the article's web page; "" if none or not fetched
	PageTitle       string    // og:title, else <title>, of the article's web page
	PageDescription string    // the page's description, shortened like Excerpt
	PublishedAt     time.Time // zero if unknown
	// PublishedDateOnly reports that the feed's date had no time of day:
	// PublishedAt is midnight UTC and only its date is meaningful.
	PublishedDateOnly bool
	TimelineAt        time.Time // position in timelines; see psql.timelineAt
	Backlog           bool      // placed as backlog (see psql.isBacklog), so readers never get their own position for it
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewsWindow is how far below its arrival (CreatedAt) a news article can be
// placed in timelines (see psql.timelineAt), and how recently a reader must
// have looked to get their own positions for what arrives (see psql's
// saveArticles and Subscribe).
const NewsWindow = 6 * time.Hour

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
	img := a.ImageURL
	if img == "" {
		img = a.PageImageURL
	}
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
	Tags            []Tag     // ordered by name, ignoring case; Feeds left zero
}

// Tag is a user's label for their subscriptions.
type Tag struct {
	ID    string
	Name  string
	Feeds int // how many of the user's subscriptions have it; set by TagRepository.List
}

// Timeline is a user's custom timeline, shown as a tab after Home. Home
// itself is the zero Timeline: every subscription, with no ID.
type Timeline struct {
	ID          string
	Name        string
	Description string
	Position    int
	Filter      TimelineFilter
	ShareToken  string // "" unless shared
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TimelineFilter says which of a user's articles a timeline shows. The zero
// value shows every subscription, as Home does.
type TimelineFilter struct {
	OnlyIncluded    bool // only the feeds of IncludeTagIDs and IncludeFeedIDs (timelines.all_feeds = false)
	IncludeTagIDs   []string
	IncludeFeedIDs  []string
	ExcludeTagIDs   []string // left out even when included
	ExcludeFeedIDs  []string
	StarredOnly     bool     // only articles the user starred, newest starred first
	MaxPostsPerWeek int      // only feeds posting at most this many articles a week; 0: any
	MediaOnly       bool     // only articles with an audio or video enclosure
	IncludeWords    []string // an article must mention one; empty: no condition
	ExcludeWords    []string // an article mentioning one is left out
}

// DefaultTimelines are the timelines every new account starts with, in tab
// order after Home: Quiet posters (feeds posting at most twice a week) and
// Starred. They're ordinary timelines, which the user can change or delete.
func DefaultTimelines() []Timeline {
	return []Timeline{
		{Name: "Quiet posters", Filter: TimelineFilter{MaxPostsPerWeek: 2}},
		{Name: "Starred", Filter: TimelineFilter{StarredOnly: true}},
	}
}

// VisitGap is how long a reader must be away before their next visit
// starts and the "You were here" divider moves.
const VisitGap = 30 * time.Minute

// What MarkSeen keeps of a reader's own positions (timeline_entries) at the
// start of each visit: every row above the previous visit's divider, but at
// least MinEntries and at most MaxEntries, the newest by position.
const (
	MinEntries = 100
	MaxEntries = 500
)

// TimelineMark is what decides where a timeline's "You were here" line
// goes: the timeline's own SeenUpTo and DividerAt (psql's TimelineLine), with
// the user's SeenAt, which says whether a new visit has started.
type TimelineMark struct {
	SeenUpTo  time.Time // the newest position the timeline has had on screen; zero if it never has
	SeenAt    time.Time // when the user last reported a position, on any timeline
	DividerAt time.Time // the line of the visit SeenAt belongs to; zero if the timeline wasn't seen before it
}

// Divider returns where the "You were here" line goes on a page rendered
// at now: the newest position the timeline had on screen in the last visit
// it was opened in. A new visit (at least VisitGap since the last report)
// puts it where the timeline was left. It's zero without a mark, during the
// first visit, and on a timeline never seen.
func (m TimelineMark) Divider(now time.Time) time.Time {
	if m.SeenAt.IsZero() {
		return time.Time{}
	}
	if now.Sub(m.SeenAt) >= VisitGap {
		return m.SeenUpTo
	}
	return m.DividerAt
}

// TagName normalizes a tag name: trimmed, with each run of whitespace
// replaced by one space.
func TagName(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
	ImportAppLink     ImportFailure = "app_link" // a link to the app itself, such as a shared timeline's
)

// ImportFeed is one feed of an import: a job in the import queue and its result.
type ImportFeed struct {
	ImportID string
	UserID   string // the import's user; set by ClaimNext
	Position int
	URL      string
	Title    string
	Tags     []string // folder names from the file, normalized; applied as tags on subscribe
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
	// Position is where the entry is in the reader's timeline: their own
	// position (timeline_entries) if they have one, else Article.TimelineAt;
	// timelines only.
	Position time.Time
}

// SavedArticle is an article a user saved, with its feed.
type SavedArticle struct {
	Article Article
	Feed    Feed
	SavedAt time.Time
}

// StarredArticle is an article a user starred, with its feed.
type StarredArticle struct {
	Article   Article
	Feed      Feed
	StarredAt time.Time
}

// SharedArticle is an article of a shared timeline, with its feed (the
// feed's own title, never the owner's name for it).
type SharedArticle struct {
	Article   Article
	Feed      Feed
	StarredAt time.Time // when the owner starred it; zero unless the timeline is starred-only
}

// User is an account.
type User struct {
	ID               string
	Name             string
	Email            string
	EmailConfirmedAt time.Time // zero until the email is confirmed
	Theme            string    // ThemeSystem, ThemeLight or ThemeDark; only a Session's User has it
	CreatedAt        time.Time
}

// Themes a user can choose (users.theme). ThemeSystem follows the device's
// light or dark setting.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

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

// WaitlistEntry is someone waiting for an invite (a waitlist row). It
// counts once its address is confirmed.
type WaitlistEntry struct {
	ID          string
	Email       string
	Name        string
	Sites       string    // sites they'd like to follow, as typed, at most 500 characters; "" if not answered
	Source      string    // the ?ref= of the page they joined from; "" if none
	ConfirmedAt time.Time // zero until the address is confirmed
	CreatedAt   time.Time
}

// The admin's waitlist tabs: confirmed entries with no pending invite
// (WaitlistWaiting), confirmed entries with one (WaitlistInvited), and
// entries not yet confirmed (WaitlistUnconfirmed).
const (
	WaitlistWaiting     = "waiting"
	WaitlistInvited     = "invited"
	WaitlistUnconfirmed = "unconfirmed"
)

// WaitlistRow is a waitlist entry as the admin lists it, with its latest
// invite (zero, with an empty ID, if it has none).
type WaitlistRow struct {
	WaitlistEntry
	Invite Invite
}

// WaitlistCounts are how many entries each of the admin's waitlist tabs has.
type WaitlistCounts struct {
	Waiting, Invited, Unconfirmed int
}

// Invite is a single-use signup invite (an invites row). A bound invite has
// an Email, and only that address can sign up with it; an open one has none.
type Invite struct {
	ID         string
	Email      string // "" for an open invite
	WaitlistID string // the waitlist entry it was sent to; "" if none, or once the entry is gone
	// Name is the name its waitlist entry gave, to fill in signup's form; ""
	// without an entry. Read only.
	Name       string
	Note       string // who it's for, for open invites; shown only in the admin
	Wave       string // the label of the wave it went out in; "" if none
	ExpiresAt  time.Time
	RedeemedAt time.Time // zero until someone signs up with it
	UserID     string    // the account made with it; "" until then, or once it's deleted
	RevokedAt  time.Time // zero unless revoked
	CreatedAt  time.Time
}

// Invite statuses, from Invite.Status.
const (
	InvitePending  = "pending"
	InviteRedeemed = "redeemed"
	InviteExpired  = "expired"
	InviteRevoked  = "revoked"
)

// Status says whether i can still be used at now: InvitePending if it can,
// else why not. A redeemed invite stays redeemed once it would have expired.
func (i Invite) Status(now time.Time) string {
	switch {
	case !i.RedeemedAt.IsZero():
		return InviteRedeemed
	case !i.RevokedAt.IsZero():
		return InviteRevoked
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	}
	return InvitePending
}

// InviteWave sums up the invites sent in one wave: how many went out, how
// many were used to sign up, and how many of those accounts had a session
// seen 7 or more days after signing up.
type InviteWave struct {
	Label    string
	SentAt   time.Time // its first invite's
	Sent     int
	SignedUp int
	Active   int
}

// Wave is a wave of invites to send from the admin: one per waiting
// waitlist entry, each bound to it, under one label.
type Wave struct {
	Label     string
	ExpiresAt time.Time
	Invites   []WaveInvite
}

// WaveInvite is one invite of a Wave, with its email: the invite's token and
// the entry's new link token (its leave link) are in the email, so both are
// made, and the email rendered, before the wave is sent.
type WaveInvite struct {
	WaitlistID        string
	Email             string
	InviteTokenHash   []byte
	WaitlistTokenHash []byte
	// Subject, Text and HTML are the invite's email.
	Subject, Text, HTML string
}

// EmailUsage is how many emails were queued against the email provider's
// sending limits: in the last 24 hours (plus any still pending from before),
// and in the current UTC calendar month.
type EmailUsage struct {
	Last24h   int
	ThisMonth int
}

// Session is one logged-in device (a user_sessions row) with its user.
type Session struct {
	ID         string
	User       User
	LastSeenAt time.Time
}

// Admin is an admin account (an admins row), apart from readers' users: a
// reader's password never opens the admin.
type Admin struct {
	ID    string
	Email string
	// TOTPSecret is the sealed TOTP secret (admin.Seal), opened only with
	// the admin service's key.
	TOTPSecret []byte
	// TOTPLastStep is the last 30-second step a code was accepted for; codes
	// for it and earlier steps are refused, so each works once.
	TOTPLastStep int64
	CreatedAt    time.Time
}

// AdminCredentials are what enrolling an admin sets, and resetting it
// replaces: the password's hash, the sealed TOTP secret, the step of the
// code that confirmed it (so that code can't also log in) and the recovery
// codes' hashes.
type AdminCredentials struct {
	HashedPassword     []byte
	SealedTOTPSecret   []byte
	TOTPLastStep       int64
	RecoveryCodeHashes [][]byte
}

// Where an admin action came from (AuditActor.Via).
const (
	AuditViaWeb = "web"
	AuditViaCLI = "cli"
)

// AuditActor is who did an admin action, and from where: what the caller
// of an action knows. The repository method that makes the change records
// what it did (AuditEntry's other fields), since it's the one that knows.
type AuditActor struct {
	// AdminID is "" for a failed password or a command.
	AdminID string
	// AdminEmail is kept when the admin is deleted; for a failed password,
	// it's the submitted email. Recording fills it from AdminID when empty.
	AdminEmail string
	Via        string // AuditViaWeb or AuditViaCLI
	IP         string // the admin's client IP, for web actions
}

// AuditEntry is one admin action or login in the audit log (an admin_audit
// row): who, what, to which record, and the fields it changed.
type AuditEntry struct {
	AuditActor
	Action     string // e.g. "login", "invite.create"
	TargetType string // e.g. "invite"; "" for none
	TargetID   string
	// Before and After are the fields the action changed, as they were and
	// as they became; nil for none. Their values are encoded as JSON.
	Before, After map[string]any
}

// AuditEvent is a recorded AuditEntry.
type AuditEvent struct {
	ID         int64
	OccurredAt time.Time
	AuditEntry
}

// AuditFilter narrows the audit log: to one action, to one type of target
// and to one target, and to the entries older than the one with BeforeID (a
// page's cursor). Zero values don't filter.
type AuditFilter struct {
	Action     string
	TargetType string
	TargetID   string
	BeforeID   int64
}

// ServerError is one of www's recent 500s or panics (a server_errors row),
// kept for the admin. Message is internal detail, never shown to users.
type ServerError struct {
	ID         int64
	OccurredAt time.Time
	Instance   string // os.Hostname() of the process
	Route      string // chi route pattern, never the URL
	Method     string
	Message    string
	Panic      bool
}

// RUMView is one page view's real-user metrics, from the beacon a page sends
// when it's hidden (a rum_views row). It carries no identifiers: no user, IP
// address or URL. A nil metric wasn't reported.
type RUMView struct {
	ReceivedAt    time.Time // zero for now
	Route         string    // chi route pattern of the page's path, never the path
	NavType       string    // navigate, reload, back_forward, prerender or restore (from the back/forward cache)
	Device        string    // phone, tablet or desktop, by viewport width
	Browser       string    // edge, chrome, firefox, safari, samsung or other
	BrowserMajor  int       // 0 when unknown
	Installed     bool      // opened as an installed app
	Connection    string    // effective connection type (4g, 3g, …), where reported
	Protocol      string    // h2, h3, http/1.1
	ReferrerHost  string    // another site's host; "" for none or our own
	TTFBMs        *float64
	FCPMs         *float64
	LCPMs         *float64
	INPMs         *float64
	CLS           *float64
	DCLMs         *float64 // DOMContentLoaded's end
	LoadMs        *float64 // load's end
	ServerMs      *float64 // Server-Timing's app duration
	TransferBytes *int64
	LCPElement    string // tag name of the LCP element
	INPTarget     string // data-* hook or tag name of the slowest interaction's target
}

// AdminOverview is the admin's Overview: aggregates across every reader,
// never one reader's reading ("Admin" in docs/decisions.md).
type AdminOverview struct {
	People  OverviewPeople
	Content OverviewContent
}

// OverviewPeople are the Overview's counts of accounts and the waitlist.
type OverviewPeople struct {
	Accounts, ConfirmedAccounts int
	// Signups7, Signups30 are the accounts made in the last 7 and 30 days.
	Signups7, Signups30 int
	// Active7, Active30 are the readers with a session seen in the last 7
	// and 30 days. A session's last_seen_at is updated at most once a day,
	// so they're about right, not exact.
	Active7, Active30 int
	// Waiting are the confirmed waitlist entries without a pending invite
	// (the waitlist's Waiting tab); InvitesPending the invites neither
	// used, revoked nor expired.
	Waiting, InvitesPending int
}

// OverviewContent are the Overview's counts of what readers follow and
// read. Articles, Reads and TimelineEntries are the planner's estimates, as
// counting those tables exactly reads them whole.
type OverviewContent struct {
	Sites, Feeds, Subscriptions int
	// SubscribedFeeds are the feeds with at least one subscriber.
	SubscribedFeeds int
	// Estimated.
	Articles, Reads, TimelineEntries int
}

// QueueBehindAfter is how overdue a queue's oldest job can be before the
// admin's Overview marks the queue behind.
const QueueBehindAfter = 15 * time.Minute

// QueueStatus is one background queue on the admin's Overview: how many of
// its jobs are due and when the oldest of them was due (zero when none are).
// Name is its runner, as job_metrics names it: lookup, import, refresh,
// icon, enrich or mail.
type QueueStatus struct {
	Name   string
	Jobs   int
	Oldest time.Time
}

// Behind is whether the queue's oldest job is over QueueBehindAfter
// overdue at now.
func (q QueueStatus) Behind(now time.Time) bool {
	return q.Jobs > 0 && now.Sub(q.Oldest) > QueueBehindAfter
}
