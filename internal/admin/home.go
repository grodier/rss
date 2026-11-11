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
	// Version is the admin's build; RenderedAt when this page was made.
	Version    string
	RenderedAt time.Time
	// Option is the design checkpoint's option, a (default), b or c; the
	// picked one replaces the switch before the PR is ready.
	Option string
	// Headline is option c's strip of the key figures.
	Headline []statTile
}

// homeHandler shows the Overview.
func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	o, err := s.services.Overview.Overview(r.Context(), now)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := homePage{
		Nav:        s.nav(r, "overview"),
		People:     peopleTiles(o.People),
		Content:    contentTiles(o.Content),
		Version:    s.config.Version,
		RenderedAt: now,
		Option:     "a",
	}
	switch o := r.URL.Query().Get("option"); o {
	case "b", "c":
		data.Option = o
	}
	if data.Option == "c" {
		data.Headline = []statTile{data.People[0], data.People[2], data.People[3], data.Content[3]}
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
