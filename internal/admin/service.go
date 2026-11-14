package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/admin/render"
	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// The service page shows the service as a whole: www's availability from
// the admin's pinger (#588), its instances' CPU and memory and its deploys
// from Render's API, its Go runtime (#563) and its version (see
// "Observability" in docs/decisions.md).

// servicePeriod is one of the page's periods, for the incidents and the
// runtime charts; the uptime figures always show all four.
type servicePeriod struct {
	Key   string
	Label string // "24 hours", for "No incidents in the last 24 hours."
	Span  time.Duration
	Step  time.Duration // between the runtime charts' points
}

// servicePeriods are the page's periods, in the filter's order.
var servicePeriods = []servicePeriod{
	{Key: "24h", Label: "24 hours", Span: 24 * time.Hour, Step: 10 * time.Minute},
	{Key: "7d", Label: "7 days", Span: 7 * 24 * time.Hour, Step: time.Hour},
	{Key: "30d", Label: "30 days", Span: 30 * 24 * time.Hour, Step: 4 * time.Hour},
	{Key: "90d", Label: "90 days", Span: 90 * 24 * time.Hour, Step: 4 * time.Hour},
}

const defaultServicePeriod = "24h"

// maxIncidents is how many incidents the page lists, newest first.
const maxIncidents = 50

const (
	// renderTimeout bounds the page's requests to Render, well inside the
	// server's 10-second WriteTimeout.
	renderTimeout = 5 * time.Second
	// deploysFetched is how many deploys the page reads for the charts'
	// markers (Render's page size), and deploysShown how many it lists.
	deploysFetched = 100
	deploysShown   = 10
)

// servicePage is the service page's data.
type servicePage struct {
	Nav     navData
	Periods []filterLink
	Period  servicePeriod

	// Pinging is whether the pinger runs (-app-internal-url is set): the
	// availability and version sections have nothing to show without it.
	Pinging bool
	// HasChecks is whether the pinger has stored any check in 90 days.
	HasChecks     bool
	Uptime        []statTile
	Incidents     []incidentRow
	MoreIncidents bool // the list stopped at maxIncidents

	// Charts are the runtime charts, nil with no samples in the period.
	// RuntimeKept, set when the period reaches past what's kept, says so in
	// their place.
	Charts      []chart.LineChart
	RuntimeKept string

	Version *versionInfo // nil when no check has reported one

	// Rendering is whether the admin has Render's settings; RenderFailed
	// whether the instances' figures couldn't be read, DeploysFailed the
	// deploys'. Instances are the CPU, memory and instance count charts,
	// nil when Render has no figures for the period.
	Rendering     bool
	RenderFailed  bool
	Instances     []chart.LineChart
	Deploys       []deployRow
	DeploysFailed bool
}

// deployRow is one of www's deploys as the table shows it.
type deployRow struct {
	Created  time.Time
	Finished time.Time // zero while it runs
	Status   string
	Failed   bool
	Version  versionInfo // Text "" when unknown
}

// incidentRow is an incident as the table shows it.
type incidentRow struct {
	Started  time.Time
	Ended    time.Time // zero while it lasts
	Duration string
	Checks   int
	Statuses string
}

// versionInfo is www's version from the newest check that reported one.
type versionInfo struct {
	Text string // the version, or a commit's first 7 characters
	Href string // the commit on GitHub, for a commit SHA
	At   time.Time
}

// serviceHandler shows the service page.
func (s *Server) serviceHandler(w http.ResponseWriter, r *http.Request) {
	period := servicePeriods[0]
	for _, p := range servicePeriods {
		if r.URL.Query().Get("period") == p.Key {
			period = p
		}
	}
	// To whole seconds, like the web performance page, so the steps survive
	// the round trip through Postgres.
	now := s.now().UTC().Truncate(time.Second)
	page := servicePage{Nav: s.nav(r, "service"), Period: period, Pinging: s.config.AppInternalURL != ""}
	for _, p := range servicePeriods {
		href := "/service"
		if p.Key != defaultServicePeriod {
			href += "?period=" + p.Key
		}
		page.Periods = append(page.Periods, filterLink{Label: p.Key, Href: href, Current: p.Key == period.Key})
	}

	if page.Pinging {
		ctx := r.Context()
		uptime, err := s.services.Availability.Uptime(ctx, now)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		// One more than shown, to know whether any were left out.
		incidents, err := s.services.Availability.Incidents(ctx, now.Add(-period.Span), maxIncidents+1)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		latest, err := s.services.Availability.LatestVersion(ctx)
		if err != nil && !errors.Is(err, rss.ErrNoRecord) {
			s.serverErrorHTML(w, r, err)
			return
		}
		page.HasChecks = uptime.Quarter.Checks > 0
		page.Uptime = uptimeTiles(uptime)
		page.MoreIncidents = len(incidents) > maxIncidents
		page.Incidents = incidentRows(incidents[:min(len(incidents), maxIncidents)], now)
		if err == nil {
			page.Version = version(latest)
		}
	}

	var markers []chart.Marker
	if s.services.Render != nil {
		page.Rendering = true
		var err error
		markers, err = s.renderSections(r, &page, period, now)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	// Runtime metrics are kept for metrics.RuntimeRetention: a longer
	// period says so instead of charts that would show less than it says,
	// as the web performance page does past its raw views.
	if period.Span > metrics.RuntimeRetention {
		page.RuntimeKept = runtimeKept()
	} else {
		from := now.Add(-period.Span).Truncate(period.Step)
		rows, err := s.services.Runtime.ListRuntime(r.Context(), from, now)
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		if len(rows) > 0 {
			page.Charts = runtimeCharts(rows, from, now, period.Step)
		}
	}
	for i := range page.Charts {
		page.Charts[i].Markers = markers
	}
	for i := range page.Instances {
		page.Instances[i].Markers = markers
	}

	if err := s.renderHTML(w, http.StatusOK, "service.html", page); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// renderSections fills the page's Instances and Deploys from Render, and
// returns the markers of the deploys that went live. Render failing or
// timing out shows on the page and is logged, without failing it; the
// error returned is the database's.
func (s *Server) renderSections(r *http.Request, page *servicePage, period servicePeriod, now time.Time) ([]chart.Marker, error) {
	ctx, cancel := context.WithTimeout(r.Context(), renderTimeout)
	defer cancel()
	// To the minute, so that loads within a minute reuse the client's
	// cached answers.
	to := now.Truncate(time.Minute)
	from := to.Add(-period.Span).Truncate(period.Step)
	var (
		instances             render.Instances
		deploys               []render.Deploy
		instancesErr, deplErr error
		wg                    sync.WaitGroup
	)
	wg.Go(func() { instances, instancesErr = s.services.Render.Instances(ctx, from, to, period.Step) })
	wg.Go(func() { deploys, deplErr = s.services.Render.Deploys(ctx, deploysFetched) })
	wg.Wait()

	if instancesErr != nil {
		s.logger.Warn("service page: read Render's metrics", "error", instancesErr)
		page.RenderFailed = true
	} else {
		page.Instances = instanceCharts(instances, from, to)
	}
	if deplErr != nil {
		s.logger.Warn("service page: read Render's deploys", "error", deplErr)
		page.DeploysFailed = true
		return nil, nil
	}

	versions, err := s.deployVersions(r.Context(), deploys, now, page.Pinging)
	if err != nil {
		return nil, err
	}
	var markers []chart.Marker
	for _, d := range deploys {
		if d.WentLive() && !d.FinishedAt.Before(from) {
			markers = append(markers, chart.Marker{T: d.FinishedAt, Label: "Deploy " + versions[d.ID].Text})
		}
	}
	slices.SortFunc(markers, func(a, b chart.Marker) int { return a.T.Compare(b.T) })
	for _, d := range deploys[:min(len(deploys), deploysShown)] {
		label, failed := deployStatus(d.Status)
		page.Deploys = append(page.Deploys, deployRow{
			Created: d.CreatedAt, Finished: d.FinishedAt,
			Status: label, Failed: failed, Version: versions[d.ID],
		})
	}
	return markers, nil
}

// deployVersions labels deploys by ID: a deploy that went live with the
// version the pinger last saw before the next one went live (Render's
// records of image-backed deploys carry no commit), and any other, or one
// the pinger didn't see, with its image's digest.
func (s *Server) deployVersions(ctx context.Context, deploys []render.Deploy, now time.Time, pinging bool) (map[string]versionInfo, error) {
	labels := map[string]versionInfo{}
	for _, d := range deploys {
		if _, digest, ok := strings.Cut(d.Image, ":"); ok && len(digest) >= 7 {
			labels[d.ID] = versionInfo{Text: d.Image[:len(d.Image)-len(digest)] + digest[:7]}
		}
	}
	if !pinging {
		return labels, nil
	}
	var live []render.Deploy
	for _, d := range deploys {
		if d.WentLive() {
			live = append(live, d)
		}
	}
	slices.SortFunc(live, func(a, b render.Deploy) int { return a.FinishedAt.Compare(b.FinishedAt) })
	spans := make([]rss.Span, len(live))
	for i, d := range live {
		spans[i] = rss.Span{From: d.FinishedAt, To: now}
		if i+1 < len(live) {
			spans[i].To = live[i+1].FinishedAt
		}
	}
	versions, err := s.services.Availability.VersionsIn(ctx, spans)
	if err != nil {
		return nil, err
	}
	for i, v := range versions {
		if v != "" {
			labels[live[i].ID] = *version(rss.AvailabilityCheck{Version: v})
		}
	}
	return labels, nil
}

// deployStatus is a deploy's status as the table says it, and whether it
// failed.
func deployStatus(status string) (string, bool) {
	switch status {
	case "live":
		return "Live", false
	case "deactivated":
		return "Replaced", false
	case "canceled":
		return "Canceled", false
	case "build_failed", "update_failed", "pre_deploy_failed":
		return "Failed", true
	}
	return "In progress", false
}

// instanceCharts charts Render's figures from from to to: CPU and memory
// with their limits, and the instance count. Each spans the whole period,
// like the runtime charts, so their axes and deploy markers line up. nil
// when Render had none for the period.
func instanceCharts(in render.Instances, from, to time.Time) []chart.LineChart {
	if len(in.CPU) == 0 && len(in.Memory) == 0 && len(in.Count) == 0 {
		return nil
	}
	points := func(ps []render.Point, scale float64) []chart.Point {
		out := make([]chart.Point, 0, len(ps)+2)
		if len(ps) == 0 || ps[0].T.After(from) {
			out = append(out, chart.Point{T: from, V: math.NaN()})
		}
		for _, p := range ps {
			out = append(out, chart.Point{T: p.T, V: p.V * scale})
		}
		if len(ps) == 0 || ps[len(ps)-1].T.Before(to) {
			out = append(out, chart.Point{T: to, V: math.NaN()})
		}
		return out
	}
	limit := func(v, scale float64) []chart.Threshold {
		if v <= 0 {
			return nil
		}
		return []chart.Threshold{{Value: v * scale, Label: "Limit"}}
	}
	const height = 160
	mb := 1.0 / (1 << 20)
	return []chart.LineChart{
		{Title: "CPU", Unit: "CPU", Height: height, Series: []chart.Series{{Name: "CPU", Points: points(in.CPU, 1)}}, Thresholds: limit(in.CPULimit, 1)},
		{Title: "Memory", Unit: "MB", Height: height, Series: []chart.Series{{Name: "Memory", Points: points(in.Memory, mb)}}, Thresholds: limit(in.MemoryLimit, mb)},
		{Title: "Instances", Height: height, Series: []chart.Series{{Name: "Instances", Points: points(in.Count, 1)}}},
	}
}

// runtimeKept says how long runtime metrics are kept and which periods show
// them: "Runtime metrics are kept for 30 days: choose 24h, 7d or 30d."
func runtimeKept() string {
	var keys []string
	for _, p := range servicePeriods {
		if p.Span <= metrics.RuntimeRetention {
			keys = append(keys, p.Key)
		}
	}
	choose := keys[len(keys)-1]
	if len(keys) > 1 {
		choose = strings.Join(keys[:len(keys)-1], ", ") + " or " + choose
	}
	return fmt.Sprintf("Runtime metrics are kept for %d days: choose %s.", int(metrics.RuntimeRetention/(24*time.Hour)), choose)
}

// uptimeTiles are u's four windows as a stat row.
func uptimeTiles(u rss.Uptime) []statTile {
	windows := []struct {
		label string
		w     rss.UptimeWindow
	}{{"Last 24 hours", u.Day}, {"Last 7 days", u.Week}, {"Last 30 days", u.Month}, {"Last 90 days", u.Quarter}}
	tiles := make([]statTile, len(windows))
	for i, w := range windows {
		tiles[i] = statTile{Label: w.label, Text: uptimePercent(w.w)}
		if w.w.Checks > 0 {
			tiles[i].Note = fmt.Sprintf("%s of %s checks", count(w.w.OK), count(w.w.Checks))
		}
	}
	return tiles
}

// uptimePercent is w's share of ok checks with two decimals, rounded down
// so that a single failed check never shows as 100%; "No checks" without
// any.
func uptimePercent(w rss.UptimeWindow) string {
	switch {
	case w.Checks == 0:
		return "No checks"
	case w.OK == w.Checks:
		return "100%"
	}
	p := math.Floor(float64(w.OK)*10000/float64(w.Checks)) / 100
	return fmt.Sprintf("%.2f%%", p)
}

// incidentRows are incidents as the table shows them, their durations
// measured to now while they last.
func incidentRows(incidents []rss.Incident, now time.Time) []incidentRow {
	rows := make([]incidentRow, len(incidents))
	for i, in := range incidents {
		end := now
		if !in.EndedAt.IsZero() {
			end = in.EndedAt
		}
		rows[i] = incidentRow{
			Started:  in.StartedAt,
			Ended:    in.EndedAt,
			Duration: duration(end.Sub(in.StartedAt)),
			Checks:   in.Checks,
			Statuses: strings.Join(in.Statuses, ", "),
		}
	}
	return rows
}

// duration says d in its two largest units, rounded down: "3 min",
// "1 h 12 min", "2 d 4 h".
func duration(d time.Duration) string {
	minutes := int(d / time.Minute)
	switch {
	case minutes < 1:
		return "under 1 min"
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	case minutes < 24*60:
		if m := minutes % 60; m > 0 {
			return fmt.Sprintf("%d h %d min", minutes/60, m)
		}
		return fmt.Sprintf("%d h", minutes/60)
	}
	if h := minutes / 60 % 24; h > 0 {
		return fmt.Sprintf("%d d %d h", minutes/(24*60), h)
	}
	return fmt.Sprintf("%d d", minutes/(24*60))
}

// commitSHA matches a full git commit SHA, which is what production's
// builds report as their version.
var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// version is c's version for the page: a commit SHA shortened and linked
// to the commit, anything else (dev locally) as it is.
func version(c rss.AvailabilityCheck) *versionInfo {
	v := &versionInfo{Text: c.Version, At: c.CheckedAt}
	if commitSHA.MatchString(c.Version) {
		v.Text = c.Version[:7]
		v.Href = "https://github.com/grodier/rss/commit/" + c.Version
	}
	return v
}

// runtimeCharts charts www's runtime samples from from to to, a point per
// step. www can run several instances at once (two overlap during every
// deploy), each with its own row per minute: memory, goroutines and GC
// pause take the highest instance at each minute, since the limits are per
// instance, and connections are summed, since the instances share the
// database's. Each step's point is then the highest of its minutes, so a
// spike isn't averaged away, except for connection waits, a rate: the
// step's waits over its minutes that measured them. A step with no samples
// is a gap. rows are oldest first, as MetricsRepository.ListRuntime returns
// them.
func runtimeCharts(rows []metrics.RuntimeRow, from, to time.Time, step time.Duration) []chart.LineChart {
	// One minute's values across instances.
	type minute struct {
		heap, total, goroutines, gc float64 // the highest instance's
		open, inUse, waits          float64 // summed
		waited                      bool    // an instance measured its waits
	}
	minutes := map[time.Time]*minute{}
	// Each instance's previous sample: waits are the cumulative count's
	// increase since it.
	type sample struct {
		t     time.Time
		count int64
	}
	last := map[string]sample{}
	for _, r := range rows {
		t := r.PeriodStart.UTC()
		m := minutes[t]
		if m == nil {
			m = &minute{}
			minutes[t] = m
		}
		m.heap = max(m.heap, float64(r.HeapBytes)/(1<<20))
		m.total = max(m.total, float64(r.TotalBytes)/(1<<20))
		m.goroutines = max(m.goroutines, float64(r.Goroutines))
		m.gc = max(m.gc, r.GCPauseP99Ms)
		m.open += float64(r.DBOpen)
		m.inUse += float64(r.DBInUse)
		// An instance's first sample has nothing to compare with, and a
		// count that went down is a restarted process's: neither measures
		// waits. An increase over missing samples is spread over the minutes
		// it covers, so the gap's last minute doesn't take it all.
		if prev, ok := last[r.Instance]; ok && r.DBWaitCount >= prev.count {
			gap := max(1, int(t.Sub(prev.t)/time.Minute))
			m.waits += float64(r.DBWaitCount-prev.count) / float64(gap)
			m.waited = true
		}
		last[r.Instance] = sample{t, r.DBWaitCount}
	}

	// Each step's values: index 0 heap, 1 total, 2 goroutines, 3 gc, 4
	// open, 5 in use, 6 waits per minute; samples counts its minutes, and
	// waited those that measured waits.
	const n = 7
	type stepValues struct {
		v               [n]float64
		samples, waited int
	}
	start := from.Truncate(step)
	var steps []stepValues
	for t := start; t.Before(to); t = t.Add(step) {
		steps = append(steps, stepValues{})
	}
	for t, m := range minutes {
		i := int(t.Sub(start) / step)
		if t.Before(start) || i >= len(steps) {
			continue
		}
		s := &steps[i]
		s.samples++
		for j, x := range []float64{m.heap, m.total, m.goroutines, m.gc, m.open, m.inUse} {
			s.v[j] = max(s.v[j], x)
		}
		if m.waited {
			s.v[6] += m.waits
			s.waited++
		}
	}

	var series [n][]chart.Point
	for i, s := range steps {
		t := start.Add(time.Duration(i) * step)
		for j := range s.v {
			v := math.NaN()
			switch {
			case j == 6:
				if s.waited > 0 {
					v = s.v[j] / float64(s.waited)
				}
			case s.samples == 0:
			default:
				v = s.v[j]
			}
			series[j] = append(series[j], chart.Point{T: t, V: v})
		}
	}

	const height = 160
	return []chart.LineChart{
		{Title: "Memory", Unit: "MB", Height: height, Series: []chart.Series{{Name: "Heap", Points: series[0]}, {Name: "Total", Points: series[1]}}},
		{Title: "Goroutines", Height: height, Series: []chart.Series{{Name: "Goroutines", Points: series[2]}}},
		{Title: "GC pause p99", Unit: "ms", Height: height, Series: []chart.Series{{Name: "p99", Points: series[3]}}},
		{Title: "Database connections", Height: height, Series: []chart.Series{{Name: "Open", Points: series[4]}, {Name: "In use", Points: series[5]}}},
		{Title: "Connection waits", Unit: "/min", Height: height, Series: []chart.Series{{Name: "Waits", Points: series[6]}}},
	}
}
