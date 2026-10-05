package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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
	return newTestServerWith(t, Services{
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			return site, siteErr
		}},
		FeedService: &fakeFeedStore{listBySiteFn: func(ctx context.Context, siteID string) ([]rss.Feed, error) {
			return feeds, feedsErr
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
}
