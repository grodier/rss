package server

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

const testChromeUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// newRUMTestServer returns a server whose real-user beacons go to the
// returned store, with the canonical host paperround.app.
func newRUMTestServer(t *testing.T) (*Server, http.Handler, *fakeRUMStore) {
	t.Helper()
	store := &fakeRUMStore{}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://paperround.app"}, Services{RUMService: store})
	return s, s.router(), store
}

// rumBeacon returns a beacon from path with the fields every beacon has,
// plus extra (name, value pairs).
func rumBeacon(path string, extra ...string) url.Values {
	form := url.Values{"path": {path}, "nav": {"navigate"}, "width": {"1280"}, "installed": {"0"}}
	for i := 0; i < len(extra); i += 2 {
		form.Set(extra[i], extra[i+1])
	}
	return form
}

// postRUM sends form to POST /rum as rum.ts does, from a same-origin page
// with User-Agent ua, and checks the answer is a 204.
func postRUM(t *testing.T, h http.Handler, form url.Values, ua string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rum", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", ua)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("POST /rum = %d, want %d", rr.Code, http.StatusNoContent)
	}
	return rr
}

// onlyView returns the one view store has, failing the test if it hasn't
// exactly one.
func onlyView(t *testing.T, store *fakeRUMStore) rss.RUMView {
	t.Helper()
	views := store.recorded()
	if len(views) != 1 {
		t.Fatalf("stored %d views, want 1: %+v", len(views), views)
	}
	return views[0]
}

func TestRUMStoresRoutePattern(t *testing.T) {
	tests := []struct{ path, route string }{
		{"/feeds/" + testFeedID, "/feeds/{id}"},
		{"/shared/Zm9vYmFyYmF6cXV4cXV1eA", "/shared/{token}"},
		{"/timelines/all", "/timelines/all"},
		{"/settings/reading", "/settings/reading"},
		{"/", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, h, store := newRUMTestServer(t)
			postRUM(t, h, rumBeacon(tt.path), testChromeUA)
			if got := onlyView(t, store).Route; got != tt.route {
				t.Errorf("route = %q, want %q", got, tt.route)
			}
		})
	}
}

func TestRUMStoresView(t *testing.T) {
	_, h, store := newRUMTestServer(t)
	postRUM(t, h, rumBeacon("/feeds/"+testFeedID,
		"nav", "reload",
		"width", "390",
		"installed", "1",
		"connection", "4g",
		"protocol", "h2",
		"referrer", "News.Example.com",
		"ttfb", "120.5",
		"fcp", "300",
		"lcp", "610.3",
		"inp", "96",
		"cls", "0.05",
		"dcl", "420.3",
		"load", "980",
		"server", "12.3",
		"transfer", "12345",
		"lcp_element", "img",
		"inp_target", "data-share-url",
	), testChromeUA)

	v := onlyView(t, store)
	f := func(p *float64) string {
		if p == nil {
			return "nil"
		}
		return fmt.Sprint(*p)
	}
	got := fmt.Sprint(v.Route, v.NavType, v.Device, v.Browser, v.BrowserMajor, v.Installed, v.Connection, v.Protocol,
		v.ReferrerHost, f(v.TTFBMs), f(v.FCPMs), f(v.LCPMs), f(v.INPMs), f(v.CLS), f(v.DCLMs), f(v.LoadMs),
		f(v.ServerMs), *v.TransferBytes, v.LCPElement, v.INPTarget)
	want := fmt.Sprint("/feeds/{id}", "reload", "phone", "chrome", 141, true, "4g", "h2",
		"news.example.com", "120.5", "300", "610.3", "96", "0.05", "420.3", "980",
		"12.3", 12345, "img", "data-share-url")
	if got != want {
		t.Errorf("view = %s,\nwant   %s", got, want)
	}
	if !v.ReceivedAt.IsZero() {
		t.Errorf("ReceivedAt = %v, want zero (the database's now)", v.ReceivedAt)
	}
}

func TestRUMDropsUnknownPath(t *testing.T) {
	tests := map[string]url.Values{
		"no path":         rumBeacon(""),
		"relative path":   rumBeacon("feeds"),
		"query":           rumBeacon("/feeds?id=" + testFeedID),
		"no page":         rumBeacon("/nope"),
		"POST-only route": rumBeacon("/timeline/seen"),
		"another host":    rumBeacon("//evil.example/feeds"),
		"bad nav type":    rumBeacon("/", "nav", "teleport"),
		"no nav type":     rumBeacon("/", "nav", ""),
		"no width":        rumBeacon("/", "width", ""),
		"zero width":      rumBeacon("/", "width", "0"),
		"bad width":       rumBeacon("/", "width", "wide"),
	}
	for name, form := range tests {
		t.Run(name, func(t *testing.T) {
			_, h, store := newRUMTestServer(t)
			postRUM(t, h, form, testChromeUA)
			if views := store.recorded(); len(views) != 0 {
				t.Errorf("stored %+v, want nothing", views)
			}
		})
	}
}

func TestRUMDropsUnreadableBeacon(t *testing.T) {
	_, h, store := newRUMTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rum", strings.NewReader("path=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("POST /rum = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if views := store.recorded(); len(views) != 0 {
		t.Errorf("stored %+v, want nothing", views)
	}
}

func TestRUMClamps(t *testing.T) {
	floats := map[string]func(rss.RUMView) *float64{
		"ttfb":   func(v rss.RUMView) *float64 { return v.TTFBMs },
		"fcp":    func(v rss.RUMView) *float64 { return v.FCPMs },
		"lcp":    func(v rss.RUMView) *float64 { return v.LCPMs },
		"inp":    func(v rss.RUMView) *float64 { return v.INPMs },
		"dcl":    func(v rss.RUMView) *float64 { return v.DCLMs },
		"load":   func(v rss.RUMView) *float64 { return v.LoadMs },
		"server": func(v rss.RUMView) *float64 { return v.ServerMs },
		"cls":    func(v rss.RUMView) *float64 { return v.CLS },
	}
	show := func(p *float64) string {
		if p == nil {
			return "NULL"
		}
		return fmt.Sprint(*p)
	}
	for field, get := range floats {
		limit, within := "120000", "612.5"
		if field == "cls" {
			limit, within = "10", "0.125"
		}
		for value, want := range map[string]string{
			"0":         "0",
			within:      within,
			limit:       limit,
			limit + "1": "NULL", // ten times over
			"-1":        "NULL",
			"NaN":       "NULL",
			"Infinity":  "NULL",
			"fast":      "NULL",
			"":          "NULL",
		} {
			t.Run(field+"="+value, func(t *testing.T) {
				_, h, store := newRUMTestServer(t)
				postRUM(t, h, rumBeacon("/", field, value), testChromeUA)
				if got := show(get(onlyView(t, store))); got != want {
					t.Errorf("%s = %s, want %s", field, got, want)
				}
			})
		}
	}

	for value, want := range map[string]string{
		"0":        "0",
		"52428800": "52428800", // 50 MB
		"52428801": "NULL",
		"-1":       "NULL",
		"1.5":      "NULL",
		"":         "NULL",
	} {
		t.Run("transfer="+value, func(t *testing.T) {
			_, h, store := newRUMTestServer(t)
			postRUM(t, h, rumBeacon("/", "transfer", value), testChromeUA)
			got := "NULL"
			if b := onlyView(t, store).TransferBytes; b != nil {
				got = fmt.Sprint(*b)
			}
			if got != want {
				t.Errorf("transfer = %s, want %s", got, want)
			}
		})
	}
}

func TestRUMContext(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
		check func(rss.RUMView) string
		want  string
	}{
		{"width 599", []string{"width", "599"}, func(v rss.RUMView) string { return v.Device }, "phone"},
		{"width 600", []string{"width", "600"}, func(v rss.RUMView) string { return v.Device }, "tablet"},
		{"width 1023", []string{"width", "1023"}, func(v rss.RUMView) string { return v.Device }, "tablet"},
		{"width 1024", []string{"width", "1024"}, func(v rss.RUMView) string { return v.Device }, "desktop"},
		{"installed", []string{"installed", "1"}, func(v rss.RUMView) string { return fmt.Sprint(v.Installed) }, "true"},
		{"not installed", []string{"installed", "yes"}, func(v rss.RUMView) string { return fmt.Sprint(v.Installed) }, "false"},
		{"unknown connection", []string{"connection", "5g"}, func(v rss.RUMView) string { return v.Connection }, ""},
		{"unknown protocol", []string{"protocol", "quic"}, func(v rss.RUMView) string { return v.Protocol }, ""},
		{"our own referrer", []string{"referrer", "PaperRound.app"}, func(v rss.RUMView) string { return v.ReferrerHost }, ""},
		{"referrer with a path", []string{"referrer", "example.com/a"}, func(v rss.RUMView) string { return v.ReferrerHost }, ""},
		{"referrer with a port", []string{"referrer", "example.com:8080"}, func(v rss.RUMView) string { return v.ReferrerHost }, ""},
		{"SVG tag", []string{"lcp_element", "foreignObject"}, func(v rss.RUMView) string { return v.LCPElement }, "foreignobject"},
		{"LCP element text", []string{"lcp_element", "Read more"}, func(v rss.RUMView) string { return v.LCPElement }, ""},
		{"INP target URL", []string{"inp_target", "https://example.com/"}, func(v rss.RUMView) string { return v.INPTarget }, ""},
		{"INP target too long", []string{"inp_target", "data-" + strings.Repeat("a", 60)}, func(v rss.RUMView) string { return v.INPTarget }, ""},
		{"restore", []string{"nav", "restore"}, func(v rss.RUMView) string { return v.NavType }, "restore"},
		{"back_forward", []string{"nav", "back_forward"}, func(v rss.RUMView) string { return v.NavType }, "back_forward"},
		{"prerender", []string{"nav", "prerender"}, func(v rss.RUMView) string { return v.NavType }, "prerender"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, h, store := newRUMTestServer(t)
			postRUM(t, h, rumBeacon("/", tt.extra...), testChromeUA)
			if got := tt.check(onlyView(t, store)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRUMBrowser(t *testing.T) {
	tests := []struct {
		ua      string
		browser string
		major   int
	}{
		{testChromeUA, "chrome", 141},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.51 Mobile Safari/537.36", "chrome", 140},
		// Opera has no family of its own here: Chrome's, by its engine.
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 OPR/124.0.0.0", "chrome", 140},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.3537.57", "edge", 141},
		{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36 EdgA/140.0.3485.98", "edge", 140},
		{"Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/28.0 Chrome/130.0.0.0 Mobile Safari/537.36", "samsung", 28},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:144.0) Gecko/20100101 Firefox/144.0", "firefox", 144},
		{"Mozilla/5.0 (Android 15; Mobile; rv:143.0) Gecko/143.0 Firefox/143.0", "firefox", 143},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Safari/605.1.15", "safari", 26},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.2 Mobile/15E148 Safari/604.1", "safari", 26},
		{"Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1", "safari", 26},
		// Every browser on iOS uses Safari's engine; only Safari's own
		// User-Agent says which version.
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/141.0.7390.41 Mobile/15E148 Safari/604.1", "safari", 0},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/143.0 Mobile/15E148 Safari/605.1.15", "safari", 0},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 EdgiOS/141.0.3537.43 Mobile/15E148 Safari/605.1.15", "safari", 0},
		{"curl/8.5.0", "other", 0},
		{"", "other", 0},
		{"Mozilla/5.0 Chrome/999999999999.0", "chrome", 99999},
	}
	for _, tt := range tests {
		t.Run(tt.ua, func(t *testing.T) {
			_, h, store := newRUMTestServer(t)
			postRUM(t, h, rumBeacon("/"), tt.ua)
			v := onlyView(t, store)
			if v.Browser != tt.browser || v.BrowserMajor != tt.major {
				t.Errorf("browser = %s %d, want %s %d", v.Browser, v.BrowserMajor, tt.browser, tt.major)
			}
		})
	}
}

func TestRUMRateLimit(t *testing.T) {
	_, h, store := newRUMTestServer(t)
	for range 121 {
		postRUM(t, h, rumBeacon("/"), testChromeUA)
	}
	if n := len(store.recorded()); n != 120 {
		t.Errorf("stored %d of 121 beacons from one address, want 120", n)
	}

	// Another address has its own limit.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rum", strings.NewReader(rumBeacon("/").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "198.51.100.7:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("POST /rum from another address = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if n := len(store.recorded()); n != 121 {
		t.Errorf("stored %d beacons, want 121 with the other address's", n)
	}
}

func TestRUMTotalRateLimit(t *testing.T) {
	_, h, store := newRUMTestServer(t)
	// One beacon from each of 601 addresses, as from a client rotating
	// them: each is within its own limit, and the total is capped.
	for i := range 601 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rum", strings.NewReader(rumBeacon("/").Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = fmt.Sprintf("[2001:db8:%x::1]:1234", i)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("POST /rum %d = %d, want %d", i, rr.Code, http.StatusNoContent)
		}
	}
	if n := len(store.recorded()); n != 600 {
		t.Errorf("stored %d of 601 beacons from as many addresses, want 600", n)
	}
}

func TestRUMNoSession(t *testing.T) {
	_, h, store := newRUMTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rum", strings.NewReader(rumBeacon("/").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("POST /rum = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if c := rr.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Errorf("Set-Cookie = %q, want none", c)
	}
	onlyView(t, store)
}

func TestRUMStoreFailure(t *testing.T) {
	s, h, store := newRUMTestServer(t)
	store.err = errors.New("database down")
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))

	postRUM(t, h, rumBeacon("/"), testChromeUA)
	if !strings.Contains(logs.String(), "database down") {
		t.Errorf("log = %q, want the store's error", logs.String())
	}
}
