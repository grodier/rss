package server

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
)

const testSiteID = "22222222-2222-4222-8222-222222222222"

func serveSite(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sites/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.siteHandler)).ServeHTTP(rr, req)
	return rr
}

func siteServer(t *testing.T, site rss.Site, siteErr error, feeds []rss.Feed, feedsErr error) *Server {
	t.Helper()
	return siteServerWithLookup(t, site, siteErr, feeds, feedsErr, rss.Lookup{}, rss.ErrNoRecord)
}

func siteServerWithLookup(t *testing.T, site rss.Site, siteErr error, feeds []rss.Feed, feedsErr error, lookup rss.Lookup, lookupErr error) *Server {
	t.Helper()
	return newTestServerWith(t, Services{
		SubscriptionService: &fakeSubscriptionStore{},
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			return site, siteErr
		}},
		FeedService: &fakeFeedStore{listBySiteFn: func(ctx context.Context, siteID string) ([]rss.Feed, error) {
			return feeds, feedsErr
		}},
		LookupService: &fakeLookupStore{getBySiteKeyFn: func(ctx context.Context, siteKey string) (rss.Lookup, error) {
			if siteKey != site.Host {
				t.Errorf("GetBySiteKey(%q), want %q", siteKey, site.Host)
			}
			return lookup, lookupErr
		}},
	})
}

func TestSiteHandlerMalformedID(t *testing.T) {
	s := newTestServer(t) // nil stores: any store call would panic

	for _, id := range []string{"abc", " "} {
		t.Run(id, func(t *testing.T) { assertNotFoundHTML(t, serveSite(t, s, id)) })
	}
}

func TestSiteHandlerNotFound(t *testing.T) {
	s := siteServer(t, rss.Site{}, rss.ErrNoRecord, nil, nil)
	assertNotFoundHTML(t, serveSite(t, s, testSiteID))
}

func TestSiteHandlerListsFeeds(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/", Title: "Example Site"}
	feeds := []rss.Feed{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Title: "First", Url: "https://example.com/a.xml"},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Title: "Second", Url: "https://example.com/b.xml"},
	}
	rr := serveSite(t, siteServer(t, site, nil, feeds, nil), testSiteID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Example Site",
		`href="/feeds/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"`,
		`href="/feeds/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"`,
		`href="https://example.com/"`,
		"https://example.com/a.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestSiteHandlerNoFeeds(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/"}
	rr := serveSite(t, siteServer(t, site, nil, nil, nil), testSiteID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "No feeds found") {
		t.Errorf("body does not contain %q: %s", "No feeds found", rr.Body.String())
	}
}

func TestSiteHandlerEmptyTitleShowsHost(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/"}
	rr := serveSite(t, siteServer(t, site, nil, nil, nil), testSiteID)

	if !strings.Contains(rr.Body.String(), "<h1>example.com</h1>") {
		t.Errorf("h1 does not show host: %s", rr.Body.String())
	}
}

func TestSiteHandlerEmptyFeedTitleShowsURL(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/"}
	feeds := []rss.Feed{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Title: "Titled", Url: "https://example.com/a.xml"},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Url: "https://example.com/b.xml"},
	}
	body := serveSite(t, siteServer(t, site, nil, feeds, nil), testSiteID).Body.String()

	for _, want := range []string{
		`<a class="feed-row-title" href="/feeds/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa">Titled</a>`,
		`<a class="feed-row-title" href="/feeds/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb">https://example.com/b.xml</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestSiteHandlerStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	site := rss.Site{ID: testSiteID, Host: "example.com"}

	t.Run("site", func(t *testing.T) {
		rr := serveSite(t, siteServer(t, rss.Site{}, boom, nil, nil), testSiteID)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
	t.Run("feeds", func(t *testing.T) {
		rr := serveSite(t, siteServer(t, site, nil, nil, boom), testSiteID)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
	t.Run("lookup", func(t *testing.T) {
		rr := serveSite(t, siteServerWithLookup(t, site, nil, nil, nil, rss.Lookup{}, boom), testSiteID)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestCanRecheck(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	lookup := func(status rss.LookupStatus, age time.Duration) rss.Lookup {
		l := rss.Lookup{Status: status}
		if age > 0 {
			l.FinishedAt = now.Add(-age)
		}
		return l
	}

	tests := []struct {
		name   string
		lookup rss.Lookup
		want   bool
	}{
		{"no lookup", rss.Lookup{}, true},
		{"pending", lookup(rss.LookupPending, 0), false},
		{"running", lookup(rss.LookupRunning, 0), false},
		{"done 2h ago", lookup(rss.LookupDone, 2*time.Hour), false},
		{"done exactly 24h ago", lookup(rss.LookupDone, lookupDoneTTL), false},
		{"done just over 24h ago", lookup(rss.LookupDone, lookupDoneTTL+time.Second), true},
		{"failed 30m ago", lookup(rss.LookupFailed, 30*time.Minute), false},
		{"failed exactly 1h ago", lookup(rss.LookupFailed, lookupFailedTTL), false},
		{"failed just over 1h ago", lookup(rss.LookupFailed, lookupFailedTTL+time.Second), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canRecheck(tt.lookup, now); got != tt.want {
				t.Errorf("canRecheck = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSiteHandlerRecheck(t *testing.T) {
	now := time.Now()
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://www.example.com/"}
	const button = "Check for new feeds</button>"
	const checking = "Checking for new feeds"

	tests := []struct {
		name        string
		lookup      rss.Lookup
		lookupErr   error
		wantButton  bool
		wantChecked bool
		wantPending bool
	}{
		{name: "no lookup", lookupErr: rss.ErrNoRecord, wantButton: true},
		{name: "done 2h ago", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupDone, FinishedAt: now.Add(-2 * time.Hour)}, wantChecked: true},
		{name: "done 25h ago", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupDone, FinishedAt: now.Add(-25 * time.Hour)}, wantButton: true, wantChecked: true},
		{name: "failed 30m ago", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupFailed, FinishedAt: now.Add(-30 * time.Minute)}, wantChecked: true},
		{name: "failed 2h ago", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupFailed, FinishedAt: now.Add(-2 * time.Hour)}, wantButton: true, wantChecked: true},
		{name: "pending", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupPending}, wantPending: true},
		{name: "running", lookup: rss.Lookup{ID: testLookupID, Status: rss.LookupRunning}, wantPending: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := siteServerWithLookup(t, site, nil, nil, nil, tt.lookup, tt.lookupErr)
			rr := serveSite(t, s, testSiteID)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()

			if got := strings.Contains(body, button); got != tt.wantButton {
				t.Errorf("button shown = %v, want %v: %s", got, tt.wantButton, body)
			}
			if tt.wantButton && !strings.Contains(body, `<input type="hidden" name="q" value="https://www.example.com/">`) {
				t.Errorf("form does not post the site URL: %s", body)
			}
			if got := strings.Contains(body, "Feeds last checked"); got != tt.wantChecked {
				t.Errorf("last checked shown = %v, want %v: %s", got, tt.wantChecked, body)
			}
			if tt.wantChecked {
				want := "Feeds last checked " + `<time data-local datetime="` + tt.lookup.FinishedAt.UTC().Format("2006-01-02T15:04:05Z07:00") + `">` + tt.lookup.FinishedAt.UTC().Format("2 Jan 2006 15:04 UTC") + "</time>"
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q: %s", want, body)
				}
			}
			if got := strings.Contains(body, checking); got != tt.wantPending {
				t.Errorf("checking shown = %v, want %v: %s", got, tt.wantPending, body)
			}
			if tt.wantPending && !strings.Contains(body, `href="/lookups/`+testLookupID+`"`) {
				t.Errorf("no link to lookup: %s", body)
			}
		})
	}
}

// The recheck form posts site.URL to POST /lookups, which must look up the
// same site key as site.Host.
func TestSiteURLMapsToSiteHost(t *testing.T) {
	u, err := discovery.ParseInput("https://www.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got := discovery.SiteKey(u); got != "example.com" {
		t.Errorf("SiteKey = %q, want %q", got, "example.com")
	}
}

func TestSiteHandlerSubscribeControls(t *testing.T) {
	const (
		subscribedID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		openID       = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		goneID       = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		goneFollowID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	)
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/"}
	feeds := []rss.Feed{
		{ID: subscribedID, Title: "Followed", Url: "https://example.com/a.xml"},
		{ID: openID, Title: "Open", Url: "https://example.com/b.xml"},
		{ID: goneID, Title: "Gone", Url: "https://example.com/c.xml", GoneAt: time.Now()},
		{ID: goneFollowID, Title: "Gone but followed", Url: "https://example.com/d.xml", GoneAt: time.Now()},
	}
	var gotUser string
	var gotIDs []string
	s := siteServer(t, site, nil, feeds, nil)
	s.services.SubscriptionService = &fakeSubscriptionStore{
		subscribedFeedIDsFn: func(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
			gotUser, gotIDs = userID, feedIDs
			return map[string]bool{subscribedID: true, goneFollowID: true}, nil
		},
	}

	rr := serveSiteAs(t, s, "user-1", testSiteID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser != "user-1" || strings.Join(gotIDs, ",") != strings.Join([]string{subscribedID, openID, goneID, goneFollowID}, ",") {
		t.Errorf("SubscribedFeedIDs(%q, %v), want user-1 and the site's feed IDs in order", gotUser, gotIDs)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`id="feed-` + subscribedID + `"`,
		`action="/feeds/` + subscribedID + `/unsubscribe"`,
		`value="/sites/` + testSiteID + `#feed-` + subscribedID + `"`,
		`aria-label="Unsubscribe from Followed"`,
		`action="/feeds/` + openID + `/subscribe"`,
		`value="/sites/` + testSiteID + `#feed-` + openID + `"`,
		`aria-label="Subscribe to Open"`,
		`action="/feeds/` + goneFollowID + `/unsubscribe"`,
		"This feed no longer exists.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		`action="/feeds/` + subscribedID + `/subscribe"`,
		`action="/feeds/` + openID + `/unsubscribe"`,
		`action="/feeds/` + goneID + `/subscribe"`,
		`action="/feeds/` + goneID + `/unsubscribe"`,
		`action="/feeds/` + goneFollowID + `/subscribe"`,
	} {
		if strings.Contains(body, notWant) {
			t.Errorf("body contains %q: %s", notWant, body)
		}
	}
	if n := strings.Count(body, "This feed no longer exists."); n != 1 {
		t.Errorf("%q appears %d times, want 1 (only the gone feed that isn't followed)", "This feed no longer exists.", n)
	}
}

func TestSiteHandlerSubscribedFeedIDsError(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com"}
	s := siteServer(t, site, nil, []rss.Feed{{ID: testFeedID, Title: "F"}}, nil)
	s.services.SubscriptionService = &fakeSubscriptionStore{
		subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("db down")
		},
	}

	rr := serveSite(t, s, testSiteID)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// serveSiteAs is serveSite for a logged-in user.
func serveSiteAs(t *testing.T, s *Server, userID, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sites/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, authenticatedUserIDContextKey, userID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.siteHandler)).ServeHTTP(rr, req)
	return rr
}

func TestSiteHandlerSiteIcons(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "example.com", URL: "https://example.com/", Title: "Example Site"}
	feeds := []rss.Feed{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SiteID: testSiteID, Title: "Essays", Url: "https://example.com/a.xml"},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SiteID: testSiteID, Title: "Events", Url: "https://example.com/b.xml"},
	}
	s := siteServer(t, site, nil, feeds, nil)
	render := func(t *testing.T) string {
		rr := serveSite(t, s, testSiteID)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	}
	testSiteIcons(t, s, testSiteID, "E", render)

	t.Run("header and feed rows", func(t *testing.T) {
		s.services.SiteService.(*fakeSiteStore).iconHashesFn = func(context.Context, []string) (map[string]string, error) {
			return map[string]string{testSiteID: "abc123"}, nil
		}
		if got := strings.Count(render(t), `<img src="/sites/`+testSiteID+`/icon?v=abc123"`); got != 1+len(feeds) {
			t.Errorf("found %d icons, want %d (the header and each feed)", got, 1+len(feeds))
		}
	})
}

// getSiteIcon requests site id's icon through the router, without a session.
func getSiteIcon(t *testing.T, s *Server, id, query string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sites/"+id+"/icon"+query, nil)
	maps.Copy(req.Header, header)
	rr := httptest.NewRecorder()
	s.router().ServeHTTP(rr, req)
	return rr
}

func TestSiteIconHandler(t *testing.T) {
	icon := rss.SiteIcon{SiteID: testSiteID, ContentType: "image/png", Data: []byte("\x89PNG fake"), Hash: "abc123"}
	var gotID string
	var iconErr error
	s := newTestServerWith(t, Services{SiteService: &fakeSiteStore{getIconFn: func(_ context.Context, siteID string) (rss.SiteIcon, error) {
		gotID = siteID
		if iconErr != nil {
			return rss.SiteIcon{}, iconErr
		}
		return icon, nil
	}}})

	t.Run("serves the icon, locked down", func(t *testing.T) {
		iconErr = nil
		rr := getSiteIcon(t, s, testSiteID, "?v=abc123", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if gotID != testSiteID {
			t.Errorf("GetIcon(%q), want %q", gotID, testSiteID)
		}
		if got := rr.Body.String(); got != string(icon.Data) {
			t.Errorf("body = %q, want %q", got, icon.Data)
		}
		for key, want := range map[string]string{
			"Content-Type":            "image/png",
			"Content-Security-Policy": "default-src 'none'; sandbox",
			"X-Content-Type-Options":  "nosniff",
			"ETag":                    `"abc123"`,
			"Cache-Control":           "public, max-age=31536000, immutable",
		} {
			if got := rr.Header().Values(key); len(got) != 1 || got[0] != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	})

	for _, query := range []string{"", "?v=stale"} {
		t.Run("short cache without the current hash "+query, func(t *testing.T) {
			iconErr = nil
			rr := getSiteIcon(t, s, testSiteID, query, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if got, want := rr.Header().Get("Cache-Control"), "public, max-age=3600"; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
		})
	}

	for _, inm := range []string{`"abc123"`, `W/"abc123"`, `"other", "abc123"`, "*"} {
		t.Run("If-None-Match "+inm, func(t *testing.T) {
			iconErr = nil
			rr := getSiteIcon(t, s, testSiteID, "?v=abc123", http.Header{"If-None-Match": {inm}})
			if rr.Code != http.StatusNotModified {
				t.Fatalf("status = %d, want 304", rr.Code)
			}
			if rr.Body.Len() != 0 {
				t.Errorf("body = %q, want none", rr.Body.String())
			}
			if got := rr.Header().Get("ETag"); got != `"abc123"` {
				t.Errorf("ETag = %q", got)
			}
		})
	}

	t.Run("If-None-Match for another version", func(t *testing.T) {
		iconErr = nil
		rr := getSiteIcon(t, s, testSiteID, "", http.Header{"If-None-Match": {`"old"`}})
		if rr.Code != http.StatusOK || rr.Body.String() != string(icon.Data) {
			t.Errorf("status = %d, body = %q; want 200 with the icon", rr.Code, rr.Body.String())
		}
	})

	t.Run("no icon", func(t *testing.T) {
		iconErr = rss.ErrNoRecord
		assertNotFoundHTML(t, getSiteIcon(t, s, testSiteID, "", nil))
	})

	t.Run("malformed ID", func(t *testing.T) {
		iconErr = nil
		gotID = ""
		assertNotFoundHTML(t, getSiteIcon(t, s, "abc", "", nil))
		if gotID != "" {
			t.Errorf("GetIcon called with %q", gotID)
		}
	})

	t.Run("store error", func(t *testing.T) {
		iconErr = errors.New("boom")
		rr := getSiteIcon(t, s, testSiteID, "", nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("sets no session cookie", func(t *testing.T) {
		iconErr = nil
		rr := getSiteIcon(t, s, testSiteID, "", nil)
		if cookies := rr.Result().Cookies(); len(cookies) != 0 {
			t.Errorf("set cookies %v, want none", cookies)
		}
	})
}
