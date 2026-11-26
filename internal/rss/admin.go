package rss

import "time"

// EmailUsage is how many emails were queued against the email provider's
// sending limits: in the last 24 hours (plus any still pending from before),
// and in the current UTC calendar month.
type EmailUsage struct {
	Last24h   int
	ThisMonth int
}

// The outbox's email statuses (emails.status).
const (
	EmailPending = "pending"
	EmailSent    = "sent"
	EmailFailed  = "failed"
)

// AdminEmail is an email in the outbox as the admin's email page shows it:
// never its body, which can carry a one-time link.
type AdminEmail struct {
	ID       string
	To       string
	Subject  string
	Status   string // EmailPending, EmailSent or EmailFailed
	Attempts int
	// NextAttemptAt is when the mail runner may next claim a pending email
	// (also the lease of one it's sending).
	NextAttemptAt time.Time
	// LastError is the last failed attempt's error, internal detail shown
	// only in the admin.
	LastError string
	CreatedAt time.Time
	SentAt    time.Time // zero until sent
	// UserID is the account whose address is To, "" when none is.
	UserID string
}

// AdminEmailQuery is a page of the admin's email page: the emails with
// Status ("" for all), newest first, after the email After ("" for the
// first page), Limit of them.
type AdminEmailQuery struct {
	Status string
	After  string
	Limit  int
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

// The queues whose failed jobs the admin's queues page groups, retries and
// deletes, named after their runners, as job_metrics names them.
const (
	QueueLookup = "lookup"
	QueueImport = "import"
	QueueEnrich = "enrich"
)

// AdminQueueCounts are one background queue's jobs on the admin's queues
// page. Name is its runner (lookup, import, enrich, icon or mail); Pending
// are the jobs due now; Running those claimed, for the queues with a
// status (lookups and imports); Failed the lookups and import feeds that
// failed, the enrichment jobs that failed and wait to be tried again, and
// the emails given up on; Overdue the icon checks due over
// QueueBehindAfter ago; and Oldest when the oldest pending job was due,
// zero when none is.
type AdminQueueCounts struct {
	Name                              string
	Pending, Running, Failed, Overdue int
	Oldest                            time.Time
}

// JobsHour is how many jobs a runner finished in the hour ending at End.
type JobsHour struct {
	Runner string
	End    time.Time
	Jobs   int
}

// QueueFailure is one failed job on the admin's queues page. ID is the
// lookup's ID, the import feed's import ID and position
// ("{import_id}.{position}") or the enrichment job's article ID. Error is
// its stored error and Failure an import feed's kind of failure (unreachable,
// not_feed, …). At is when it failed (lookups, import feeds) or was queued
// (enrichment jobs, whose failures aren't timed). URL is the address the
// job fetches; SiteID a lookup's site, FeedID an import feed's subscribed
// feed or an enrichment job's article's feed, "" for none; and ImportedAt an
// import feed's import's date.
type QueueFailure struct {
	ID, Error, Failure string
	At                 time.Time
	URL                string
	SiteID, FeedID     string
	ImportedAt         time.Time
}

// JobOutcomes are one runner's finished jobs in a window, by outcome, as
// job_metrics counts them (see "Background work" in docs/architecture.md):
// Runner is lookup, import, refresh, icon, enrich or mail, and Total the
// sum of its Outcomes.
type JobOutcomes struct {
	Runner   string
	Total    int
	Outcomes []JobOutcome
}

// JobOutcome is how many of a runner's jobs ended with one outcome.
type JobOutcome struct {
	Name string
	Jobs int
}

// Jobs returns how many jobs it finished with outcome name, 0 if none.
func (o JobOutcomes) Jobs(name string) int {
	for _, out := range o.Outcomes {
		if out.Name == name {
			return out.Jobs
		}
	}
	return 0
}

// The admin's feed list's filters (AdminFeedQuery.Filter).
const (
	AdminFeedsAll           = "all"
	AdminFeedsFailing       = "failing"        // AdminFailingAfter or more failures in a row
	AdminFeedsGone          = "gone"           // answered 410 Gone
	AdminFeedsNeverFetched  = "never-fetched"  // no successful fetch yet
	AdminFeedsStale         = "stale"          // subscribed, not gone, no successful fetch for AdminStaleAfter
	AdminFeedsNoSubscribers = "no-subscribers" // no one subscribes
)

// The admin's feed list's orders (AdminFeedQuery.Sort): the most
// subscribers, posts per week or failures first, or the oldest last
// success first, a feed never fetched before all.
const (
	AdminFeedsBySubscribers = "subscribers"
	AdminFeedsByPosts       = "posts"
	AdminFeedsByFailures    = "failures"
	AdminFeedsByLastSuccess = "last-success"
)

// AdminFailingAfter is how many failed fetches in a row put a feed under
// the admin's Failing filter, like the reader's warning.
const AdminFailingAfter = 3

// AdminStaleAfter is how long a subscribed feed can go without a
// successful fetch before the admin's Stale filter lists it.
const AdminStaleAfter = 24 * time.Hour

// AdminFeedQuery is a page of the admin's feed list: a filter, a search of
// titles, URLs and site hosts (Q, "" for none), an order, and the feed
// whose row the page starts after (After, "" for the first page).
type AdminFeedQuery struct {
	Filter string
	Q      string
	Sort   string
	After  string
	Limit  int
}

// AdminFeedRow is a feed as the admin lists it: the feed, its site's host,
// how many readers subscribe to it (a count, never who) and its section in
// Discover's catalog (ID "" when it isn't in it).
type AdminFeedRow struct {
	Feed
	SiteHost    string
	Subscribers int
	Section     Section
}

// AdminFeed is one feed's admin page: its row, its site, its articles'
// count and the newest one's arrival, and its articles' enrichment jobs'
// count and the oldest one's creation (zero times with none).
type AdminFeed struct {
	AdminFeedRow
	Site            Site
	Articles        int
	NewestArticleAt time.Time
	EnrichJobs      int
	OldestEnrichJob time.Time
}

// The admin's site list's filters (AdminSiteQuery.Filter).
const (
	AdminSitesAll           = "all"
	AdminSitesNoIcon        = "no-icon"
	AdminSitesLookupFailed  = "lookup-failed"
	AdminSitesNoSubscribers = "no-subscribers"
)

// The admin's site list's orders (AdminSiteQuery.Sort).
const (
	AdminSitesBySubscribers = "subscribers" // most first
	AdminSitesByFeeds       = "feeds"       // most first
	AdminSitesByHost        = "host"        // A to Z
	AdminSitesByFound       = "found"       // newest first
)

// AdminSiteQuery is a page of the admin's site list: a filter, a search of
// hosts and titles (Q, "" for none), an order, and the site whose row the
// page starts after (After, "" for the first page).
type AdminSiteQuery struct {
	Filter string
	Q      string
	Sort   string
	After  string
	Limit  int
}

// AdminSiteRow is a site as the admin lists it: the site, how many feeds it
// has, how many readers subscribe to any of them (each reader once; a
// count, never who), its icon (SiteID "" when it has none; Data is left
// empty) and its lookup (ID "" when there's none).
type AdminSiteRow struct {
	Site
	Feeds       int
	Subscribers int
	Icon        SiteIcon
	Lookup      Lookup
}

// AdminSite is one site's admin page: the site, its icon's bookkeeping, its
// lookup (ID "" when it has none) and its feeds, by title.
type AdminSite struct {
	Site   Site
	Icon   SiteDebug
	Lookup Lookup
	Feeds  []AdminFeedRow
}

// AdminUserQuery selects a page of the admin's users list, newest first:
// those whose email or name starts with Q (any case; "" for every user),
// starting after the row After (zero for the first page).
type AdminUserQuery struct {
	Q     string
	After AdminUserCursor
	Limit int
}

// AdminUserCursor is where a page of the users list starts: after the user
// who joined at CreatedAt with ID, ordered by both. It carries the time,
// so a page still follows the last if that user is deleted in between.
type AdminUserCursor struct {
	CreatedAt time.Time
	ID        string
}

// AdminUserRow is a reader as the admin lists them: account facts only,
// never their reading ("Admin" in docs/decisions.md).
type AdminUserRow struct {
	ID               string
	Name             string
	Email            string
	EmailConfirmedAt time.Time // zero until confirmed
	CreatedAt        time.Time
	LastSeenAt       time.Time // the newest of their sessions' last_seen_at; zero with none
}

// AdminUser is one reader's admin page: their account, the invite they
// signed up with (ID "" for none), the features they have, their counts,
// the emails to them that the outbox still holds, and their outstanding
// email links.
// Counts only: never their reads, saves, stars, tags, timelines or feeds.
type AdminUser struct {
	AdminUserRow
	Invite Invite
	// Newsletters says whether they have FeatureNewsletters.
	Newsletters bool
	Counts      AdminUserCounts
	Emails      []AdminUserEmail
	Tokens      []AdminUserToken
}

// AdminUserCounts are a reader's counts on their admin page. Devices are
// their sessions still within the reader's idle timeout; ImportedFeeds and
// ImportFailures add up their imports' feeds subscribed to (or already
// subscribed) and failed.
type AdminUserCounts struct {
	Devices         int
	Subscriptions   int
	Tags            int
	Timelines       int
	SharedTimelines int
	Saved           int
	Starred         int
	Imports         int
	ImportedFeeds   int
	ImportFailures  int
}

// AdminUserEmail is an email in the outbox as a reader's admin page shows
// it: never its body, which can carry a one-time link.
type AdminUserEmail struct {
	ID        string
	To        string // the account's address, or a change of address's new one
	Subject   string
	Status    string // pending, sent or failed
	Attempts  int
	CreatedAt time.Time
	SentAt    time.Time // zero until sent
}

// AdminUserToken is an outstanding email link as a reader's admin page
// shows it: never its hash.
type AdminUserToken struct {
	Purpose   string // TokenConfirmEmail, TokenResetPassword or TokenChangeEmail
	Email     string // the address it was sent to
	CreatedAt time.Time
	ExpiresAt time.Time
}

// The admin's seed list's filters (AdminSeedQuery.Filter).
const (
	AdminSeedAll           = "all"
	AdminSeedNotFound      = "not-found"       // its lookup failed or found no feeds
	AdminSeedWaiting       = "waiting"         // no lookup yet, or pending or running
	AdminSeedNotInDiscover = "not-in-discover" // found, and none of its site's feeds is in the catalog
)

// AdminSeedQuery selects a page of the admin's seed list, newest first.
type AdminSeedQuery struct {
	Filter string
	After  string // the site key of the previous page's last entry; "" for the first page
	Limit  int
}

// A seed list entry's state on the admin's seed list page
// (AdminSeedEntry.State).
const (
	SeedWaiting = "waiting"  // never looked up, or its lookup is queued
	SeedRunning = "running"  // its lookup is running
	SeedFailed  = "failed"   // its lookup failed
	SeedNoFeeds = "no-feeds" // its lookup finished and found no feeds
	SeedFound   = "found"    // its lookup found a site with feeds
)

// AdminSeedEntry is a seed list entry as the admin lists it: its address,
// site key, section hint ("" for none), source ("" for none) and when it
// was added, its site key's lookup (ID "" when it has none), and the site
// that lookup found (SiteHost "" when none) with how many feeds the site
// has and how many of them are in Discover's catalog.
type AdminSeedEntry struct {
	URL, SiteKey string
	SectionName  string
	Source       string
	AddedAt      time.Time
	Lookup       Lookup
	SiteHost     string
	Feeds        int
	InDiscover   int
}

// State is where e stands: its lookup's status, with a finished one split
// by whether it found a site.
func (e AdminSeedEntry) State() string {
	switch e.Lookup.Status {
	case LookupRunning:
		return SeedRunning
	case LookupFailed:
		return SeedFailed
	case LookupDone:
		if e.Lookup.SiteID == "" {
			return SeedNoFeeds
		}
		return SeedFound
	}
	return SeedWaiting
}

// AdminCatalogSection is a Discover section with its catalog feeds, as the
// admin lists them.
type AdminCatalogSection struct {
	Section
	Feeds []AdminCatalogFeed
}

// AdminCatalogFeed is a feed in Discover's catalog as the admin lists it:
// its site, posting rate, latest article's date (zero with none),
// subscribers (a count, never who) and when it answered 410 Gone (zero if
// it hasn't), since Discover hides a gone feed still in the catalog.
type AdminCatalogFeed struct {
	ID, Title, URL   string
	SiteID, SiteHost string
	PostsPerWeek     float64
	LatestPost       time.Time
	Subscribers      int
	GoneAt           time.Time
}

// The picking page's filters (AdminPickQuery.Filter).
const (
	AdminPickSeed    = "seed"    // sites a seed entry's lookup found
	AdminPickAll     = "all"     // every site with a candidate feed
	AdminPickSkipped = "skipped" // sites with skipped feeds
)

// AdminPickQuery selects a page of sites for the picking page.
type AdminPickQuery struct {
	Filter string
	After  string // the previous page's last site ID; "" for the first page
	Limit  int    // sites
}

// AdminPickSite is a site on the picking page: the site, its seed entry's
// source, the section to pre-select (ID "" for none), whether it came from
// the seed entry, its feeds to decide (or, under Skipped, its skipped
// feeds), and how many more there are than the page shows.
type AdminPickSite struct {
	Site
	Source   string
	Hint     Section
	HintSeed bool
	Feeds    []AdminPickFeed
	More     int
}

// AdminPickFeed is a feed on the picking page.
type AdminPickFeed struct {
	ID, Title, URL string
	PostsPerWeek   float64
	LatestPost     time.Time // zero when it has no articles
	Latest         []string  // its 3 newest titles
	Comments       bool      // its title or address mentions comments
	Categories     []string  // see Feed.Categories
}

// DiscoverPick is a feed put in Discover's catalog from the picking page.
type DiscoverPick struct{ FeedID, SectionID string }
