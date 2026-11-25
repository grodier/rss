package rss

import "time"

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
	HomePage    bool // URL is its site's root (discovery.IsHomePage)
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
