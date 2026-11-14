package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
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
	Text  string // shown in place of Value when set: "99.93%"
	Note  string
	Href  string
}

// homePage is the Overview's data.
type homePage struct {
	Nav     navData
	Service []statTile
	People  []statTile
	Content []statTile
	Queues  []queueRow
	// Jobs are the runners' outcomes in the last 24 hours; JobsRecorded is
	// false until www has written any job metrics.
	Jobs         []jobRow
	JobsRecorded bool
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
	jobs, recorded, err := s.services.Overview.Jobs(r.Context(), now.Add(-jobsWindow))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	service, err := s.serviceTiles(r.Context(), now)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	data := homePage{
		Nav:          s.nav(r, "overview"),
		Service:      service,
		People:       peopleTiles(o.People),
		Content:      contentTiles(o.Content),
		Queues:       queueRows(queues, now),
		Jobs:         jobRows(jobs),
		JobsRecorded: recorded,
		Version:      s.config.Version,
		RenderedAt:   now,
	}
	if err := s.renderHTML(w, http.StatusOK, "home.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// serviceTiles are the Service section's figures, from the pinger's checks:
// www's uptime over 24 hours and its version, linking to the service page.
func (s *Server) serviceTiles(ctx context.Context, now time.Time) ([]statTile, error) {
	uptime := statTile{Label: "Uptime, 24 hours", Text: "Not configured", Href: "/service"}
	ver := statTile{Label: "www’s version", Text: "Not configured", Href: "/service"}
	if s.config.AppInternalURL == "" {
		return []statTile{uptime, ver}, nil
	}
	u, err := s.services.Availability.Uptime(ctx, now)
	if err != nil {
		return nil, err
	}
	uptime.Text = uptimePercent(u.Day)
	if u.Day.Checks > 0 {
		uptime.Note = fmt.Sprintf("%s of %s checks", count(u.Day.OK), count(u.Day.Checks))
	}
	latest, err := s.services.Availability.LatestVersion(ctx)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		ver.Text = "None reported"
	case err != nil:
		return nil, err
	default:
		ver.Text = version(latest).Text
		ver.Note = "as of " + latest.CheckedAt.UTC().Format("15:04") + " UTC"
	}
	return []statTile{uptime, ver}, nil
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

// jobsWindow is how far back the Jobs section looks.
const jobsWindow = 24 * time.Hour

// jobOutcome is a runner's outcome as the Jobs section reads it: its
// job_metrics name, its label in the breakdown, and its bucket.
type jobOutcome struct {
	name, label string
	bucket      string // done or failed; any outcome not listed is other
}

// jobRunners are the runners the Jobs section shows, in order. Every
// runner's outcomes fall into the same buckets, so the bars compare down
// the page: done (a 304 is a feed checked with nothing new, a lookup with
// no feeds an answer), failed, and other (retries, gone feeds).
var jobRunners = []struct {
	name, label string
	outcomes    []jobOutcome
}{
	{"refresh", "Refresh", []jobOutcome{{"saved", "saved", "done"}, {"not_modified", "304", "done"}, {"failed", "failed", "failed"}, {"gone", "gone", ""}}},
	{"lookup", "Lookups", []jobOutcome{{"found", "found", "done"}, {"none", "no feeds", "done"}, {"failed", "failed", "failed"}}},
	{"enrich", "Enrichment", []jobOutcome{{"saved", "saved", "done"}, {"gave_up", "gave up", "failed"}}},
	{"mail", "Email", []jobOutcome{{"sent", "sent", "done"}, {"gave_up", "gave up", "failed"}}},
}

// jobRow is one runner in the Jobs section: its jobs in the window, its
// failures, its bar of the buckets, and each outcome's share, for the
// bar's tooltip and label.
type jobRow struct {
	Label     string
	Total     int
	Failed    int
	FailedPct string
	Bar       []barSegment
	Breakdown string
}

// barSegment is one bucket's part of a runner's bar, in percent of its
// width; Kind is done, other or failed.
type barSegment struct {
	Kind         string
	Start, Width float64
}

// minFailureWidth is the narrowest failures are drawn on a bar, in
// percent, so a few among many jobs still show.
const minFailureWidth = 3.0

// jobRows are the Jobs section's rows, from the runners' outcomes.
func jobRows(outcomes []rss.JobOutcomes) []jobRow {
	byRunner := map[string]rss.JobOutcomes{}
	for _, o := range outcomes {
		byRunner[o.Runner] = o
	}
	rows := make([]jobRow, len(jobRunners))
	for i, jr := range jobRunners {
		o := byRunner[jr.name]
		row := jobRow{Label: jr.label, Total: o.Total}
		var done int
		var parts []string
		listed := map[string]bool{}
		for _, out := range jr.outcomes {
			listed[out.name] = true
			n := o.Jobs(out.name)
			switch out.bucket {
			case "done":
				done += n
			case "failed":
				row.Failed += n
			}
			if n > 0 {
				parts = append(parts, out.label+" "+share(n, o.Total))
			}
		}
		// Then the outcomes no bucket names (retries), as job_metrics
		// names them.
		for _, out := range o.Outcomes {
			if !listed[out.Name] {
				parts = append(parts, out.Name+" "+share(out.Jobs, o.Total))
			}
		}
		row.Breakdown = strings.Join(parts, ", ")
		row.FailedPct = share(row.Failed, o.Total)
		if o.Total > 0 {
			pct := func(n int) float64 { return float64(n) * 100 / float64(o.Total) }
			row.Bar = bucketBar(pct(done), pct(o.Total-done-row.Failed), pct(row.Failed))
		}
		rows[i] = row
	}
	return rows
}

// bucketBar lays out a runner's bar from its buckets' shares, in percent:
// failures first, at least minFailureWidth wide, then done, then other,
// shrunk to fill what the failures leave. Widths are rounded to a tenth.
func bucketBar(done, other, failed float64) []barSegment {
	var bar []barSegment
	start := 0.0
	add := func(kind string, w float64) {
		w = math.Round(w*10) / 10
		if w <= 0 {
			return
		}
		bar = append(bar, barSegment{Kind: kind, Start: start, Width: w})
		start = math.Round((start+w)*10) / 10
	}
	scale := 1.0
	if failed > 0 && failed < minFailureWidth {
		if rest := done + other; rest > 0 {
			scale = (rest - (minFailureWidth - failed)) / rest
		}
		failed = minFailureWidth
	}
	add("failed", failed)
	add("done", done*scale)
	add("other", other*scale)
	return bar
}

// share is part of whole as a rounded percentage, "<1%" when part is a few
// of many, and "" when whole is 0.
func share(part, whole int) string {
	if whole == 0 {
		return ""
	}
	p := int(math.Round(float64(part) * 100 / float64(whole)))
	if p == 0 && part > 0 {
		return "<1%"
	}
	return strconv.Itoa(p) + "%"
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
