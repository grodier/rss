package rss

import (
	"strings"
	"time"
)

// NewsWindow is how far below its arrival (CreatedAt) a news article can be
// placed in timelines (see psql.timelineAt), and how recently a reader must
// have looked to get their own positions for what arrives (see psql's
// saveArticles and Subscribe).
const NewsWindow = 6 * time.Hour

// Subscription is a feed a user follows, with its site.
type Subscription struct {
	Feed            Feed
	Site            Site
	Title           string    // the user's name for the feed; "" uses Feed.Title
	LatestArticleAt time.Time // newest article's published_at, else created_at; zero if the feed has no articles
	Tags            []Tag     // ordered by name, ignoring case; Feeds left zero
}

// Following is what Home says about what a reader follows: how many sites,
// and the followed feeds never fetched yet ("Getting the latest from …").
type Following struct {
	Sites          int      // distinct sites of the reader's subscriptions
	Unfetched      []string // names of up to the asked-for number of never-attempted feeds, in My feeds' order
	UnfetchedTotal int      // how many followed feeds were never attempted
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
