package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"github.com/grodier/rss/internal/discovery"
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
		articleDebugFn: func(context.Context, string, string) (rss.ArticleDebug, error) {
			t.Error("ArticleDebug called with debug mode off")
			return rss.ArticleDebug{}, nil
		},
		articlesByExternalIDFn: func(context.Context, string, []string) (map[string]rss.Article, error) {
			t.Error("ArticlesByExternalID called with debug mode off")
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
			`<form class="debug-inspect" action="/debug/sites/`+testSiteID+`/discover" method="post">`,
			"Run discovery</button>",
			"Runs a lookup of this site now without saving anything.",
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
}

func TestCompareArticle(t *testing.T) {
	base := rss.Article{
		URL: "https://example.com/a", CanonicalURL: "https://example.com/a", ImageURL: "https://example.com/a.jpg",
		MediaType: "audio/mpeg", Authors: []string{"Ada"}, Title: "A", Summary: "<p>s</p>", Content: "<p>c</p>", Excerpt: "s",
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
		{"authors", func(a *rss.Article) { a.Authors = []string{"Ada", "Grace"} }, []string{"authors"}},
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

	// The database returns no authors as an empty list, the parser as nil.
	fetched, stored := base, base
	fetched.Authors, stored.Authors = nil, []string{}
	if got := compareArticle(fetched, stored); got != nil {
		t.Errorf("compareArticle with nil and empty authors = %q, want none", got)
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
    <item><guid>n</guid><title>New post</title><dc:subject>Notes</dc:subject></item>
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
			"<title>Paper Round - inspect feed</title>",
			"<h1>Inspect feed</h1>",
			`<a href="/feeds/`+testFeedID+`">Back to the feed</a>`,
			"<dt>conditional headers</dt><dd>none</dd>",
			"<dt>status</dt><dd>200 OK</dd>",
			"<dt>Sniff</dt><dd>feed</dd>",
			`<dt>Etag</dt><dd>&#34;v2&#34;</dd>`,
			"<th scope=\"row\">title</th><td>Inspected</td><td>Stored feed title</td>",
			"Item fields",
			`<tr class="debug-ignored"><th scope="row">dc:subject</th><td>1/5</td><td>ignored</td><td>Notes</td></tr>`,
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

func TestArticleProvenance(t *testing.T) {
	published := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		a    rss.Article
		want provenance
	}{
		{
			name: "all from the feed, backlog, guid",
			a: rss.Article{Title: "T", PageTitle: "PT", Excerpt: "E", PageDescription: "PD", ImageURL: "https://example.com/a.png", PageImageURL: "https://example.com/b.png",
				ExternalID: "tag:example.com,2026:1", URL: "https://example.com/1", PublishedAt: published, TimelineAt: published, CreatedAt: published.Add(48 * time.Hour)},
			want: provenance{Title: "feed", Excerpt: "feed", Image: "feed", Timeline: "backlog", ExternalID: "guid"},
		},
		{
			name: "news at its published date",
			a:    rss.Article{Title: "T", ExternalID: "x", PublishedAt: published, TimelineAt: published, CreatedAt: published.Add(30 * time.Minute)},
			want: provenance{Title: "feed", Excerpt: "none", Image: "none", Timeline: "news", ExternalID: "guid"},
		},
		{
			name: "news exactly NewsWindow below its arrival",
			a:    rss.Article{Title: "T", ExternalID: "x", PublishedAt: published, TimelineAt: published, CreatedAt: published.Add(rss.NewsWindow)},
			want: provenance{Title: "feed", Excerpt: "none", Image: "none", Timeline: "news", ExternalID: "guid"},
		},
		{
			name: "all from the page, news (undated), link",
			a: rss.Article{PageTitle: "PT", PageDescription: "PD", PageImageURL: "https://example.com/b.png",
				ExternalID: "https://example.com/1", URL: "https://example.com/1", TimelineAt: published, CreatedAt: published},
			want: provenance{Title: "page", Excerpt: "page", Image: "page", Timeline: "news", ExternalID: "link"},
		},
		{
			name: "title from the excerpt, news (dated but spread later), hash",
			a:    rss.Article{Excerpt: "E", ExternalID: "sha256:abc", PublishedAt: published, TimelineAt: published.Add(time.Hour), CreatedAt: published.Add(2 * time.Hour)},
			want: provenance{Title: "excerpt", Excerpt: "feed", Image: "none", Timeline: "news", ExternalID: "hash"},
		},
		{
			name: "title from the page description",
			a:    rss.Article{PageDescription: "PD", ExternalID: "x", TimelineAt: published},
			want: provenance{Title: "excerpt", Excerpt: "page", Image: "none", Timeline: "news", ExternalID: "guid"},
		},
		{
			name: "nothing",
			a:    rss.Article{ExternalID: "x", TimelineAt: published},
			want: provenance{Title: "none", Excerpt: "none", Image: "none", Timeline: "news", ExternalID: "guid"},
		},
		{
			name: "empty URL is not a link",
			a:    rss.Article{ExternalID: "id-1", TimelineAt: published},
			want: provenance{Title: "none", Excerpt: "none", Image: "none", Timeline: "news", ExternalID: "guid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := articleProvenance(tt.a); got != tt.want {
				t.Errorf("articleProvenance = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestArticleDebugFields(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	t.Run("summary and content as lengths, excerpt from summary", func(t *testing.T) {
		a := rss.Article{Summary: "<p>Hello</p>", Content: "<p>Hello, world</p>", Excerpt: "Hello", TimelineAt: now}
		fields := articleDebugFields(a, rss.ArticleDebug{}, now)
		if got, _ := fieldValue(fields, "summary"); got != "12 bytes" {
			t.Errorf("summary = %q, want 12 bytes", got)
		}
		if got, _ := fieldValue(fields, "content"); got != "19 bytes" {
			t.Errorf("content = %q, want 19 bytes", got)
		}
		if got := fieldNote(fields, "shown excerpt"); got != "from: feed; excerpt from: summary" {
			t.Errorf("shown excerpt note = %q", got)
		}
	})

	t.Run("excerpt from content", func(t *testing.T) {
		a := rss.Article{Summary: "<img src=x>", Content: "<p>Hello</p>", Excerpt: "Hello", TimelineAt: now}
		fields := articleDebugFields(a, rss.ArticleDebug{}, now)
		if got := fieldNote(fields, "shown excerpt"); got != "from: feed; excerpt from: content" {
			t.Errorf("shown excerpt note = %q", got)
		}
	})

	t.Run("no enrich job", func(t *testing.T) {
		fields := articleDebugFields(rss.Article{TimelineAt: now}, rss.ArticleDebug{}, now)
		if got, _ := fieldValue(fields, "enrich job"); got != "none" {
			t.Errorf("enrich job = %q, want none", got)
		}
		if got := fieldNote(fields, "enrich job"); got != noEnrichJobNote {
			t.Errorf("enrich job note = %q", got)
		}
		if _, ok := fieldValue(fields, "enrich attempts"); ok {
			t.Error("enrich attempts shown without a job")
		}
	})

	t.Run("enrich job", func(t *testing.T) {
		d := rss.ArticleDebug{EnrichJob: rss.EnrichJobState{Exists: true, Priority: 1, Attempts: 2, LastError: "timeout", NextAttemptAt: now, CreatedAt: now}}
		fields := articleDebugFields(rss.Article{TimelineAt: now}, d, now)
		if got, _ := fieldValue(fields, "enrich attempts"); got != "2" {
			t.Errorf("enrich attempts = %q, want 2", got)
		}
		if got := fieldNote(fields, "enrich last_error"); got != internalNote {
			t.Errorf("enrich last_error note = %q", got)
		}
		if _, ok := fieldValue(fields, "enrich job"); ok {
			t.Error(`"enrich job: none" shown with a job`)
		}
	})
}

func fieldNote(fields []debugField, name string) string {
	for _, f := range fields {
		if f.Name == name {
			return f.Note
		}
	}
	return ""
}

func TestNewArticleRowDebug(t *testing.T) {
	timeline := time.Date(2026, 3, 2, 9, 5, 30, 0, time.UTC)
	t.Run("dated", func(t *testing.T) {
		got := newArticleRowDebug(rss.Article{ID: testArticleID, PageTitle: "PT", ImageURL: "https://example.com/a.png",
			PublishedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC), TimelineAt: timeline})
		want := articleRowDebug{ShortID: "22222222", Timeline: "news", TimelineAt: "2026-03-02 09:05Z", PublishedAt: "2026-03-01 08:00Z", Title: "page", Image: "feed"}
		if *got != want {
			t.Errorf("newArticleRowDebug = %+v, want %+v", *got, want)
		}
	})
	t.Run("date only", func(t *testing.T) {
		got := newArticleRowDebug(rss.Article{ID: testArticleID, PublishedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), PublishedDateOnly: true, TimelineAt: timeline})
		if got.PublishedAt != "2026-03-01" {
			t.Errorf("PublishedAt = %q, want 2026-03-01", got.PublishedAt)
		}
	})
	t.Run("undated", func(t *testing.T) {
		got := newArticleRowDebug(rss.Article{ID: testArticleID, TimelineAt: timeline})
		if got.PublishedAt != "" {
			t.Errorf("PublishedAt = %q, want empty", got.PublishedAt)
		}
	})
}

func TestArticleHandlerDebugPanel(t *testing.T) {
	article := rss.Article{
		ExternalID: "tag:example.com,2026:<1>",
		URL:        "https://example.com/1",
		Title:      "Title",
		Summary:    "<p>Short</p>",
		Content:    "<p>Body</p><script>alert(1)</script>",
		TimelineAt: time.Now(),
	}
	serve := func(t *testing.T, cfg Config, debugOn bool, target string, store DebugStore) string {
		t.Helper()
		s := articleServer(t, article, nil, nil, nil)
		s.config.DebugEmails = cfg.DebugEmails
		s.services.DebugService = store
		req := withRouteID(httptest.NewRequest(http.MethodGet, target, nil), testArticleID)
		rr, _, _ := serveDebug(t, s, s.articleHandler, req, &debugSession, debugOn)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}
	copies := &fakeDebugStore{articleDebugFn: func(_ context.Context, userID, id string) (rss.ArticleDebug, error) {
		if userID != debugSession.User.ID || id != testArticleID {
			t.Errorf("ArticleDebug(%q, %q), want the user and the article", userID, id)
		}
		return rss.ArticleDebug{Copies: []rss.ArticleCopy{{ArticleID: "33333333-3333-4333-8333-333333333333", FeedTitle: "Other Feed",
			CreatedAt: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC), TimelineAt: time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC)}}}, nil
	}}

	t.Run("on", func(t *testing.T) {
		body := serve(t, debugConfig, true, "/articles/"+testArticleID, copies)
		assertBodyContains(t, body,
			`<details class="debug" id="debug">`,
			"<summary>Debug: article</summary>",
			"<dt>summary</dt><dd>12 bytes",
			"<dt>shown title</dt><dd>Title <span class=\"debug-note\">from: feed</span>",
			"<dt>enrich job</dt><dd>none",
			`<a href="/articles/33333333-3333-4333-8333-333333333333">Other Feed · 2026-01-02 03:04Z · 2026-01-02 03:05Z</a>`,
			"&lt;p&gt;Body&lt;/p&gt;&lt;script&gt;alert(1)&lt;/script&gt;",
			`<form class="debug-inspect" action="/debug/feeds/`+testFeedID+`/inspect" method="post">`,
			`<input type="hidden" name="item" value="tag:example.com,2026:&lt;1&gt;">`,
			"Source item</button>",
			`<form class="debug-inspect" action="/debug/articles/`+testArticleID+`/inspect-page" method="post">`,
			"Inspect page</button>",
		)
		if strings.Contains(body, "<script>alert") {
			t.Error("raw content is not escaped")
		}
	})

	t.Run("no URL, no Inspect page", func(t *testing.T) {
		s := articleServer(t, rss.Article{Title: "No link", TimelineAt: time.Now()}, nil, nil, nil)
		s.config.DebugEmails = debugConfig.DebugEmails
		s.services.DebugService = copies
		req := withRouteID(httptest.NewRequest(http.MethodGet, "/articles/"+testArticleID, nil), testArticleID)
		rr, _, _ := serveDebug(t, s, s.articleHandler, req, &debugSession, true)
		if body := rr.Body.String(); strings.Contains(body, "inspect-page") {
			t.Errorf("shows Inspect page for an article without a URL: %s", body)
		}
	})

	t.Run("debug=1 opens the panel", func(t *testing.T) {
		body := serve(t, debugConfig, true, "/articles/"+testArticleID+"?debug=1", copies)
		assertBodyContains(t, body, `<details class="debug" id="debug" open>`)
	})

	t.Run("no copies", func(t *testing.T) {
		body := serve(t, debugConfig, true, "/articles/"+testArticleID, &fakeDebugStore{articleDebugFn: func(context.Context, string, string) (rss.ArticleDebug, error) {
			return rss.ArticleDebug{}, nil
		}})
		assertBodyContains(t, body, "No copies in other feeds.")
	})

	t.Run("off", func(t *testing.T) {
		body := serve(t, debugConfig, false, "/articles/"+testArticleID+"?debug=1", unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup with debug mode off: %s", body)
		}
	})

	t.Run("not allowed", func(t *testing.T) {
		body := serve(t, Config{}, true, "/articles/"+testArticleID, unusedDebugStore(t))
		if strings.Contains(body, `class="debug`) {
			t.Errorf("body has debug markup for a user who isn't allowed: %s", body)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := articleServer(t, article, nil, nil, nil)
		s.config.DebugEmails = debugConfig.DebugEmails
		s.services.DebugService = &fakeDebugStore{articleDebugFn: func(context.Context, string, string) (rss.ArticleDebug, error) {
			return rss.ArticleDebug{}, context.DeadlineExceeded
		}}
		req := withRouteID(httptest.NewRequest(http.MethodGet, "/articles/"+testArticleID, nil), testArticleID)
		rr, _, _ := serveDebug(t, s, s.articleHandler, req, &debugSession, true)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestNewArticleDebugViewLeadImage(t *testing.T) {
	now := time.Now()
	a := rss.Article{ImageURL: "https://example.com/a.png", Summary: strings.Repeat("x", articleRawMax+10)}
	v := newArticleDebugView(a, rss.ArticleDebug{}, "<p>x</p>", "", now)
	if got, _ := fieldValue(v.Page, "lead image"); got != "none (the body already shows the card image)" {
		t.Errorf("lead image = %q", got)
	}
	if got, _ := fieldValue(v.Page, "sanitized body"); got != "8 bytes" {
		t.Errorf("sanitized body = %q, want 8 bytes", got)
	}
	if len(v.RawSummary) != articleRawMax {
		t.Errorf("len(RawSummary) = %d, want %d", len(v.RawSummary), articleRawMax)
	}
	v = newArticleDebugView(a, rss.ArticleDebug{}, "", a.ImageURL, now)
	if got, _ := fieldValue(v.Page, "lead image"); got != a.ImageURL {
		t.Errorf("lead image = %q, want %q", got, a.ImageURL)
	}
	v = newArticleDebugView(rss.Article{}, rss.ArticleDebug{}, "", "", now)
	if got, _ := fieldValue(v.Page, "lead image"); got != "" {
		t.Errorf("lead image = %q, want empty", got)
	}
}

// TestArticleRowsDebugLine checks the feed, timeline and saved pages: each
// row has a debug line only in debug mode, and the lines make no queries.
func TestArticleRowsDebugLine(t *testing.T) {
	timeline := timelineItems(2)
	saved := savedItems(2)
	feedArticles := []rss.Article{timeline[0].Article, timeline[1].Article}
	pages := []struct {
		name    string
		handler func(*Server) http.HandlerFunc
		target  string
		server  func(t *testing.T) *Server
	}{
		{"feed", func(s *Server) http.HandlerFunc { return s.feedHandler }, "/feeds/" + testFeedID, func(t *testing.T) *Server {
			return feedServerWithArticles(t, nil, &fakeArticleStore{listByFeedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.Article, error) {
				return feedArticles, nil
			}})
		}},
		{"timeline", func(s *Server) http.HandlerFunc { return s.homeHandler }, "/", func(t *testing.T) *Server {
			return newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{
				listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.TimelineAnchors, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
					return timeline, nil
				},
			}})
		}},
		{"saved", func(s *Server) http.HandlerFunc { return s.savedHandler }, "/saved", func(t *testing.T) *Server {
			return newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{
				listSavedFn: func(context.Context, string, string, rss.ArticleCursor, int) ([]rss.SavedArticle, error) {
					return saved, nil
				},
			}})
		}},
	}
	for _, p := range pages {
		for _, on := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s debug %v", p.name, on), func(t *testing.T) {
				s := p.server(t)
				s.config.DebugEmails = debugConfig.DebugEmails
				store := unusedDebugStore(t)
				req := httptest.NewRequest(http.MethodGet, p.target, nil)
				if p.name == "feed" {
					req = withRouteID(req, testFeedID)
					// The feed's own panel; not the rows.
					store.feedDebugFn = func(context.Context, []string) (map[string]rss.FeedDebug, error) { return nil, nil }
				}
				s.services.DebugService = store
				rr, _, _ := serveDebug(t, s, p.handler(s), req, &debugSession, on)
				if rr.Code != http.StatusOK {
					t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
				}
				body := rr.Body.String()
				got := strings.Count(body, `<p class="debug-line">`)
				want := 0
				if on {
					want = 2
					id := timeline[0].Article.ID
					assertBodyContains(t, body, `<p class="debug-line">`+id[:8]+` · news `, `title: feed · image: none · <a href="/articles/`+id+`?debug=1#debug">debug</a></p>`)
				}
				if got != want {
					t.Errorf("%d debug lines, want %d", got, want)
				}
			})
		}
	}
}

// inspectPageHTML is an article's web page for the page inspector tests.
const inspectPageHTML = `<!doctype html><html><head>
<title>Fetched page</title>
<meta property="og:title" content="OG title">
<meta name="description" content="Fetched   description">
<meta property="og:image" content="https://example.com/lead.jpg">
<meta property="article:published_time" content="2026-10-20T08:00:00Z">
<script type="application/ld+json">{"@type": "NewsArticle", "headline": "</script><script>alert(1)</script>"}</script>
</head><body></body></html>`

// htmlResponse is a 200 HTML response with body from url.
func htmlResponse(t *testing.T, rawURL, body string) *fakeFetcher {
	return &fakeFetcher{resp: &fetch.Response{
		URL:        mustURL(t, rawURL),
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       []byte(body),
	}}
}

// pageInspectServer is a server for the page inspector whose article is a
// with ID testArticleID, fetching with fetcher.
func pageInspectServer(t *testing.T, a rss.Article, fetcher *fakeFetcher) *Server {
	t.Helper()
	a.ID, a.FeedID = testArticleID, testFeedID
	return newTestServerConfig(t, debugConfig, Services{
		ArticleService: &fakeArticleStore{getByIDFn: func(_ context.Context, id string) (rss.Article, error) {
			if id != testArticleID {
				return rss.Article{}, rss.ErrNoRecord
			}
			return a, nil
		}},
		Fetcher: fetcher,
	})
}

// servePost posts to target, handled by h behind requireDebug, with the
// route's id and debug mode debugOn.
func servePost(t *testing.T, s *Server, h http.HandlerFunc, target, id string, debugOn bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr, _, _ := serveDebug(t, s, s.requireDebug(h).ServeHTTP, withRouteID(req, id), &debugSession, debugOn)
	return rr
}

func TestArticlePageInspectHandler(t *testing.T) {
	article := rss.Article{URL: "https://example.com/1", Title: "An article", PageTitle: "Stored page title"}
	serve := func(t *testing.T, s *Server) string {
		t.Helper()
		rr := servePost(t, s, s.articlePageInspectHandler, "/debug/articles/"+testArticleID+"/inspect-page", testArticleID, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}

	t.Run("html", func(t *testing.T) {
		fetcher := htmlResponse(t, "https://example.com/1", inspectPageHTML)
		body := serve(t, pageInspectServer(t, article, fetcher))
		assertBodyContains(t, body,
			"<title>Paper Round - inspect page</title>",
			`<a href="/articles/`+testArticleID+`">Back to the article</a>`,
			"<dt>status</dt><dd>200 OK</dd>",
			"What enrichment would do",
			`<th scope="row">page_title</th><td>OG title</td><td>Stored page title</td>`,
			`<th scope="row">page_description</th><td>Fetched description</td><td>—</td>`,
			`<th scope="row">page_image_url</th><td>https://example.com/lead.jpg</td><td>—</td>`,
			`<tr><th scope="row">meta</th><td>og:title</td><td>OG title</td><td></td><td>used</td></tr>`,
			`<tr class="debug-ignored"><th scope="row">meta</th><td>article:published_time</td><td>2026-10-20T08:00:00Z</td><td></td><td>ignored</td></tr>`,
			"Types: none",
		)
		if len(fetcher.urls) != 1 || fetcher.urls[0] != "https://example.com/1" {
			t.Errorf("fetched %v, want the article's URL once", fetcher.urls)
		}
	})

	t.Run("JSON-LD is escaped", func(t *testing.T) {
		page := `<script type="application/ld+json">{"@type": "NewsArticle", "x": "<\/script><script>alert(1)<\/script>"}</script>` +
			`<script type="application/ld+json">{"bad": "</script><script>alert(2)</script>`
		body := serve(t, pageInspectServer(t, article, htmlResponse(t, "https://example.com/1", page)))
		assertBodyContains(t, body, "Types: NewsArticle", `&lt;\/script&gt;&lt;script&gt;alert(1)`, "JSON-LD block")
		if strings.Contains(body, "<script>alert") {
			t.Error("JSON-LD is not escaped")
		}
	})

	t.Run("not HTML", func(t *testing.T) {
		fetcher := htmlResponse(t, "https://example.com/1", "%PDF-1.7")
		fetcher.resp.Header.Set("Content-Type", "application/pdf")
		body := serve(t, pageInspectServer(t, article, fetcher))
		assertBodyContains(t, body, "Enrichment would give up: not HTML")
		if strings.Contains(body, ">Tags</h2>") {
			t.Error("shows tags for a page that isn't HTML")
		}
	})

	t.Run("404", func(t *testing.T) {
		fetcher := htmlResponse(t, "https://example.com/1", "<title>Not found</title>")
		fetcher.resp.StatusCode = http.StatusNotFound
		body := serve(t, pageInspectServer(t, article, fetcher))
		assertBodyContains(t, body, "<dt>status</dt><dd>404 Not Found</dd>", "Enrichment would retry or give up: status 404")
	})

	t.Run("fetch error", func(t *testing.T) {
		body := serve(t, pageInspectServer(t, article, &fakeFetcher{err: errors.New("dial tcp: connection refused")}))
		assertBodyContains(t, body, "Fetch failed: dial tcp: connection refused")
	})

	t.Run("no URL", func(t *testing.T) {
		fetcher := htmlResponse(t, "https://example.com/1", inspectPageHTML)
		s := pageInspectServer(t, rss.Article{Title: "No link"}, fetcher)
		assertNotFoundHTML(t, servePost(t, s, s.articlePageInspectHandler, "/debug/articles/"+testArticleID+"/inspect-page", testArticleID, true))
		if len(fetcher.urls) != 0 {
			t.Errorf("fetched %v for an article without a URL", fetcher.urls)
		}
	})

	t.Run("unknown article", func(t *testing.T) {
		const other = "99999999-9999-4999-8999-999999999999"
		s := pageInspectServer(t, article, htmlResponse(t, "https://example.com/1", inspectPageHTML))
		assertNotFoundHTML(t, servePost(t, s, s.articlePageInspectHandler, "/debug/articles/"+other+"/inspect-page", other, true))
	})
}

// siteDiscoverServer is a server for the discovery inspector of a site with
// feeds, fetching with fetcher and discovering with d.
func siteDiscoverServer(t *testing.T, feeds []rss.Feed, fetcher *fakeFetcher, d *fakeDiscoverer) *Server {
	t.Helper()
	return newTestServerConfig(t, debugConfig, Services{
		SiteService: &fakeSiteStore{getByIDFn: func(_ context.Context, id string) (rss.Site, error) {
			if id != testSiteID {
				return rss.Site{}, rss.ErrNoRecord
			}
			return rss.Site{ID: id, Host: "example.com", URL: "https://example.com/", Title: "Stored title"}, nil
		}},
		FeedService: &fakeFeedStore{listBySiteFn: func(_ context.Context, siteID string) ([]rss.Feed, error) {
			return feeds, nil
		}},
		Fetcher:    fetcher,
		Discoverer: d,
	})
}

func TestSiteDiscoverHandler(t *testing.T) {
	stored := []rss.Feed{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Title: "Posts", Url: "https://example.com/feed.xml"},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Title: "Old", Url: "https://example.com/old.xml"},
	}
	home := `<title>Fetched title</title><link rel="alternate" type="application/rss+xml" href="/feed.xml">`
	checked := []discovery.CandidateCheck{
		{Candidate: discovery.Candidate{URL: mustURL(t, "https://example.com/feed.xml"), Title: "Posts"}, Source: discovery.SourcePage,
			FinalURL: "https://example.com/feed.xml", Status: 200, Feed: "https://example.com/feed.xml", Outcome: discovery.OutcomeFeed},
		{Candidate: discovery.Candidate{URL: mustURL(t, "https://example.com/comments.xml")}, Source: discovery.SourcePage,
			FinalURL: "https://example.com/comments.xml", Status: 200, Feed: "https://example.com/comments.xml", Outcome: discovery.OutcomeFeed},
		{Candidate: discovery.Candidate{URL: mustURL(t, "https://example.com/gone.xml")}, Source: discovery.SourcePage,
			FinalURL: "https://example.com/gone.xml", Status: 404, Outcome: "status 404"},
	}
	serve := func(t *testing.T, s *Server) string {
		t.Helper()
		rr := servePost(t, s, s.siteDiscoverHandler, "/debug/sites/"+testSiteID+"/discover", testSiteID, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}

	t.Run("known, new and missing feeds", func(t *testing.T) {
		d := &fakeDiscoverer{res: discovery.Result{Checked: checked, Feeds: []discovery.FeedInfo{
			{URL: "https://example.com/feed.xml", Title: "Posts"},
			{URL: "https://example.com/comments.xml", Title: "Comments"},
		}}}
		fetcher := htmlResponse(t, "https://example.com/", home)
		body := serve(t, siteDiscoverServer(t, stored, fetcher, d))
		assertBodyContains(t, body,
			"<title>Paper Round - run discovery</title>",
			`<a href="/sites/`+testSiteID+`">Back to the site</a>`,
			`<th scope="row">title</th><td>Fetched title</td><td>Stored title</td>`,
			`<tr><th scope="row">link</th><td>alternate</td><td>https://example.com/feed.xml</td><td>application/rss&#43;xml</td><td>used</td></tr>`,
			`<tr><th scope="row">page</th><td>https://example.com/feed.xml <span class="debug-note">Posts</span></td><td>https://example.com/feed.xml</td><td>200</td><td>feed</td></tr>`,
			`<tr class="debug-ignored"><th scope="row">page</th><td>https://example.com/gone.xml</td><td>https://example.com/gone.xml</td><td>404</td><td>status 404</td></tr>`,
			`<dt>known</dt><dd>Posts <span class="debug-note">https://example.com/feed.xml</span></dd>`,
			`<dt>new</dt><dd>Comments <span class="debug-note">https://example.com/comments.xml</span></dd>`,
			"Stored but not found by this run",
			`<a href="/feeds/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb">Old</a>`,
		)
		if strings.Contains(body, "Discovery failed") {
			t.Error("shows a discovery error without one")
		}
		if !slices.Equal(d.urls, []string{"https://example.com/"}) || !slices.Equal(fetcher.urls, []string{"https://example.com/"}) {
			t.Errorf("discovered %v and fetched %v, want the site's URL once each", d.urls, fetcher.urls)
		}
	})

	t.Run("discovery error still shows Checked", func(t *testing.T) {
		partial := append([]discovery.CandidateCheck{}, checked[0])
		partial = append(partial, discovery.CandidateCheck{Candidate: discovery.Candidate{URL: mustURL(t, "https://example.com/late.xml")},
			Source: discovery.SourcePage, Outcome: discovery.OutcomeDeadline})
		d := &fakeDiscoverer{res: discovery.Result{Checked: partial}, err: errors.New("discovery: checking feeds: context deadline exceeded")}
		body := serve(t, siteDiscoverServer(t, stored, htmlResponse(t, "https://example.com/", home), d))
		assertBodyContains(t, body,
			`<p class="fetch-error" role="alert">Discovery failed: discovery: checking feeds: context deadline exceeded</p>`,
			"<td>not fetched (deadline)</td>",
			"No feeds found.",
		)
	})

	t.Run("direct feed", func(t *testing.T) {
		d := &fakeDiscoverer{res: discovery.Result{Direct: true, Feeds: []discovery.FeedInfo{{URL: "https://example.com/feed.xml", Title: "Posts"}}}}
		body := serve(t, siteDiscoverServer(t, stored, htmlResponse(t, "https://example.com/", home), d))
		assertBodyContains(t, body, "The site's URL is itself a feed")
	})

	t.Run("home page fetch error", func(t *testing.T) {
		d := &fakeDiscoverer{err: errors.New("discovery: fetching https://example.com/: dial tcp: refused")}
		body := serve(t, siteDiscoverServer(t, stored, &fakeFetcher{err: errors.New("dial tcp: refused")}, d))
		assertBodyContains(t, body, "Fetch failed: dial tcp: refused", "Discovery failed: discovery: fetching")
	})

	t.Run("unknown site", func(t *testing.T) {
		const other = "99999999-9999-4999-8999-999999999999"
		d := &fakeDiscoverer{}
		s := siteDiscoverServer(t, stored, htmlResponse(t, "https://example.com/", home), d)
		assertNotFoundHTML(t, servePost(t, s, s.siteDiscoverHandler, "/debug/sites/"+other+"/discover", other, true))
		if len(d.urls) != 0 {
			t.Errorf("discovered %v for an unknown site", d.urls)
		}
	})
}

// TestPageInspectorsDebugOff checks that with debug mode off both inspectors
// are 404 and touch nothing: no store, fetch or discovery is called.
func TestPageInspectorsDebugOff(t *testing.T) {
	untouched := func(t *testing.T) Services {
		return Services{
			ArticleService: &fakeArticleStore{getByIDFn: func(context.Context, string) (rss.Article, error) {
				t.Error("ArticleService.GetByID called")
				return rss.Article{}, nil
			}},
			SiteService: &fakeSiteStore{getByIDFn: func(context.Context, string) (rss.Site, error) {
				t.Error("SiteService.GetByID called")
				return rss.Site{}, nil
			}},
			FeedService: &fakeFeedStore{
				listBySiteFn: func(context.Context, string) ([]rss.Feed, error) {
					t.Error("FeedService.ListBySite called")
					return nil, nil
				},
				requestRefreshFn: func(context.Context, string) error {
					t.Error("FeedService.RequestRefresh called")
					return nil
				},
			},
			DebugService: unusedDebugStore(t),
		}
	}
	for _, tt := range []struct {
		name, target, id string
		handler          func(s *Server) http.HandlerFunc
	}{
		{"page inspector", "/debug/articles/" + testArticleID + "/inspect-page", testArticleID, func(s *Server) http.HandlerFunc { return s.articlePageInspectHandler }},
		{"site discovery", "/debug/sites/" + testSiteID + "/discover", testSiteID, func(s *Server) http.HandlerFunc { return s.siteDiscoverHandler }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			services := untouched(t)
			fetcher, d := &fakeFetcher{}, &fakeDiscoverer{}
			services.Fetcher, services.Discoverer = fetcher, d
			s := newTestServerConfig(t, debugConfig, services)
			assertNotFoundHTML(t, servePost(t, s, tt.handler(s), tt.target, tt.id, false))
			if len(fetcher.urls) != 0 || len(d.urls) != 0 {
				t.Errorf("fetched %v and discovered %v with debug mode off", fetcher.urls, d.urls)
			}
		})
	}
}
