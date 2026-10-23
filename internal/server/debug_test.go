package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
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
			`<form class="debug-inspect" action="/debug/feeds/`+testFeedID+`/inspect" method="post">`,
			`<input type="checkbox" name="conditional" value="1"> Send the stored ETag and Last-Modified`,
			"Fetch and inspect</button>",
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

func TestRequireDebug(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		debugOn bool
		want    bool
	}{
		{"on", debugConfig, true, true},
		{"off", debugConfig, false, false},
		{"not allowed", Config{Port: 4000, Env: "development", DebugEmails: []string{"someone@example.com"}}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, tt.cfg, Services{})
			ran := false
			h := s.requireDebug(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))
			rr, _, _ := serveDebug(t, s, h.ServeHTTP, httptest.NewRequest(http.MethodPost, "/debug/feeds/"+testFeedID+"/inspect", nil), &debugSession, tt.debugOn)
			if ran != tt.want {
				t.Errorf("handler ran = %v, want %v", ran, tt.want)
			}
			if !tt.want {
				assertNotFoundHTML(t, rr)
			}
		})
	}

	t.Run("anonymous, through the router", func(t *testing.T) {
		s := newTestServerConfig(t, debugConfig, Services{})
		req := httptest.NewRequest(http.MethodPost, "/debug/feeds/"+testFeedID+"/inspect", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), "/login") {
			t.Errorf("got %d %q, want a redirect to /login", rr.Code, rr.Header().Get("Location"))
		}
	})
}

func TestCompareArticle(t *testing.T) {
	base := rss.Article{
		URL: "https://example.com/a", CanonicalURL: "https://example.com/a", ImageURL: "https://example.com/a.jpg",
		MediaType: "audio/mpeg", Title: "A", Summary: "<p>s</p>", Content: "<p>c</p>", Excerpt: "s",
		PublishedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	tests := []struct {
		name   string
		change func(a *rss.Article)
		want   []string
	}{
		{"equal", func(*rss.Article) {}, nil},
		{"url", func(a *rss.Article) { a.URL = "https://example.com/b" }, []string{"url"}},
		{"canonical_url", func(a *rss.Article) { a.CanonicalURL = "" }, []string{"canonical_url"}},
		{"image_url", func(a *rss.Article) { a.ImageURL = "" }, []string{"image_url"}},
		{"media_type", func(a *rss.Article) { a.MediaType = "video/mp4" }, []string{"media_type"}},
		{"title", func(a *rss.Article) { a.Title = "B" }, []string{"title"}},
		{"summary", func(a *rss.Article) { a.Summary = "<p>longer</p>" }, []string{"summary (13 → 8 bytes)"}},
		{"content", func(a *rss.Article) { a.Content = "" }, []string{"content (0 → 8 bytes)"}},
		{"excerpt", func(a *rss.Article) { a.Excerpt = "x" }, []string{"excerpt"}},
		{"published_at", func(a *rss.Article) { a.PublishedAt = a.PublishedAt.Add(time.Hour) }, []string{"published_at"}},
		{"published_at in another zone", func(a *rss.Article) { a.PublishedAt = a.PublishedAt.In(time.FixedZone("X", 3600)) }, nil},
		{"published_date_only", func(a *rss.Article) { a.PublishedDateOnly = true }, []string{"published_date_only"}},
		{"several, in column order", func(a *rss.Article) { a.Excerpt = "x"; a.URL = "u" }, []string{"url", "excerpt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := base
			tt.change(&stored)
			if got := compareArticle(base, stored); !slices.Equal(got, tt.want) {
				t.Errorf("compareArticle = %q, want %q", got, tt.want)
			}
		})
	}
}

// inspectRSS is an RSS feed for the feed inspector tests: items "n" (new),
// "u" (unchanged), "d" (whose stored title differs), a second "u", and "x"
// with a script in its description.
const inspectRSS = `<?xml version="1.0"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Inspected</title>
    <link>https://example.com/</link>
    <item><guid>n</guid><title>New post</title><dc:creator>Ada</dc:creator></item>
    <item><guid>u</guid><title>Same post</title><description>Same</description></item>
    <item><guid>d</guid><title>Fetched title</title></item>
    <item><guid>u</guid><title>Same again</title></item>
    <item><guid>x</guid><title>Script</title><description><![CDATA[<script>alert(1)</script>]]></description></item>
  </channel>
</rss>`

// inspectStored are the stored articles of inspectRSS: "u" and "x" as a
// refresh would store them, and "d" with another title.
func inspectStored(t *testing.T) map[string]rss.Article {
	t.Helper()
	parsed, err := feedparse.Parse(mustURL(t, "https://example.com/feed.xml"), []byte(inspectRSS))
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]rss.Article{}
	for _, a := range ingest.Articles(parsed.Items) {
		switch a.ExternalID {
		case "u", "x":
			stored[a.ExternalID] = a
		case "d":
			a.Title = "Stored title"
			stored[a.ExternalID] = a
		}
	}
	return stored
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// inspectServer is a server for the feed inspector with fetcher, the stored
// articles of inspectStored, and stores that fail the test if they write.
func inspectServer(t *testing.T, fetcher *fakeFetcher) (*Server, *fakeRefresher) {
	t.Helper()
	stored := inspectStored(t)
	refresher := &fakeRefresher{refreshFn: func(context.Context, rss.Feed) (rss.FetchResult, error) {
		t.Error("Refresh called")
		return rss.FetchResult{}, nil
	}}
	s := newTestServerConfig(t, debugConfig, Services{
		FeedService: &fakeFeedStore{
			getByIDFn: func(_ context.Context, id string) (rss.Feed, error) {
				if id != testFeedID {
					return rss.Feed{}, rss.ErrNoRecord
				}
				return rss.Feed{ID: id, SiteID: testSiteID, Url: "https://example.com/feed.xml", Title: "Stored feed title",
					ETag: `"v1"`, LastModified: "Mon, 05 Oct 2026 09:30:00 GMT"}, nil
			},
			requestRefreshFn: func(context.Context, string) error {
				t.Error("RequestRefresh called")
				return nil
			},
		},
		DebugService: &fakeDebugStore{articlesByExternalIDFn: func(_ context.Context, feedID string, ids []string) (map[string]rss.Article, error) {
			if feedID != testFeedID {
				t.Errorf("ArticlesByExternalID(%q), want the feed's ID", feedID)
			}
			out := map[string]rss.Article{}
			for _, id := range ids {
				if a, ok := stored[id]; ok {
					out[id] = a
				}
			}
			return out, nil
		}},
		Refresher: refresher,
		Fetcher:   fetcher,
	})
	return s, refresher
}

// rssResponse is a 200 response with body.
func rssResponse(t *testing.T, body string) *fakeFetcher {
	return &fakeFetcher{resp: &fetch.Response{
		URL:        mustURL(t, "https://example.com/feed.xml"),
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/rss+xml"}, "Etag": {`"v2"`}},
		Body:       []byte(body),
	}}
}

// serveInspect posts form to the feed inspector of feed id and returns the
// response.
func serveInspect(t *testing.T, s *Server, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/debug/feeds/"+id+"/inspect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr, _, _ := serveDebug(t, s, s.feedInspectHandler, withRouteID(req, id), &debugSession, true)
	return rr
}

func TestFeedInspectHandler(t *testing.T) {
	t.Run("rss", func(t *testing.T) {
		fetcher := rssResponse(t, inspectRSS)
		s, refresher := inspectServer(t, fetcher)
		rr := serveInspect(t, s, testFeedID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<title>rss - inspect feed</title>",
			"<h1>Inspect feed</h1>",
			`<a href="/feeds/`+testFeedID+`">Back to the feed</a>`,
			"<dt>conditional headers</dt><dd>none</dd>",
			"<dt>status</dt><dd>200 OK</dd>",
			"<dt>Sniff</dt><dd>feed</dd>",
			`<dt>Etag</dt><dd>&#34;v2&#34;</dd>`,
			"<th scope=\"row\">title</th><td>Inspected</td><td>Stored feed title</td>",
			"Item fields",
			`<tr class="debug-ignored"><th scope="row">dc:creator</th><td>1/5</td><td>ignored</td><td>Ada</td></tr>`,
			`<th scope="row">guid</th><td>5/5</td><td>used</td>`,
			"<summary>new · New post</summary>",
			"<summary>unchanged · Same post</summary>",
			"<summary>differs: title · Fetched title</summary>",
			"<dt>title</dt><dd>Stored title</dd>",
			"<summary>dropped: duplicate of an earlier item · Same again</summary>",
			"<dt>external_id</dt><dd>n</dd>",
		)
		if strings.Contains(body, "This article's item") {
			t.Error("shows an article's item without item")
		}
		if len(fetcher.urls) != 1 || fetcher.urls[0] != "https://example.com/feed.xml" {
			t.Errorf("fetched %v, want the feed's URL once", fetcher.urls)
		}
		if len(refresher.calls) != 0 {
			t.Errorf("Refresh called %d times", len(refresher.calls))
		}
	})

	t.Run("raw items are escaped", func(t *testing.T) {
		s, _ := inspectServer(t, rssResponse(t, inspectRSS))
		body := serveInspect(t, s, testFeedID, nil).Body.String()
		if strings.Contains(body, "<script>alert(1)") {
			t.Error("the item's script is not escaped")
		}
		assertBodyContains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;item&gt;&lt;guid&gt;x&lt;/guid&gt;")
	})

	t.Run("item first", func(t *testing.T) {
		s, _ := inspectServer(t, rssResponse(t, inspectRSS))
		body := serveInspect(t, s, testFeedID, url.Values{"item": {"d"}}).Body.String()
		focus := strings.Index(body, "This article's item")
		items := strings.Index(body, ">Items</h2>")
		item := strings.Index(body, `<details class="inspect-item" open>
  <summary>differs: title · Fetched title</summary>`)
		if focus < 0 || items < 0 || item < 0 || !(focus < item && item < items) {
			t.Errorf("item d is not shown open under This article's item, before Items (at %d, %d, %d): %s", focus, item, items, body)
		}
		if n := strings.Count(body, "differs: title · Fetched title"); n != 1 {
			t.Errorf("item d is shown %d times, want once", n)
		}
	})

	t.Run("conditional", func(t *testing.T) {
		fetcher := rssResponse(t, inspectRSS)
		s, _ := inspectServer(t, fetcher)
		body := serveInspect(t, s, testFeedID, url.Values{"conditional": {"1"}}).Body.String()
		h := fetcher.headers[0]
		if h.Get("If-None-Match") != `"v1"` || h.Get("If-Modified-Since") != "Mon, 05 Oct 2026 09:30:00 GMT" {
			t.Errorf("sent %v, want the stored validators", h)
		}
		assertBodyContains(t, body, "<dt>If-None-Match</dt><dd>&#34;v1&#34;</dd>")
	})

	t.Run("not conditional", func(t *testing.T) {
		fetcher := rssResponse(t, inspectRSS)
		s, _ := inspectServer(t, fetcher)
		serveInspect(t, s, testFeedID, nil)
		if h := fetcher.headers[0]; len(h) != 0 {
			t.Errorf("sent %v, want no headers", h)
		}
	})

	t.Run("304", func(t *testing.T) {
		s, _ := inspectServer(t, &fakeFetcher{resp: &fetch.Response{URL: mustURL(t, "https://example.com/feed.xml"), StatusCode: http.StatusNotModified, Header: http.Header{}}})
		rr := serveInspect(t, s, testFeedID, url.Values{"conditional": {"1"}})
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(), `<p class="fetch-error" role="alert">304 Not Modified</p>`)
		if strings.Contains(rr.Body.String(), "Item fields") {
			t.Error("shows item fields without a body")
		}
	})

	t.Run("fetch error", func(t *testing.T) {
		s, _ := inspectServer(t, &fakeFetcher{err: errors.New("dial tcp: connection refused")})
		rr := serveInspect(t, s, testFeedID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(), "Fetch failed: dial tcp: connection refused")
	})

	t.Run("not a feed", func(t *testing.T) {
		fetcher := rssResponse(t, "<!doctype html><html><body><p>Hello</p></body></html>")
		fetcher.resp.Header.Set("Content-Type", "text/html")
		s, _ := inspectServer(t, fetcher)
		rr := serveInspect(t, s, testFeedID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(), "Not a feed: ", "<dt>Sniff</dt><dd>not a feed</dd>", "&lt;p&gt;Hello&lt;/p&gt;")
	})

	t.Run("large body", func(t *testing.T) {
		big := `<rss version="2.0"><channel><title>Big</title><!-- ` + strings.Repeat("é", inspectBodyMax) + ` --></channel></rss>`
		s, _ := inspectServer(t, rssResponse(t, big))
		body := serveInspect(t, s, testFeedID, nil).Body.String()
		assertBodyContains(t, body, "Showing the first 256 KiB of "+debugBytes(len(big))+".")
		if strings.Contains(body, "</channel>") || strings.Contains(body, "&lt;/channel&gt;") {
			t.Error("the body is not cut")
		}
		if !utf8.ValidString(body) {
			t.Error("the body is not cut on a UTF-8 boundary")
		}
	})

	t.Run("unknown feed", func(t *testing.T) {
		fetcher := rssResponse(t, inspectRSS)
		s, _ := inspectServer(t, fetcher)
		assertNotFoundHTML(t, serveInspect(t, s, "99999999-9999-4999-8999-999999999999", nil))
		if len(fetcher.urls) != 0 {
			t.Errorf("fetched %v for an unknown feed", fetcher.urls)
		}
	})
}

// TestExtendWriteDeadline posts to the feed inspector through the router,
// logged in with debug mode on, on a server whose WriteTimeout is shorter
// than the fetch: the page still arrives.
func TestExtendWriteDeadline(t *testing.T) {
	var logs bytes.Buffer
	fetcher := &slowFetcher{delay: 300 * time.Millisecond, fakeFetcher: rssResponse(t, inspectRSS)}
	s, _ := inspectServer(t, nil)
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	s.services.Fetcher = fetcher
	s.services.UserService = &fakeUserStore{authenticateFn: func(context.Context, string, string) (string, error) {
		return debugSession.User.ID, nil
	}}
	s.services.SessionService = &fakeSessionStore{
		createFn: func(context.Context, string, time.Time) (string, error) { return testSessionID, nil },
		getFn: func(context.Context, string) (rss.Session, error) {
			return rss.Session{ID: testSessionID, User: debugSession.User, LastSeenAt: time.Now()}, nil
		},
	}

	ts := httptest.NewUnstartedServer(s.router())
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	post := func(target string, form url.Values) *http.Response {
		t.Helper()
		resp, err := c.PostForm(ts.URL+target, form)
		if err != nil {
			t.Fatalf("POST %s: %v", target, err)
		}
		return resp
	}

	post("/login", url.Values{"email": {debugSession.User.Email}, "password": {"correct-horse-battery"}}).Body.Close()
	post("/settings/debug", url.Values{"on": {"1"}}).Body.Close()
	resp := post("/debug/feeds/"+testFeedID+"/inspect", nil)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the page: %v (the write deadline wasn't extended?)", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "Item fields") {
		t.Errorf("got %d, want the inspector page: %s", resp.StatusCode, b)
	}
	if strings.Contains(logs.String(), "extend write deadline") {
		t.Errorf("extendWriteDeadline failed: %s", logs.String())
	}
}

// slowFetcher is a fakeFetcher that takes delay to answer.
type slowFetcher struct {
	*fakeFetcher
	delay time.Duration
}

func (f *slowFetcher) Get(ctx context.Context, rawURL string, header http.Header) (*fetch.Response, error) {
	time.Sleep(f.delay)
	return f.fakeFetcher.Get(ctx, rawURL, header)
}
