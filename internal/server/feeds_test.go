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

func assertNotFoundHTML(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), "Page not found") {
		t.Errorf("body does not contain %q: %s", "Page not found", rr.Body.String())
	}
}

func TestFeedHandlerMalformedID(t *testing.T) {
	s := newTestServer(t)

	for _, id := range []string{"abc", " "} {
		req := httptest.NewRequest(http.MethodGet, "/feeds/x", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rr := httptest.NewRecorder()

		s.feedHandler(rr, req)

		t.Run(id, func(t *testing.T) { assertNotFoundHTML(t, rr) })
	}
}

const testFeedID = "11111111-1111-4111-8111-111111111111"

func serveFeed(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.feedHandler)).ServeHTTP(rr, req)
	return rr
}

func feedServer(t *testing.T, siteErr error) *Server {
	t.Helper()
	return newTestServerWith(t, Services{
		FeedService: &fakeFeedStore{
			getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
				return rss.Feed{
					Title:       "Example Feed Title",
					Description: "A feed about examples",
					Url:         "https://example.com/feed.xml",
					SiteID:      testSiteID,
				}, nil
			},
		},
		SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
			if siteErr != nil {
				return rss.Site{}, siteErr
			}
			return rss.Site{ID: id, Host: "example.com", Title: "Example Site"}, nil
		}},
	})
}

func TestFeedHandlerSuccess(t *testing.T) {
	rr := serveFeed(t, feedServer(t, nil), testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Example Feed Title",
		"A feed about examples",
		"https://example.com/feed.xml",
		`href="/sites/` + testSiteID + `"`,
		"Example Site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
	}
}

func TestFeedHandlerSiteError(t *testing.T) {
	rr := serveFeed(t, feedServer(t, errors.New("boom")), testFeedID)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestFeedHandlerNotFound(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
			return rss.Feed{}, rss.ErrNoRecord
		},
	}})

	rr := serveFeed(t, s, testFeedID)

	assertNotFoundHTML(t, rr)
}
