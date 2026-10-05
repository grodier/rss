package server

import (
	"context"
	"errors"
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

func TestSiteHandlerRecheck(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
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
			s.now = func() time.Time { return now }
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
				want := "Feeds last checked " + tt.lookup.FinishedAt.Format("2 Jan 2006 15:04 MST")
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
