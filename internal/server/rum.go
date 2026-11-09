package server

import (
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

// rumForm is the real-user beacon (fields in web/app/features/rum.ts). Its
// fields are strings, so a malformed one is dropped on its own rather than
// with the beacon.
type rumForm struct {
	Path       string `form:"path"`
	Nav        string `form:"nav"`
	Width      string `form:"width"`
	Installed  string `form:"installed"`
	Connection string `form:"connection"`
	Protocol   string `form:"protocol"`
	Referrer   string `form:"referrer"`
	TTFB       string `form:"ttfb"`
	FCP        string `form:"fcp"`
	LCP        string `form:"lcp"`
	INP        string `form:"inp"`
	CLS        string `form:"cls"`
	DCL        string `form:"dcl"`
	Load       string `form:"load"`
	Server     string `form:"server"`
	Transfer   string `form:"transfer"`
	LCPElement string `form:"lcp_element"`
	INPTarget  string `form:"inp_target"`
}

// The largest values a beacon's numbers can have; anything outside 0 to
// these is stored as NULL.
const (
	rumMaxMs            = 120_000
	rumMaxCLS           = 10
	rumMaxTransferBytes = 50 << 20
)

// Viewport widths below these are a phone's and a tablet's; the rest are
// desktops.
const (
	phoneMaxWidth  = 600
	tabletMaxWidth = 1024
)

var (
	rumNavTypes    = []string{"navigate", "reload", "back_forward", "prerender", "restore"}
	rumConnections = []string{"slow-2g", "2g", "3g", "4g"}
	rumProtocols   = []string{"http/0.9", "http/1.0", "http/1.1", "h2", "h3"}
	// rumNameRX matches a tag or data-* attribute name, lowercased.
	rumNameRX = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	// rumHostRX matches a host name, lowercased.
	rumHostRX = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
)

// rumHandler stores a page view's real-user beacon, sent by rum.ts when the
// page is hidden (see "Observability" in docs/decisions.md). It answers 204
// whatever happens: the page has gone, and nothing reads the answer. It
// drops a beacon over rumLimiter's or rumTotalLimiter's limit, one it can't
// read, and one whose path is no page of ours; of the path it keeps only the
// route pattern, and of the User-Agent only the browser's family and major
// version.
func (s *Server) rumHandler(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)

	// The address's limit first, so one client over it can't use up
	// everyone's.
	if !s.rumLimiter.Allow(ipRateLimitKey(s.clientIP(r))) || !s.rumTotalLimiter.Allow("") {
		return
	}
	var form rumForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		return
	}
	view, ok := s.rumView(r, form)
	if !ok {
		return
	}
	if err := s.services.RUMService.Record(r.Context(), view); err != nil {
		s.logError(r, err)
	}
}

// rumView returns the view f, posted in r, describes, or false if it can't
// be stored: its path is no page's, or its navigation type or width isn't
// valid.
func (s *Server) rumView(r *http.Request, f rumForm) (rss.RUMView, bool) {
	route, ok := rumRoute(r, f.Path)
	if !ok || !slices.Contains(rumNavTypes, f.Nav) {
		return rss.RUMView{}, false
	}
	width, err := strconv.Atoi(f.Width)
	if err != nil || width <= 0 {
		return rss.RUMView{}, false
	}
	browser, major := parseBrowser(r.UserAgent())
	return rss.RUMView{
		Route:         route,
		NavType:       f.Nav,
		Device:        deviceClass(width),
		Browser:       browser,
		BrowserMajor:  major,
		Installed:     f.Installed == "1",
		Connection:    oneOf(f.Connection, rumConnections),
		Protocol:      oneOf(f.Protocol, rumProtocols),
		ReferrerHost:  s.rumReferrer(f.Referrer),
		TTFBMs:        rumNumber(f.TTFB, rumMaxMs),
		FCPMs:         rumNumber(f.FCP, rumMaxMs),
		LCPMs:         rumNumber(f.LCP, rumMaxMs),
		INPMs:         rumNumber(f.INP, rumMaxMs),
		CLS:           rumNumber(f.CLS, rumMaxCLS),
		DCLMs:         rumNumber(f.DCL, rumMaxMs),
		LoadMs:        rumNumber(f.Load, rumMaxMs),
		ServerMs:      rumNumber(f.Server, rumMaxMs),
		TransferBytes: rumBytes(f.Transfer, rumMaxTransferBytes),
		LCPElement:    rumName(f.LCPElement),
		INPTarget:     rumName(f.INPTarget),
	}, true
}

// rumRoute returns the route pattern of the page at path, such as
// /feeds/{id}, or false if no page is there, looked up in the router that
// is serving r. The path itself is never kept: it can hold IDs and tokens.
func rumRoute(r *http.Request, path string) (string, bool) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "?") {
		return "", false
	}
	route := chi.RouteContext(r.Context()).Routes.Find(chi.NewRouteContext(), http.MethodGet, path)
	return route, route != ""
}

// rumReferrer returns host, lowercased, if it's another site's host name,
// else "".
func (s *Server) rumReferrer(host string) string {
	host = strings.ToLower(host)
	if !rumHostRX.MatchString(host) || host == s.canonicalHost {
		return ""
	}
	return host
}

// deviceClass names the kind of device a viewport width px wide is on.
func deviceClass(width int) string {
	switch {
	case width < phoneMaxWidth:
		return "phone"
	case width < tabletMaxWidth:
		return "tablet"
	}
	return "desktop"
}

// rumNumber parses a beacon's number, or returns nil if it's missing, isn't
// a number, or is outside 0 to max.
func rumNumber(v string, max float64) *float64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || f < 0 || f > max {
		return nil
	}
	return &f
}

// rumBytes parses a beacon's byte count as rumNumber does.
func rumBytes(v string, max int64) *int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || n > max {
		return nil
	}
	return &n
}

// rumName returns a tag or data-* attribute name, lowercased, or "" if v
// isn't one.
func rumName(v string) string {
	v = strings.ToLower(v)
	if !rumNameRX.MatchString(v) {
		return ""
	}
	return v
}

// oneOf returns v if it's one of allowed, else "".
func oneOf(v string, allowed []string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return ""
}

// parseBrowser returns the browser family a User-Agent names (edge, chrome,
// firefox, safari, samsung or other) and its major version, 0 if unknown.
// Every browser on iOS and iPadOS uses Safari's engine and measures what
// Safari does, so it counts as Safari, whose version only Safari's own
// User-Agent gives. Other browsers built on Chromium without a family of
// their own here (Opera, Brave) count as Chrome, by its version.
func parseBrowser(ua string) (string, int) {
	has := func(token string) bool { return strings.Contains(ua, token) }
	switch {
	case has("iPhone") || has("iPad") || has("iPod"):
		if has("CriOS/") || has("FxiOS/") || has("EdgiOS/") || !has("Version/") {
			return "safari", 0
		}
		return "safari", majorVersion(ua, "Version/")
	case has("Edg/"):
		return "edge", majorVersion(ua, "Edg/")
	case has("EdgA/"):
		return "edge", majorVersion(ua, "EdgA/")
	case has("SamsungBrowser/"):
		return "samsung", majorVersion(ua, "SamsungBrowser/")
	case has("Firefox/"):
		return "firefox", majorVersion(ua, "Firefox/")
	case has("Chrome/"):
		return "chrome", majorVersion(ua, "Chrome/")
	case has("Safari/") && has("Version/"):
		return "safari", majorVersion(ua, "Version/")
	}
	return "other", 0
}

// majorVersion returns the number right after token in ua (at most five
// digits), or 0 if there's none.
func majorVersion(ua, token string) int {
	_, rest, ok := strings.Cut(ua, token)
	if !ok {
		return 0
	}
	n := 0
	for i := 0; i < len(rest) && i < 5 && '0' <= rest[i] && rest[i] <= '9'; i++ {
		n = n*10 + int(rest[i]-'0')
	}
	return n
}
