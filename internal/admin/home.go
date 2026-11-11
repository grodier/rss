package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// The Overview answers "is everything all right, and how is it growing?":
// a section per group of figures, each linking to the page where one digs
// in, once that page exists. Cross-user data only as aggregates ("Admin" in
// docs/decisions.md).

// statTile is one figure in a stat row: a label, a number, an optional
// short second line, and the page it links to ("" for none yet).
type statTile struct {
	Label string
	Value int
	Note  string
	Href  string
}

// homePage is the Overview's data.
type homePage struct {
	Nav     navData
	People  []statTile
	Content []statTile
	Queues  []queueRow
	// QueuesBehind are the Queues that are behind, for option c's summary.
	QueuesBehind []queueRow
	// Option is the design checkpoint's option for the Queues section, a
	// (default), b or c; the picked one replaces the switch before the PR
	// is ready.
	Option string
	// Version is the admin's build; RenderedAt when this page was made.
	Version    string
	RenderedAt time.Time
}

// homeHandler shows the Overview.
func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	o, err := s.services.Overview.Overview(r.Context(), now)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	queues, err := s.services.Overview.Queues(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := homePage{
		Nav:        s.nav(r, "overview"),
		People:     peopleTiles(o.People),
		Content:    contentTiles(o.Content),
		Queues:     queueRows(queues, now),
		Option:     "a",
		Version:    s.config.Version,
		RenderedAt: now,
	}
	switch opt := r.URL.Query().Get("option"); opt {
	case "b", "c":
		data.Option = opt
	}
	for _, q := range data.Queues {
		if q.Behind {
			data.QueuesBehind = append(data.QueuesBehind, q)
		}
	}
	if err := s.renderHTML(w, http.StatusOK, "home.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// peopleTiles are the People section's figures. Accounts link to the users
// page once #579 adds it.
func peopleTiles(p rss.OverviewPeople) []statTile {
	return []statTile{
		{Label: "Confirmed accounts", Value: p.ConfirmedAccounts, Note: "of " + count(p.Accounts)},
		{Label: "Signups in 7 days", Value: p.Signups7, Note: count(p.Signups30) + " in 30 days"},
		// Sessions' last_seen_at moves at most once a day.
		{Label: "Active in 7 days, about", Value: p.Active7, Note: count(p.Active30) + " in 30 days"},
		{Label: "Waiting", Value: p.Waiting, Note: "on the waitlist", Href: "/waitlist"},
		{Label: "Invites pending", Value: p.InvitesPending, Note: "not used yet", Href: "/invites"},
	}
}

// contentTiles are the Content section's figures. Sites and feeds link to
// the feeds page once #581 adds it.
func contentTiles(c rss.OverviewContent) []statTile {
	return []statTile{
		{Label: "Sites", Value: c.Sites},
		{Label: "Feeds", Value: c.Feeds, Note: count(c.SubscribedFeeds) + " with subscribers"},
		{Label: "Subscriptions", Value: c.Subscriptions},
		{Label: "Articles, about", Value: c.Articles},
		{Label: "Reads, about", Value: c.Reads},
		{Label: "Timeline entries, about", Value: c.TimelineEntries},
	}
}

// queueRow is one queue in the Queues section: its name, how many jobs are
// due, how long the oldest has waited ("—" for none), and whether it's
// behind (rss.QueueBehindAfter). Each links to the Queues page once #582
// adds it.
type queueRow struct {
	Label  string
	Jobs   int
	Wait   string
	Behind bool
}

// queueLabels name the runners' queues on the Overview.
var queueLabels = map[string]string{
	"lookup":  "Lookups",
	"import":  "Imports",
	"refresh": "Refresh",
	"icon":    "Icons",
	"enrich":  "Enrichment",
	"mail":    "Email",
}

// queueRows are the Queues section's rows at now.
func queueRows(queues []rss.QueueStatus, now time.Time) []queueRow {
	rows := make([]queueRow, len(queues))
	for i, q := range queues {
		label, ok := queueLabels[q.Name]
		if !ok {
			label = q.Name
		}
		rows[i] = queueRow{Label: label, Jobs: q.Jobs, Wait: "—", Behind: q.Behind(now)}
		if q.Jobs > 0 {
			rows[i].Wait = wait(now.Sub(q.Oldest))
		}
	}
	return rows
}

// wait says how long a job has waited, roughly: "under 1 min", "3 min",
// "5 h", "2 d". A due time a little after now (the database's clock ahead
// of the admin's) waits under a minute.
func wait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under 1 min"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " d"
}

// count formats n with commas between thousands: 1,284.
func count(n int) string {
	if n < 0 {
		return "-" + count(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
