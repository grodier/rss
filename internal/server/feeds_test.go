package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/psql"
)

func TestFeedHandlerMalformedID(t *testing.T) {
	s := newTestServer(t)

	for _, id := range []string{"abc", " "} {
		req := httptest.NewRequest(http.MethodGet, "/feeds/x", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rr := httptest.NewRecorder()

		s.feedHandler(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("id %q: got status %d, want %d", id, rr.Code, http.StatusNotFound)
		}
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

func TestFeedHandlerSuccess(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		getByIDFn: func(ctx context.Context, id string) (psql.Feed, error) {
			return psql.Feed{Title: "Example Feed Title"}, nil
		},
	}})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "Example Feed Title") {
		t.Errorf("body does not contain feed title: %s", rr.Body.String())
	}
}

func TestFeedHandlerNotFound(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		getByIDFn: func(ctx context.Context, id string) (psql.Feed, error) {
			return psql.Feed{}, psql.ErrNoRecord
		},
	}})

	rr := serveFeed(t, s, testFeedID)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func postCreateFeed(s *Server) *httptest.ResponseRecorder {
	form := url.Values{"url": {"https://example.com/rss.xml"}}
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.createFeedHandler)).ServeHTTP(rr, req)
	return rr
}

func TestCreateFeedHandlerDuplicate(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		createFn: func(ctx context.Context, f psql.Feed) (string, error) {
			return "", psql.ErrDuplicateFeed
		},
	}})

	rr := postCreateFeed(s)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rr.Body.String(), "This feed has already been added") {
		t.Errorf("body does not contain duplicate error: %s", rr.Body.String())
	}
}

func TestCreateFeedHandlerSuccess(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		createFn: func(ctx context.Context, f psql.Feed) (string, error) {
			if f.Url != "https://example.com/rss.xml" {
				t.Errorf("Url = %q", f.Url)
			}
			return testFeedID, nil
		},
	}})

	rr := postCreateFeed(s)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if loc := rr.Header().Get("Location"); loc != "/feeds/"+testFeedID {
		t.Errorf("Location = %q, want /feeds/%s", loc, testFeedID)
	}
}

func TestCreateFeedHandlerStoreError(t *testing.T) {
	s := newTestServerWith(t, Services{FeedService: &fakeFeedStore{
		createFn: func(ctx context.Context, f psql.Feed) (string, error) {
			return "", errors.New("db down")
		},
	}})

	rr := postCreateFeed(s)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}
