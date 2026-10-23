package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

// debugSession is a logged-in user on the debug allowlist of debugConfig.
var debugSession = rss.Session{
	ID:   "123e4567-e89b-12d3-a456-426614174098",
	User: rss.User{ID: "123e4567-e89b-12d3-a456-426614174002", Name: "Grace Hopper", Email: "Grace@Example.com"},
}

// debugConfig allows debugSession's user (in a different case).
var debugConfig = Config{Port: 4000, Env: "development", DebugEmails: []string{"grace@example.com"}}

// serveDebug serves h for req as sess (anonymous if sess is nil), with debug
// mode turned on in the session first if debugOn. It returns the response and
// the session's "debug" and "flash" values after h ran.
func serveDebug(t *testing.T, s *Server, h http.HandlerFunc, req *http.Request, sess *rss.Session, debugOn bool) (rr *httptest.ResponseRecorder, debug bool, flash string) {
	t.Helper()
	if sess != nil {
		ctx := context.WithValue(req.Context(), authenticatedUserIDContextKey, sess.User.ID)
		ctx = context.WithValue(ctx, authenticatedSessionContextKey, *sess)
		req = req.WithContext(ctx)
	}
	rr = httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if debugOn {
			s.sessionManager.Put(r.Context(), "debug", true)
		}
		h(w, r)
		debug = s.sessionManager.GetBool(r.Context(), "debug")
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, debug, flash
}

func TestDebugAllowed(t *testing.T) {
	other := rss.Session{ID: debugSession.ID, User: rss.User{ID: debugSession.User.ID, Email: "someone@example.com"}}
	tests := []struct {
		name   string
		emails []string
		sess   *rss.Session
		want   bool
	}{
		{"allowed email", []string{"grace@example.com"}, &rss.Session{User: rss.User{Email: "grace@example.com"}}, true},
		{"different case", []string{"grace@example.com"}, &debugSession, true},
		{"not listed", []string{"grace@example.com"}, &other, false},
		{"empty allowlist", nil, &debugSession, false},
		{"anonymous", []string{"grace@example.com"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", DebugEmails: tt.emails}, Services{})
			var got bool
			serveDebug(t, s, func(w http.ResponseWriter, r *http.Request) { got = s.debugAllowed(r) },
				httptest.NewRequest(http.MethodGet, "/", nil), tt.sess, false)
			if got != tt.want {
				t.Errorf("debugAllowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDebugEnabled(t *testing.T) {
	tests := []struct {
		name    string
		emails  []string
		debugOn bool
		want    bool
	}{
		{"allowed and on", debugConfig.DebugEmails, true, true},
		{"allowed and off", debugConfig.DebugEmails, false, false},
		{"on but no longer allowed", []string{"someone@example.com"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", DebugEmails: tt.emails}, Services{})
			var got bool
			serveDebug(t, s, func(w http.ResponseWriter, r *http.Request) { got = s.debugEnabled(r) },
				httptest.NewRequest(http.MethodGet, "/", nil), &debugSession, tt.debugOn)
			if got != tt.want {
				t.Errorf("debugEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}

func postDebug(on string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/settings/debug", strings.NewReader(url.Values{"on": {on}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestSettingsDebugFormHandler(t *testing.T) {
	t.Run("on=1 turns debug mode on", func(t *testing.T) {
		s := newTestServerConfig(t, debugConfig, Services{})
		rr, debug, flash := serveDebug(t, s, s.settingsDebugFormHandler, postDebug("1"), &debugSession, false)

		assertRedirect(t, rr, "/settings")
		if !debug {
			t.Error("debug is not set in the session")
		}
		if want := "Debug mode is on for this device."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("on=0 turns debug mode off", func(t *testing.T) {
		s := newTestServerConfig(t, debugConfig, Services{})
		rr, debug, flash := serveDebug(t, s, s.settingsDebugFormHandler, postDebug("0"), &debugSession, true)

		assertRedirect(t, rr, "/settings")
		if debug {
			t.Error("debug is still set in the session")
		}
		if want := "Debug mode is off for this device."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("not allowed", func(t *testing.T) {
		s := newTestServerConfig(t, Config{Port: 4000, Env: "development", DebugEmails: []string{"someone@example.com"}}, Services{})
		rr, debug, _ := serveDebug(t, s, s.settingsDebugFormHandler, postDebug("1"), &debugSession, false)

		assertNotFoundHTML(t, rr)
		if debug {
			t.Error("debug was set in the session")
		}
	})

	t.Run("cross-origin", func(t *testing.T) {
		s := newTestServerConfig(t, debugConfig, Services{})
		req := postDebug("1")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
	})
}

func TestDebugTime(t *testing.T) {
	now := time.Date(2026, 10, 23, 17, 2, 11, 0, time.UTC)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"past", now.Add(-3*time.Hour - 5*time.Minute), "2026-10-23T13:57:11Z (3h ago)"},
		{"future", now.Add(12*time.Minute + 30*time.Second), "2026-10-23T17:14:41Z (in 12m)"},
		{"days ago, in UTC", now.Add(-72 * time.Hour).In(time.FixedZone("X", 3600)), "2026-10-20T17:02:11Z (3d ago)"},
		{"seconds", now.Add(-5 * time.Second), "2026-10-23T17:02:06Z (5s ago)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := debugTime(tt.t, now); got != tt.want {
				t.Errorf("debugTime = %q, want %q", got, tt.want)
			}
		})
	}
}

// fieldValue returns the value of the field named name, and whether there is one.
func fieldValue(fields []debugField, name string) (string, bool) {
	i := slices.IndexFunc(fields, func(f debugField) bool { return f.Name == name })
	if i < 0 {
		return "", false
	}
	return fields[i].Value, true
}

func TestFeedDebugFieldsStatus(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		feed rss.Feed
		want string
	}{
		{"gone", rss.Feed{GoneAt: now, ConsecutiveFailures: 2, LastAttempt: now}, "gone"},
		{"failing", rss.Feed{ConsecutiveFailures: 3, LastAttempt: now}, "failing (3 in a row)"},
		{"never fetched", rss.Feed{}, "never fetched"},
		{"ok", rss.Feed{LastAttempt: now}, "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := feedDebugFields(tt.feed, rss.FeedDebug{}, now)
			if fields[0].Name != "status" || fields[0].Value != tt.want {
				t.Errorf("first field = %+v, want status %q", fields[0], tt.want)
			}
		})
	}

	t.Run("columns and derived values", func(t *testing.T) {
		f := rss.Feed{ID: "f1", LastError: "boom", RefreshRequestedAt: now.Add(-time.Minute)}
		fields := feedDebugFields(f, rss.FeedDebug{Articles: 7, EnrichJobs: 2}, now)
		for name, want := range map[string]string{
			"articles":          "7",
			"enrich jobs":       "2",
			"refresh requested": "2026-10-23T11:59:00Z (1m ago)",
			"id":                "f1",
			"last_error":        "boom",
			"gone_at":           "",
		} {
			if got, ok := fieldValue(fields, name); !ok || got != want {
				t.Errorf("%s = %q (present %v), want %q", name, got, ok, want)
			}
		}
		if _, ok := fieldValue(feedDebugFields(rss.Feed{}, rss.FeedDebug{}, now), "refresh requested"); ok {
			t.Error("refresh requested shown without a request")
		}
	})
}

func TestSiteDebugFields(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/"}
	lookup := rss.Lookup{ID: "l1", Status: rss.LookupFailed, URL: "https://example.com/", Error: "timeout", Attempts: 2}

	t.Run("with an icon", func(t *testing.T) {
		d := rss.SiteDebug{
			IconCheckedAt: now.Add(-time.Hour),
			Icon:          rss.SiteIcon{SiteID: testSiteID, SourceURL: "https://example.com/i.png", ContentType: "image/png", Width: 32, Height: 32, Hash: "abc"},
			IconBytes:     4198,
		}
		fields := siteDebugFields(site, d, lookup, now)
		for name, want := range map[string]string{
			"host":            "example.com",
			"lookup status":   "failed",
			"lookup error":    "timeout",
			"lookup attempts": "2",
			"icon_checked_at": "2026-10-23T11:00:00Z (1h ago)",
			"icon source_url": "https://example.com/i.png",
			"icon type":       "image/png 32×32, 4.1 KiB",
			"icon hash":       "abc",
		} {
			if got, ok := fieldValue(fields, name); !ok || got != want {
				t.Errorf("%s = %q (present %v), want %q", name, got, ok, want)
			}
		}
		if _, ok := fieldValue(fields, "icon"); ok {
			t.Error(`"icon" = none shown for a site with an icon`)
		}
	})

	t.Run("without an icon", func(t *testing.T) {
		fields := siteDebugFields(site, rss.SiteDebug{}, lookup, now)
		if got, _ := fieldValue(fields, "icon"); got != "none (letter avatar)" {
			t.Errorf("icon = %q, want %q", got, "none (letter avatar)")
		}
		if _, ok := fieldValue(fields, "icon source_url"); ok {
			t.Error("icon source_url shown for a site without an icon")
		}
	})

	t.Run("with no lookup", func(t *testing.T) {
		fields := siteDebugFields(site, rss.SiteDebug{}, rss.Lookup{}, now)
		if got, _ := fieldValue(fields, "lookup"); got != "none" {
			t.Errorf("lookup = %q, want none", got)
		}
		if _, ok := fieldValue(fields, "lookup status"); ok {
			t.Error("lookup status shown without a lookup")
		}
	})
}

// unusedDebugStore fails the test if a debug panel query runs.
func unusedDebugStore(t *testing.T) *fakeDebugStore {
	return &fakeDebugStore{
		siteDebugFn: func(context.Context, string) (rss.SiteDebug, error) {
			t.Error("SiteDebug called with debug mode off")
			return rss.SiteDebug{}, nil
		},
		feedDebugFn: func(context.Context, []string) (map[string]rss.FeedDebug, error) {
			t.Error("FeedDebug called with debug mode off")
			return nil, nil
		},
	}
}

func withRouteID(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestSiteHandlerDebugPanel(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/", Title: "Example Site"}
	feeds := []rss.Feed{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Title: "First", Url: "https://example.com/a.xml", ConsecutiveFailures: 2}}
	lookup := rss.Lookup{ID: "l1", Status: rss.LookupFailed, Error: "<script>alert(1)</script>"}
	serve := func(t *testing.T, cfg Config, debugOn bool, store DebugStore) string {
		t.Helper()
		s := siteServerWithLookup(t, site, nil, feeds, nil, lookup, nil)
		s.config.DebugEmails = cfg.DebugEmails
		s.services.DebugService = store
		req := withRouteID(httptest.NewRequest(http.MethodGet, "/sites/"+testSiteID, nil), testSiteID)
		rr, _, _ := serveDebug(t, s, s.siteHandler, req, &debugSession, debugOn)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}

	t.Run("on", func(t *testing.T) {
		var gotSite string
		var gotFeeds []string
		body := serve(t, debugConfig, true, &fakeDebugStore{
			siteDebugFn: func(_ context.Context, id string) (rss.SiteDebug, error) {
				gotSite = id
				return rss.SiteDebug{}, nil
			},
			feedDebugFn: func(_ context.Context, ids []string) (map[string]rss.FeedDebug, error) {
				gotFeeds = ids
				return map[string]rss.FeedDebug{feeds[0].ID: {Articles: 4}}, nil
			},
		})
		if gotSite != testSiteID || !slices.Equal(gotFeeds, []string{feeds[0].ID}) {
			t.Errorf("SiteDebug(%q), FeedDebug(%v); want the site and its feeds", gotSite, gotFeeds)
		}
		assertBodyContains(t, body,
			`<details class="debug">`,
			"<summary>Debug: site</summary>",
			"<dt>icon</dt><dd>none (letter avatar)</dd>",
			"<dt>First</dt><dd>failing (2 in a row) · never attempted · 4 articles</dd>",
			"&lt;script&gt;alert(1)&lt;/script&gt;",
			`<a class="debug-badge" href="/settings#debug">Debug</a>`,
		)
		if strings.Contains(body, "<script>alert(1)") {
			t.Error("lookup error is not escaped")
		}
	})

	t.Run("off", func(t *testing.T) {
		body := serve(t, debugConfig, false, unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup with debug mode off: %s", body)
		}
	})

	t.Run("not allowed", func(t *testing.T) {
		body := serve(t, Config{}, true, unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup for a user who isn't allowed: %s", body)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := siteServerWithLookup(t, site, nil, feeds, nil, lookup, nil)
		s.config.DebugEmails = debugConfig.DebugEmails
		s.services.DebugService = &fakeDebugStore{siteDebugFn: func(context.Context, string) (rss.SiteDebug, error) {
			return rss.SiteDebug{}, context.DeadlineExceeded
		}}
		req := withRouteID(httptest.NewRequest(http.MethodGet, "/sites/"+testSiteID, nil), testSiteID)
		rr, _, _ := serveDebug(t, s, s.siteHandler, req, &debugSession, true)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestFeedHandlerDebugPanel(t *testing.T) {
	serve := func(t *testing.T, cfg Config, debugOn bool, store DebugStore) string {
		t.Helper()
		s := feedServer(t, nil)
		s.services.FeedService = &fakeFeedStore{getByIDFn: func(_ context.Context, id string) (rss.Feed, error) {
			return rss.Feed{ID: id, SiteID: testSiteID, Url: "https://example.com/feed.xml", Title: "Example Feed Title",
				LastError: "<script>alert(1)</script>", ConsecutiveFailures: 1, NextFetch: time.Now().Add(time.Hour)}, nil
		}}
		s.config.DebugEmails = cfg.DebugEmails
		s.services.DebugService = store
		req := withRouteID(httptest.NewRequest(http.MethodGet, "/feeds/"+testFeedID, nil), testFeedID)
		rr, _, _ := serveDebug(t, s, s.feedHandler, req, &debugSession, debugOn)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}

	t.Run("on", func(t *testing.T) {
		var got []string
		body := serve(t, debugConfig, true, &fakeDebugStore{feedDebugFn: func(_ context.Context, ids []string) (map[string]rss.FeedDebug, error) {
			got = ids
			return map[string]rss.FeedDebug{testFeedID: {Articles: 12, EnrichJobs: 3}}, nil
		}})
		if !slices.Equal(got, []string{testFeedID}) {
			t.Errorf("FeedDebug(%v), want [%s]", got, testFeedID)
		}
		assertBodyContains(t, body,
			`<details class="debug">`,
			"<summary>Debug: feed</summary>",
			"<dt>status</dt><dd>failing (1 in a row)",
			"<dt>articles</dt><dd>12</dd>",
			"<dt>enrich jobs</dt><dd>3",
			`<dt>last_error</dt><dd>&lt;script&gt;alert(1)&lt;/script&gt; <span class="debug-note">internal; only debug mode shows it</span></dd>`,
			"<dt>etag</dt><dd>—</dd>",
		)
		if strings.Contains(body, "<script>alert(1)") {
			t.Error("last_error is not escaped")
		}
	})

	t.Run("off", func(t *testing.T) {
		body := serve(t, debugConfig, false, unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup with debug mode off: %s", body)
		}
	})

	t.Run("not allowed", func(t *testing.T) {
		body := serve(t, Config{}, true, unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup for a user who isn't allowed: %s", body)
		}
	})
}

func TestSettingsHandlerDebug(t *testing.T) {
	serve := func(t *testing.T, cfg Config, debugOn bool) string {
		t.Helper()
		s := newTestServerConfig(t, cfg, Services{})
		rr, _, _ := serveDebug(t, s, s.settingsHandler, httptest.NewRequest(http.MethodGet, "/settings", nil), &debugSession, debugOn)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}

	t.Run("allowed, off", func(t *testing.T) {
		body := serve(t, debugConfig, false)
		assertBodyContains(t, body,
			`<section id="debug">`,
			`<form class="settings-debug" action="/settings/debug" method="post">`,
			`<input type="hidden" name="on" value="1">`,
			"Turn on debug mode",
		)
		if strings.Contains(body, "debug-badge") {
			t.Error("nav shows the debug badge with debug mode off")
		}
	})

	t.Run("allowed, on", func(t *testing.T) {
		body := serve(t, debugConfig, true)
		assertBodyContains(t, body,
			`<input type="hidden" name="on" value="0">`,
			"Turn off debug mode",
			`<a class="debug-badge" href="/settings#debug">Debug</a>`,
		)
	})

	t.Run("not allowed", func(t *testing.T) {
		body := serve(t, Config{Port: 4000, Env: "development", DebugEmails: []string{"someone@example.com"}}, true)
		if strings.Contains(body, `id="debug"`) || strings.Contains(body, "/settings/debug") || strings.Contains(body, "debug-badge") {
			t.Errorf("settings shows debug mode to a user who isn't allowed: %s", body)
		}
	})
}
