package admin

import (
	"cmp"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/validator"
)

// The queues page shows each background queue's jobs and its failures
// grouped by cause, and retries or deletes one failed job or a whole
// group. Every action is audited, and a group's needs its count typed
// ("Admin" in docs/decisions.md).

const (
	// maxFailureGroups is how many groups a queue lists, the largest.
	maxFailureGroups = 20
	// maxGroupExamples is how many of a group's jobs it lists, the newest.
	maxGroupExamples = 5
	// maxGroupIDs is how many jobs a group action takes: the newest of the
	// group's when the page was rendered.
	maxGroupIDs = 1000
	// maxErrorChars is how much of a group's error the page shows.
	maxErrorChars = 200
	// queueJobsWindow is how far back the jobs-per-hour charts look.
	queueJobsWindow = 24 * time.Hour
)

// queueInfo is one queue on the page: its runner's name (its anchor and
// its actions' path), its heading, what its jobs are called, and whether
// its failures are listed and can be deleted.
type queueInfo struct {
	Name, Label      string
	One, Many        string
	Failures, Delete bool
}

// queueInfos are the page's queues, in its order. Icon checks store no
// error, and emails are the email page's, so they have counts only.
var queueInfos = []queueInfo{
	{Name: rss.QueueLookup, Label: "Lookups", One: "lookup", Many: "lookups", Failures: true, Delete: true},
	// An import's feeds are its results page, so they can't be deleted.
	{Name: rss.QueueImport, Label: "Imports", One: "import feed", Many: "import feeds", Failures: true},
	{Name: rss.QueueEnrich, Label: "Enrichment", One: "enrichment job", Many: "enrichment jobs", Failures: true, Delete: true},
	{Name: "icon", Label: "Icons"},
	{Name: "mail", Label: "Email"},
}

// queueByName returns the queue whose failures the page lists named name.
func queueByName(name string) (queueInfo, bool) {
	for _, q := range queueInfos {
		if q.Name == name && q.Failures {
			return q, true
		}
	}
	return queueInfo{}, false
}

// jobs says n of q's jobs: "1 lookup", "1,000 lookups".
func (q queueInfo) jobs(n int) string {
	if n == 1 {
		return "1 " + q.One
	}
	return count(n) + " " + q.Many
}

// queuesPage is the queues page's data.
type queuesPage struct {
	Nav    navData
	Flash  string
	Queues []queueSection
	Now    time.Time
}

// queueSection is one queue's section: its figures, its jobs per hour,
// and, for a queue whose failures are listed, its largest groups of them,
// with how many failed jobs and groups there are in all.
type queueSection struct {
	queueInfo
	Tiles  []statTile
	Chart  chart.LineChart
	Failed int
	Groups []failureGroup
	// MoreGroups is how many groups aren't listed.
	MoreGroups int
}

// failureGroup is one cause of a queue's failures: its error, normalized
// (Key, posted back with its actions) and cut to show (Error); how many
// jobs failed with it and when the oldest and newest did; its newest jobs;
// and the IDs a group action takes, space-separated, with how many
// (Acts). Confirm and ConfirmError are the typed count and its error when
// the page is shown again after a group action with the wrong one.
type failureGroup struct {
	Queue          string
	Index          int
	Key, Error     string
	Count          int
	Oldest, Newest time.Time
	Examples       []rss.QueueFailure
	IDs            string
	Acts           int
	Confirm        string
	ConfirmError   string

	ids []string
}

// groupError is a group action whose typed count was wrong, to show on
// the page again.
type groupError struct {
	Queue, Key, Typed, Message string
}

// queuesHandler shows the queues page.
func (s *Server) queuesHandler(w http.ResponseWriter, r *http.Request) {
	s.renderQueues(w, r, http.StatusOK, nil)
}

// renderQueues renders the queues page with status, showing gerr on its
// group's form when it's set.
func (s *Server) renderQueues(w http.ResponseWriter, r *http.Request, status int, gerr *groupError) {
	now := s.now()
	counts, err := s.services.Queues.Counts(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	hours, err := s.services.Queues.JobsPerHour(r.Context(), now.Add(-queueJobsWindow), now)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	byName := map[string]rss.AdminQueueCounts{}
	for _, c := range counts {
		byName[c.Name] = c
	}

	page := queuesPage{Nav: s.nav(r, "queues"), Now: now}
	if gerr == nil {
		page.Flash = s.sessionManager.PopString(r.Context(), sessionFlash)
	}
	for _, q := range queueInfos {
		sec := queueSection{
			queueInfo: q,
			Tiles:     queueTiles(q.Name, byName[q.Name], now),
			Chart:     jobsChart(q.Name, hours, now),
		}
		if q.Failures {
			failures, err := s.services.Queues.Failures(r.Context(), q.Name)
			if err != nil {
				s.serverErrorHTML(w, r, err)
				return
			}
			sec.Failed = len(failures)
			sec.Groups = groupFailures(q.Name, failures)
			if n := len(sec.Groups); n > maxFailureGroups {
				sec.MoreGroups = n - maxFailureGroups
				sec.Groups = sec.Groups[:maxFailureGroups]
			}
			if gerr != nil && gerr.Queue == q.Name {
				for i := range sec.Groups {
					if sec.Groups[i].Key == gerr.Key {
						sec.Groups[i].Confirm, sec.Groups[i].ConfirmError = gerr.Typed, gerr.Message
					}
				}
			}
		}
		page.Queues = append(page.Queues, sec)
	}
	if err := s.renderHTML(w, status, "queues.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// queueTiles are a queue's figures at now: its pending jobs, its running
// ones where it has a status, its failed ones, and how long the oldest
// pending job has waited; icon checks have overdue ones in place of
// failures, since they store no error.
func queueTiles(name string, c rss.AdminQueueCounts, now time.Time) []statTile {
	oldest := statTile{Label: "Longest wait", Text: "—"}
	if c.Pending > 0 {
		oldest.Text = wait(now.Sub(c.Oldest))
		oldest.Note = "since " + c.Oldest.UTC().Format("Jan 2, 15:04") + " UTC"
	}
	pending := statTile{Label: "Pending", Value: c.Pending, Note: "due now"}
	switch name {
	case rss.QueueLookup, rss.QueueImport:
		return []statTile{pending, {Label: "Running", Value: c.Running}, {Label: "Failed", Value: c.Failed}, oldest}
	case rss.QueueEnrich:
		return []statTile{pending, {Label: "Failed", Value: c.Failed, Note: "waiting to be tried again"}, oldest}
	case "icon":
		return []statTile{pending, {Label: "Overdue", Value: c.Overdue, Note: "due over " + wait(rss.QueueBehindAfter) + " ago"}, oldest}
	}
	return []statTile{pending, {Label: "Failed", Value: c.Failed, Note: "given up on"}, oldest}
}

// jobsChart draws runner's jobs in each hour of the window before now,
// from hours (JobsPerHour's), 0 for an hour with none.
func jobsChart(runner string, hours []rss.JobsHour, now time.Time) chart.LineChart {
	jobs := map[int64]int{}
	for _, h := range hours {
		if h.Runner == runner {
			jobs[h.End.Unix()] = h.Jobs
		}
	}
	n := int(queueJobsWindow / time.Hour)
	points := make([]chart.Point, n)
	for i := range n {
		end := now.Add(-time.Duration(n-1-i) * time.Hour)
		points[i] = chart.Point{T: end, V: float64(jobs[end.Unix()])}
	}
	return chart.LineChart{Title: "Jobs per hour", Height: 120, Series: []chart.Series{{Name: "Jobs", Points: points}}}
}

// groupFailures groups queue's failed jobs, newest first, by their
// normalized error (an import feed's failure, then its error), largest
// group first, then the one that failed most recently.
func groupFailures(queue string, failures []rss.QueueFailure) []failureGroup {
	var groups []failureGroup
	index := map[string]int{}
	for _, f := range failures {
		key := normalizeError(f.Error)
		if f.Failure != "" {
			key = strings.TrimSuffix(f.Failure+": "+key, ": ")
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, failureGroup{Queue: queue, Key: key, Error: cutChars(key, maxErrorChars), Newest: f.At})
		}
		g := &groups[i]
		g.Count++
		if !f.At.IsZero() && (g.Oldest.IsZero() || f.At.Before(g.Oldest)) {
			g.Oldest = f.At
		}
		if f.At.After(g.Newest) {
			g.Newest = f.At
		}
		if len(g.Examples) < maxGroupExamples {
			g.Examples = append(g.Examples, f)
		}
		if len(g.ids) < maxGroupIDs {
			g.ids = append(g.ids, f.ID)
		}
	}
	slices.SortStableFunc(groups, func(a, b failureGroup) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), b.Newest.Compare(a.Newest))
	})
	for i := range groups {
		g := &groups[i]
		g.Index = i + 1
		g.IDs, g.Acts, g.ids = strings.Join(g.ids, " "), len(g.ids), nil
	}
	return groups
}

var (
	// errorURL matches an address in an error, without the punctuation
	// that follows it.
	errorURL = regexp.MustCompile(`https?://[^\s"'<>]*[^\s"'<>.,:;)]`)
	// errorHost matches a host name the resolver couldn't look up, with
	// the resolver's address when the error names it.
	errorHost = regexp.MustCompile(`\blookup [^\s:]+(?: on \S+)?: `)
	// errorIPv6 matches an IPv6 address in brackets, before a port.
	errorIPv6 = regexp.MustCompile(`\[[0-9a-fA-F:.]*:[0-9a-fA-F:.]*\]`)
	// errorNumber matches a number on its own (a status, a port, an IPv4
	// address's parts), not the digits in a word (x509).
	errorNumber = regexp.MustCompile(`\b[0-9]+\b`)
)

// normalizeError makes the errors of one cause the same text, so they
// group together: it replaces addresses with <url>, unknown hosts (and the
// resolver asked) with <host>, IPv6 addresses with <ip> and numbers with
// <n>.
func normalizeError(msg string) string {
	msg = errorURL.ReplaceAllString(msg, "<url>")
	msg = errorHost.ReplaceAllString(msg, "lookup <host>: ")
	msg = errorIPv6.ReplaceAllString(msg, "<ip>")
	msg = errorNumber.ReplaceAllString(msg, "<n>")
	return strings.TrimSpace(msg)
}

// cutChars cuts s to at most n characters, ending it with … when it cut.
func cutChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// queueActionForm is a retry or delete: of one job (ID), or of a group's
// jobs (IDs, space-separated) when its error (Group) was listed, with
// their count typed to confirm.
type queueActionForm struct {
	ID                  string `form:"id"`
	IDs                 string `form:"ids"`
	Group               string `form:"group"`
	Confirm             string `form:"confirm"`
	validator.Validator `form:"-"`
}

// queueRetryHandler retries the posted failed jobs of the queue {queue}.
func (s *Server) queueRetryHandler(w http.ResponseWriter, r *http.Request) {
	s.queueAction(w, r, false)
}

// queueDeleteHandler deletes the posted failed jobs of the queue {queue},
// one whose jobs can be deleted.
func (s *Server) queueDeleteHandler(w http.ResponseWriter, r *http.Request) {
	s.queueAction(w, r, true)
}

// queueAction retries or deletes the posted jobs of the queue {queue}:
// one job, or the IDs a group listed when the page was rendered, at most
// maxGroupIDs, once their count is typed. Only the jobs still failed
// change; it goes back to the queue's section saying how many did.
func (s *Server) queueAction(w http.ResponseWriter, r *http.Request, del bool) {
	q, ok := queueByName(chi.URLParam(r, "queue"))
	if !ok || (del && !q.Delete) {
		s.notFoundResponse(w, r)
		return
	}
	var form queueActionForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}

	ids := []string{form.ID}
	group := form.ID == ""
	if group {
		ids = strings.Fields(form.IDs)
	}
	switch {
	case len(ids) == 0 || slices.ContainsFunc(ids, func(id string) bool { return !validJobID(q.Name, id) }):
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", "That form didn’t name jobs of this queue.")
		return
	case len(ids) > maxGroupIDs:
		s.errorHTML(w, r, http.StatusBadRequest, "Bad request", fmt.Sprintf("A group action takes at most %s jobs.", count(maxGroupIDs)))
		return
	}
	if group {
		typed := strings.ReplaceAll(strings.TrimSpace(form.Confirm), ",", "")
		if typed != strconv.Itoa(len(ids)) {
			s.renderQueues(w, r, http.StatusUnprocessableEntity, &groupError{
				Queue: q.Name, Key: form.Group, Typed: form.Confirm,
				Message: fmt.Sprintf("Type %s to confirm.", count(len(ids))),
			})
			return
		}
	}

	action, verb := s.services.Queues.Retry, "Retried"
	if del {
		action, verb = s.services.Queues.Delete, "Deleted"
	}
	n, err := action(r.Context(), q.Name, ids, form.Group, s.actor(r))
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	var msg string
	switch {
	case n == 0 && del:
		msg = "Nothing to delete: those jobs aren’t failed any more."
	case n == 0:
		msg = "Nothing to retry: those jobs aren’t failed any more."
	case del:
		msg = verb + " " + q.jobs(n) + "."
	default:
		msg = verb + " " + q.jobs(n) + ": the runner takes them in the next minute or so."
	}
	s.backTo(w, r, "/queues#"+q.Name, msg)
}

// importFeedID matches an import feed's ID, its import's ID and its
// position.
var importFeedID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\.[0-9]{1,9}$`)

// validJobID reports whether id is a job ID of queue: an import feed's
// import ID and position, or a UUID.
func validJobID(queue, id string) bool {
	if queue == rss.QueueImport {
		return importFeedID.MatchString(id)
	}
	return validator.Matches(id, validator.UUIDRX)
}
