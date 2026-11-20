package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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
		ArticleService: &fakeArticleStore{},
		ReadService:    &fakeReadStore{},
		SaveService:    &fakeSaveStore{},
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
		"<h2>2 feeds</h2>",
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
	if strings.Contains(rr.Body.String(), "Recent posts") {
		t.Errorf("a site without feeds has a Recent posts section: %s", rr.Body.String())
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
	t.Run("feed facts", func(t *testing.T) {
		s := siteServer(t, site, nil, nil, nil)
		s.services.FeedService.(*fakeFeedStore).siteFeedFactsFn = func(context.Context, string) (map[string]rss.FeedFacts, error) {
			return nil, boom
		}
		if rr := serveSite(t, s, testSiteID); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
	t.Run("posts", func(t *testing.T) {
		s := siteServer(t, site, nil, []rss.Feed{{ID: testFeedID, Title: "F"}}, nil)
		s.services.ArticleService = &fakeArticleStore{listByFeedsFn: func(context.Context, []string, rss.ArticleCursor, int) ([]rss.Article, error) {
			return nil, boom
		}}
		if rr := serveSite(t, s, testSiteID); rr.Code != http.StatusInternalServerError {
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
		l := rss.Lookup{Status: status, SiteID: testSiteID, URL: "https://example.com/"}
		if age > 0 {
			l.FinishedAt = now.Add(-age)
		}
		return l
	}
	fromFeed := func(status rss.LookupStatus, age time.Duration) rss.Lookup {
		l := lookup(status, age)
		l.URL = "https://example.com/feed.xml"
		return l
	}
	noFeeds := func(age time.Duration) rss.Lookup {
		l := lookup(rss.LookupDone, age)
		l.SiteID = ""
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
		{"no feeds found 30m ago", noFeeds(30 * time.Minute), false},
		{"no feeds found just over 1h ago", noFeeds(lookupFailedTTL + time.Second), true},
		{"done 2h ago on a feed's address", fromFeed(rss.LookupDone, 2*time.Hour), true},
		{"running on a feed's address", fromFeed(rss.LookupRunning, 0), false},
		{"failed 30m ago on a feed's address", fromFeed(rss.LookupFailed, 30*time.Minute), false},
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
	const button = "Check example.com for new feeds</span></button>"
	const checking = "<span>Checking for new feeds"

	tests := []struct {
		name        string
		lookup      rss.Lookup
		lookupErr   error
		wantButton  bool
		wantChecked bool
		wantPending bool
	}{
		{name: "no lookup", lookupErr: rss.ErrNoRecord, wantButton: true},
		{name: "done 2h ago", lookup: rss.Lookup{ID: testLookupID, URL: "https://example.com/", Status: rss.LookupDone, SiteID: testSiteID, FinishedAt: now.Add(-2 * time.Hour)}, wantChecked: true},
		{name: "done 25h ago", lookup: rss.Lookup{ID: testLookupID, URL: "https://example.com/", Status: rss.LookupDone, SiteID: testSiteID, FinishedAt: now.Add(-25 * time.Hour)}, wantButton: true, wantChecked: true},
		{name: "done 2h ago on a feed's address", lookup: rss.Lookup{ID: testLookupID, URL: "https://example.com/feed.xml", Status: rss.LookupDone, SiteID: testSiteID, FinishedAt: now.Add(-2 * time.Hour)}, wantButton: true, wantChecked: true},
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
		`aria-pressed="true" aria-label="Follow example.com – Followed"`,
		`action="/feeds/` + openID + `/subscribe"`,
		`value="/sites/` + testSiteID + `#feed-` + openID + `"`,
		`aria-pressed="false" aria-label="Follow example.com – Open"`,
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
	if n := strings.Count(body, "This feed no longer exists."); n != 2 {
		t.Errorf("%q appears %d times, want 2 (each gone feed)", "This feed no longer exists.", n)
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

	t.Run("header only", func(t *testing.T) {
		s.services.SiteService.(*fakeSiteStore).iconHashesFn = func(context.Context, []string) (map[string]string, error) {
			return map[string]string{testSiteID: "abc123"}, nil
		}
		// The feed rows and posts leave the site's avatar to the head.
		if got := strings.Count(render(t), `<img src="/sites/`+testSiteID+`/icon?v=abc123"`); got != 1 {
			t.Errorf("found %d icons, want 1 (the header)", got)
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

// siteWithPosts is a server for site's page with feeds, their facts, the
// articles ListByFeeds returns (recording the feed IDs and limit it was
// asked for) and the feeds the reader follows.
func siteWithPosts(t *testing.T, site rss.Site, feeds []rss.Feed, facts map[string]rss.FeedFacts, articles []rss.Article, following map[string]bool) (s *Server, gotIDs *[]string, gotLimit *int) {
	t.Helper()
	s = siteServer(t, site, nil, feeds, nil)
	s.services.FeedService.(*fakeFeedStore).siteFeedFactsFn = func(context.Context, string) (map[string]rss.FeedFacts, error) {
		return facts, nil
	}
	gotIDs, gotLimit = new([]string), new(int)
	s.services.ArticleService = &fakeArticleStore{listByFeedsFn: func(_ context.Context, ids []string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
		if !before.IsZero() {
			t.Errorf("ListByFeeds before = %v, want the first page", before)
		}
		*gotIDs, *gotLimit = ids, limit
		return articles, nil
	}}
	s.services.SubscriptionService = &fakeSubscriptionStore{subscribedFeedIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
		return following, nil
	}}
	return s, gotIDs, gotLimit
}

func TestSiteHandlerFeedRows(t *testing.T) {
	const (
		notesID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		mainID    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		podcastID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		goneID    = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	)
	now := time.Now()
	site := rss.Site{ID: testSiteID, Host: "harbor.dev", URL: "https://harbor.dev/", Title: "Harbor Works"}
	// In ListBySite's order: by title.
	feeds := []rss.Feed{
		{ID: goneID, SiteID: testSiteID, Title: "Harbor Works – Archive", GoneAt: now},
		{ID: mainID, SiteID: testSiteID, Title: "Harbor Works", PostsPerWeek: 4},
		{ID: notesID, SiteID: testSiteID, Title: "Harbor Works – Short notes", PostsPerWeek: 10},
		{ID: podcastID, SiteID: testSiteID, Title: "The Harbor Works Podcast", PostsPerWeek: 0.5},
	}
	facts := map[string]rss.FeedFacts{
		mainID:    {Section: rss.Section{ID: "s1", Slug: "technology", Name: "Technology"}, LatestAt: now.Add(-50 * time.Hour)},
		notesID:   {LatestAt: now.Add(-time.Hour)},
		podcastID: {LatestAt: now.Add(-12 * 24 * time.Hour)},
	}
	s, _, _ := siteWithPosts(t, site, feeds, facts, nil, nil)
	rr := serveSite(t, s, testSiteID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	t.Run("catalog first, then most recently posting, gone last", func(t *testing.T) {
		var last int
		for _, id := range []string{mainID, notesID, podcastID, goneID} {
			i := strings.Index(body, `id="feed-`+id+`"`)
			if i < last {
				t.Fatalf("feed %s at %d, before the previous row (%d)", id, i, last)
			}
			last = i
		}
	})
	t.Run("facts", func(t *testing.T) {
		for _, want := range []string{
			"<h2>4 feeds</h2>",
			`<a class="site-feed-section" href="/discover/sections/technology">Technology</a> · <span>About 4 posts a week</span> · <span>Latest 2 days ago</span>`,
			`<span>About 10 posts a week</span> · <span>Latest today</span>`,
			`<span>Less than one post a week</span> · <span>Latest 12 days ago</span>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
		if n := strings.Count(body, `class="site-feed-section"`); n != 1 {
			t.Errorf("%d section labels, want 1 (only the catalog feed)", n)
		}
	})
	t.Run("names without the site's", func(t *testing.T) {
		for _, want := range []string{
			`href="/feeds/` + notesID + `">Short notes</a>`,
			`href="/feeds/` + mainID + `">Harbor Works</a>`,
			`href="/feeds/` + podcastID + `">The Harbor Works Podcast</a>`,
			// Follow's accessible name keeps the site's.
			`aria-label="Follow Harbor Works – Short notes"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
	})
	t.Run("gone feed", func(t *testing.T) {
		row := body[strings.Index(body, `id="feed-`+goneID+`"`):]
		row = row[:strings.Index(row, "</li>")]
		if !strings.Contains(row, "This feed no longer exists.") || strings.Contains(row, "<form") {
			t.Errorf("gone feed's row = %s, want the gone message and no Follow", row)
		}
	})
}

func TestSiteHandlerMoreFeeds(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "ledger.com", URL: "https://ledger.com/", Title: "Ledger"}
	var feeds []rss.Feed
	for i := range 6 {
		feeds = append(feeds, rss.Feed{ID: fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i), SiteID: testSiteID, Title: fmt.Sprintf("Ledger – Desk %d", i)})
	}
	s, _, _ := siteWithPosts(t, site, feeds, nil, nil, nil)
	body := serveSite(t, s, testSiteID).Body.String()

	details := strings.Index(body, `<details class="site-more-feeds">`)
	if details < 0 {
		t.Fatalf("no more feeds disclosure: %s", body)
	}
	if !strings.Contains(body, "<summary>Show 2 more feeds</summary>") {
		t.Errorf("body does not contain the summary for 2 more feeds: %s", body)
	}
	for i, f := range feeds {
		at := strings.Index(body, `id="feed-`+f.ID+`"`)
		if hidden := at > details; hidden != (i >= siteFeedsShown) {
			t.Errorf("feed %d behind the disclosure = %v, want %v", i, hidden, i >= siteFeedsShown)
		}
	}
	if !strings.Contains(body, "<h2>6 feeds</h2>") {
		t.Errorf("heading doesn't count every feed: %s", body)
	}
}

func TestSiteHandlerOneFeed(t *testing.T) {
	site := rss.Site{ID: testSiteID, Host: "quiet.blog", URL: "https://quiet.blog/", Title: "Quiet Light"}
	feeds := []rss.Feed{{ID: testFeedID, SiteID: testSiteID, Title: "Quiet Light", PostsPerWeek: 1}}
	facts := map[string]rss.FeedFacts{testFeedID: {LatestAt: time.Now()}}
	articles := []rss.Article{{ID: "a1", FeedID: testFeedID, Title: "A borrowed Leica"}}

	for _, tt := range []struct {
		name      string
		following bool
		want      string
	}{
		{"not followed", false, `action="/feeds/` + testFeedID + `/subscribe"`},
		{"followed", true, `action="/feeds/` + testFeedID + `/unsubscribe"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := siteWithPosts(t, site, feeds, facts, articles, map[string]bool{testFeedID: tt.following})
			body := serveSite(t, s, testSiteID).Body.String()

			head := body[strings.Index(body, `<section class="profile">`):]
			head = head[:strings.Index(head, "</section>")]
			for _, want := range []string{tt.want, `name="return_to" value="/sites/` + testSiteID + `"`, "About 1 post a week.</span>", "Latest post today.</span>"} {
				if !strings.Contains(head, want) {
					t.Errorf("head does not contain %q: %s", want, head)
				}
			}
			if strings.Contains(body, `class="site-feeds"`) {
				t.Errorf("a site with one feed lists its feeds: %s", body)
			}
			if strings.Contains(body, `class="article-feed"`) {
				t.Errorf("a one-feed site's posts name their feed: %s", body)
			}
		})
	}
}

func TestSiteHandlerRecentPosts(t *testing.T) {
	const (
		mainID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		opinionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		goneID    = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	)
	site := rss.Site{ID: testSiteID, Host: "fieldnotes.news", URL: "https://fieldnotes.news/", Title: "Field Notes"}
	feeds := []rss.Feed{
		{ID: goneID, SiteID: testSiteID, Title: "Field Notes: Archive", GoneAt: time.Now()},
		{ID: mainID, SiteID: testSiteID, Title: "Field Notes"},
		{ID: opinionID, SiteID: testSiteID, Title: "Field Notes: Opinion"},
	}
	inCatalog := rss.FeedFacts{Section: rss.Section{ID: "s1", Slug: "food", Name: "Food"}}

	t.Run("from the catalog feeds when there are some", func(t *testing.T) {
		facts := map[string]rss.FeedFacts{mainID: inCatalog}
		articles := []rss.Article{{ID: "a1", FeedID: mainID, Title: "The late harvest"}}
		s, gotIDs, gotLimit := siteWithPosts(t, site, feeds, facts, articles, nil)
		body := serveSite(t, s, testSiteID).Body.String()

		if strings.Join(*gotIDs, ",") != mainID || *gotLimit != 2*sitePostsLimit {
			t.Errorf("ListByFeeds(%v, %d), want ([%s], %d)", *gotIDs, *gotLimit, mainID, 2*sitePostsLimit)
		}
		if want := `<p class="site-posts-from">From Field Notes, the feed Discover lists.</p>`; !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
		if strings.Contains(body, `class="article-feed"`) {
			t.Errorf("posts from one feed name it: %s", body)
		}
	})

	t.Run("else from every live feed, each named", func(t *testing.T) {
		articles := []rss.Article{
			{ID: "a1", FeedID: opinionID, Title: "Buy the ugly apples"},
			{ID: "a2", FeedID: mainID, Title: "The late harvest"},
		}
		s, gotIDs, _ := siteWithPosts(t, site, feeds, nil, articles, nil)
		body := serveSite(t, s, testSiteID).Body.String()

		slices.Sort(*gotIDs)
		if strings.Join(*gotIDs, ",") != mainID+","+opinionID {
			t.Errorf("ListByFeeds(%v), want the live feeds %s and %s", *gotIDs, mainID, opinionID)
		}
		if strings.Contains(body, "site-posts-from") {
			t.Errorf("posts from every live feed explain where they're from: %s", body)
		}
		for _, want := range []string{
			`<a class="article-feed" href="/feeds/` + opinionID + `">Opinion</a>`,
			`<a class="article-feed" href="/feeds/` + mainID + `">Field Notes</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body does not contain %q: %s", want, body)
			}
		}
		// The site's avatar is in the head; posts don't repeat it, nor Follow.
		posts := body[strings.Index(body, `<section class="site-posts">`):]
		if strings.Contains(posts, `class="avatar`) || strings.Contains(posts, "follow-form") {
			t.Errorf("posts have an avatar or Follow: %s", posts)
		}
	})

	t.Run("a copy in two feeds shows once, up to the limit", func(t *testing.T) {
		articles := []rss.Article{
			{ID: "a1", FeedID: mainID, Title: "Water rights", CanonicalURL: "https://fieldnotes.news/water"},
			{ID: "a2", FeedID: opinionID, Title: "Water rights", CanonicalURL: "https://fieldnotes.news/water"},
		}
		for i := range 2 * sitePostsLimit {
			articles = append(articles, rss.Article{ID: fmt.Sprintf("b%d", i), FeedID: mainID, Title: fmt.Sprintf("Post %d", i)})
		}
		s, _, _ := siteWithPosts(t, site, feeds, nil, articles, nil)
		body := serveSite(t, s, testSiteID).Body.String()

		if strings.Contains(body, `id="article-a2"`) {
			t.Errorf("the copy shows too: %s", body)
		}
		if n := strings.Count(body, `<article class="article-row`); n != sitePostsLimit {
			t.Errorf("%d posts, want %d", n, sitePostsLimit)
		}
	})

	t.Run("no posts yet", func(t *testing.T) {
		s, _, _ := siteWithPosts(t, site, feeds, nil, nil, nil)
		if body := serveSite(t, s, testSiteID).Body.String(); !strings.Contains(body, `<p class="empty">No posts yet.</p>`) {
			t.Errorf("body has no empty state: %s", body)
		}
	})

	t.Run("every feed gone", func(t *testing.T) {
		s, gotIDs, _ := siteWithPosts(t, site, feeds[:1], nil, nil, nil)
		body := serveSite(t, s, testSiteID).Body.String()
		if *gotIDs != nil {
			t.Errorf("ListByFeeds(%v), want no call", *gotIDs)
		}
		if !strings.Contains(body, "No posts to show: this site's feeds no longer exist.") {
			t.Errorf("body has no gone state: %s", body)
		}
	})
}

func TestShortFeedName(t *testing.T) {
	for _, tt := range []struct{ title, site, want string }{
		{"The Daily Ledger – Opinion", "The Daily Ledger", "Opinion"},
		{"Field Notes: Opinion", "Field Notes", "Opinion"},
		{"Harbor Works - Notes", "Harbor Works", "Notes"},
		{"Harbor Works", "Harbor Works", "Harbor Works"},
		{"Harbor Works – ", "Harbor Works", "Harbor Works – "},
		{"The Harbor Works Podcast", "Harbor Works", "The Harbor Works Podcast"},
		{"Harbor Worksheets", "Harbor Works", "Harbor Worksheets"},
	} {
		if got := shortFeedName(tt.title, tt.site); got != tt.want {
			t.Errorf("shortFeedName(%q, %q) = %q, want %q", tt.title, tt.site, got, tt.want)
		}
	}
}
