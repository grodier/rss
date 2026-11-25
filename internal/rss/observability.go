package rss

import "time"

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

// AvailabilityCheck is the outcome of one of the admin's checks of www's
// /healthcheck over the private network (an availability_checks row).
type AvailabilityCheck struct {
	CheckedAt time.Time
	OK        bool     // www answered "available" in time
	Status    string   // the body's status, or CheckTimeout or CheckError
	LatencyMs *float64 // nil when www didn't answer
	Version   string   // the body's system_info.version; "" when it didn't say
}

// Span is a range of time, From included and To not.
type Span struct {
	From, To time.Time
}

// The statuses of an AvailabilityCheck www didn't answer: it took longer
// than the check's timeout, or the request failed or its answer wasn't a
// healthcheck's.
const (
	CheckTimeout = "timeout"
	CheckError   = "error"
)

// Uptime is the share of the admin's availability checks that were ok,
// over the last 24 hours, 7 days, 30 days and 90 days.
type Uptime struct {
	Day, Week, Month, Quarter UptimeWindow
}

// UptimeWindow counts a window's checks; Checks is 0 when none were made.
type UptimeWindow struct {
	Checks, OK int
}

// Incident is a run of failed availability checks with no ok check between
// them.
type Incident struct {
	StartedAt    time.Time // the first failed check
	LastFailedAt time.Time
	EndedAt      time.Time // the first ok check after it; zero while it lasts
	Checks       int       // failed checks in it
	Statuses     []string  // the failed checks' statuses, distinct, sorted
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
	// NotRestored names why a back_forward view wasn't restored from the
	// back/forward cache, as the browser reports it (Chrome's
	// notRestoredReasons); nil for every other view.
	NotRestored []string
}

// WebPerfQuery says which real-user views the admin's web performance page
// reads: those received in [From, To), on Device ("" for every device),
// from rum_views, or from rum_daily's whole UTC days when Rollups is set
// (From and To are then midnights), with Series' points Step apart.
type WebPerfQuery struct {
	From, To time.Time
	Device   string
	Rollups  bool
	Step     time.Duration
}

// WebPerf is the real-user views of a WebPerfQuery. Installed, Referrers,
// Protocols, Connections and BackForward are only in rum_views, so they're
// nil (or zero) from rollups.
type WebPerf struct {
	Total       RUMGroup
	Devices     []RUMGroup
	Browsers    []RUMGroup
	Installed   []RUMGroup // Key "true" or "false"
	Routes      []RUMGroup
	Series      []RUMPoint
	RouteViews  []RUMRouteViews
	Referrers   []RUMCount // the 20 with the most views, most first; never ""
	Protocols   []RUMCount // Key "" when not reported
	Connections []RUMCount // Key "" when not reported
	BackForward RUMBackForward
}

// WebPerfRoute is one route's real-user views in a WebPerfQuery.
// LCPElements, INPTargets, NavTypes and Transfer are only in rum_views, so
// they're nil (or zero) from rollups.
type WebPerfRoute struct {
	// Total's Metrics are every metric's: ttfb, fcp, lcp, inp and cls, and
	// server, the server's time from Server-Timing, which has no
	// thresholds, so no views by rating.
	Total RUMGroup
	// Buckets are each metric's views per metrics.RUMBounds bucket, for
	// its distribution, by metric; one no view reported is missing.
	Buckets  map[string][]int
	Devices  []RUMGroup
	Browsers []RUMGroup
	Series   []RUMPoint // every metric's, server included
	// LCPElements are the LCP element's tag names, with LCP's stats, and
	// INPTargets the slowest interaction's targets, with INP's: the 20
	// with the most views, and for targets the most slow ones (needing
	// improvement or poor) first. Key "" when not reported.
	LCPElements []RUMGroup
	INPTargets  []RUMGroup
	NavTypes    []RUMCount // navigate, reload, back_forward, prerender, restore; most first
	Transfer    RUMTransfer
}

// RUMTransfer is the size of the views' HTML documents over the network.
type RUMTransfer struct {
	Views  int // that reported a size
	Cached int // of them, 0 bytes: from the browser's cache
	// P50, P75 and P95 are the sizes of the rest, in bytes.
	P50, P75, P95 float64
}

// RUMBackForward is the Back and Forward views: those restored from the
// back/forward cache, and those loaded again with why they weren't
// restored.
type RUMBackForward struct {
	Restored    int // restore views
	NotRestored int // back_forward views
	// Unexplained is the back_forward views with no reason kept: the
	// browser reports none (Safari, Firefox, Chrome before 123), gave none,
	// or named none the server keeps (rumNotRestored).
	Unexplained int
	// Reasons are the 20 reasons named by the most back_forward views,
	// most first; a view can name up to 3.
	Reasons []RUMCount
}

// RUMGroup is the views of one value of a dimension (a device, a browser,
// a route…), Key, or of every value.
type RUMGroup struct {
	Key   string
	Views int
	// Metrics are the views' stats by metric: ttfb, fcp, lcp, inp and cls
	// in WebPerf's Total, only the Core Web Vitals (lcp, inp, cls) in its
	// breakdowns. One no view reported is missing.
	Metrics map[string]RUMStat
}

// RUMStat is one metric's values over some views.
type RUMStat struct {
	Views int // that reported it
	// P75, P90 and P95 are its percentiles: exact from rum_views,
	// interpolated within a bucket from rum_daily.
	P75, P90, P95 float64
	// Good, NeedsImprovement and Poor count the views by
	// metrics.RUMThresholds.
	Good, NeedsImprovement, Poor int
}

// RUMPoint is the views of one step of WebPerf's Series, starting at T.
type RUMPoint struct {
	T       time.Time
	Metrics map[string]RUMStat
}

// RUMRouteViews is how many views a route had in the step starting at T.
type RUMRouteViews struct {
	T     time.Time
	Route string
	Views int
}

// RUMCount is how many views had a value, Key.
type RUMCount struct {
	Key   string
	Views int
}

// EndpointQuery says which request metrics (request_metrics) the admin's
// endpoints pages read: those of periods starting in [From, To), from the
// minute rows, or from the hourly rows when Hourly is set (and the minute
// rows of the hours not rolled up yet), with Series' points Step apart
// from From. A Route ("" for every route) and Method pick one route's.
type EndpointQuery struct {
	From, To      time.Time
	Hourly        bool
	Step          time.Duration
	Route, Method string
}

// RequestStats are some requests' counts, sizes and latencies.
type RequestStats struct {
	Requests int
	// Classes counts the requests by status class: Classes[0] the 1xx,
	// Classes[4] the 5xx.
	Classes       [5]int
	DurationMsSum float64
	BytesSum      int64
	// Buckets counts the requests per metrics.DurationBounds bucket, the
	// last above every bound.
	Buckets []int
}

// Endpoints are the request metrics of an EndpointQuery: every request's,
// a step at a time, by route and method (without a Route), and by
// instance (with one, from minute rows only, as hourly rows have none).
type Endpoints struct {
	Total     RequestStats
	Series    []EndpointPoint
	Routes    []EndpointRoute
	Instances []EndpointInstance
}

// EndpointPoint is the requests of a step of Endpoints' Series, starting
// at T.
type EndpointPoint struct {
	T time.Time
	RequestStats
}

// EndpointRoute is a route's requests with one method.
type EndpointRoute struct {
	Route, Method string
	RequestStats
}

// EndpointInstance is the requests one www instance served.
type EndpointInstance struct {
	Instance string
	RequestStats
}
