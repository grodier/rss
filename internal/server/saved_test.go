package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

func savedItems(n int) []rss.SavedArticle {
	items := make([]rss.SavedArticle, n)
	for i := range items {
		items[i] = rss.SavedArticle{
			Article: rss.Article{
				ID:    fmt.Sprintf(timelineUUID, i),
				Title: fmt.Sprintf("Article %d", i),
			},
			Feed:    rss.Feed{ID: "feed-1", Title: "Feed One"},
			SavedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute),
		}
	}
	return items
}

func getSaved(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.savedHandler)).ServeHTTP(rr, req)
	return rr
}

func TestSavedHandler(t *testing.T) {
	var gotUser string
	var gotBefore rss.ArticleCursor
	var gotLimit int
	calls := 0
	var result []rss.SavedArticle
	var storeErr error
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{
		listSavedFn: func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
			calls++
			gotUser, gotBefore, gotLimit = userID, before, limit
			return result, storeErr
		},
	}})

	t.Run("rows render with their feed and a pressed save button", func(t *testing.T) {
		result, storeErr = savedItems(2), nil
		rr := getSaved(t, s, "/saved")
		body := rr.Body.String()
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if gotUser != "user-1" || gotLimit != 31 || !gotBefore.IsZero() {
			t.Errorf("store called with (%q, %+v, %d)", gotUser, gotBefore, gotLimit)
		}
		assertBodyContains(t, body, "<title>Saved</title>", "<h1>Saved</h1>", `href="/feeds/feed-1"`, "Feed One", `href="/saved" aria-current="page"`)
		i0, i1 := strings.Index(body, "Article 0"), strings.Index(body, "Article 1")
		if i0 < 0 || i0 > i1 {
			t.Errorf("articles out of order: %d %d", i0, i1)
		}
		forms := saveForms(t, body)
		for _, item := range result {
			id := item.Article.ID
			want := saveFormMarkup{Action: "/articles/" + id + "/unsave", Pressed: "true", ReturnTo: "/saved"}
			if got := forms[id]; got != want {
				t.Errorf("Save form for %s = %+v, want %+v", id, got, want)
			}
		}
		if strings.Contains(body, "Older saved articles") || strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected pager: %s", body)
		}
	})

	t.Run("read rows are marked", func(t *testing.T) {
		result, storeErr = savedItems(2), nil
		var gotReadUser string
		var gotIDs []string
		s.services.ReadService = readStoreReporting(&gotReadUser, &gotIDs, result[0].Article.ID)
		t.Cleanup(func() { s.services.ReadService = &fakeReadStore{} })

		assertReadMarkers(t, getSaved(t, s, "/saved").Body.String(), result[0].Article.ID, result[1].Article.ID)
	})

	t.Run("31 results gives 30 rows and an older link", func(t *testing.T) {
		result, storeErr = savedItems(31), nil
		body := getSaved(t, s, "/saved").Body.String()
		if got := strings.Count(body, `class="article-row"`); got != 30 {
			t.Errorf("rows = %d, want 30", got)
		}
		if !strings.Contains(body, "Older saved articles") {
			t.Fatalf("no older link: %s", body)
		}
		start := strings.Index(body, `href="/saved?before=`)
		if start < 0 {
			t.Fatalf("no before link: %s", body)
		}
		href := body[start+len(`href="`):]
		href = href[:strings.Index(href, `"`)]
		got, err := url.ParseRequestURI(href)
		want := encodeCursor(rss.ArticleCursor{At: result[29].SavedAt, ID: result[29].Article.ID})
		if err != nil || got.Query().Get("before") != want {
			t.Errorf("href = %q, want the cursor of the 30th row %q", href, want)
		}
	})

	t.Run("before is decoded and shows back to newest", func(t *testing.T) {
		result, storeErr = savedItems(2), nil
		want := rss.ArticleCursor{At: time.Date(2026, 1, 1, 0, 0, 0, 5, time.UTC), ID: "123e4567-e89b-12d3-a456-426614174000"}
		rr := getSaved(t, s, "/saved?before="+url.QueryEscape(encodeCursor(want)))
		if !gotBefore.At.Equal(want.At) || gotBefore.ID != want.ID {
			t.Errorf("store before = %+v, want %+v", gotBefore, want)
		}
		assertBodyContains(t, rr.Body.String(), `<a href="/saved">Back to newest</a>`)
	})

	t.Run("malformed before is 400", func(t *testing.T) {
		before := calls
		rr := getSaved(t, s, "/saved?before=junk")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
		if calls != before {
			t.Errorf("store was called")
		}
	})

	t.Run("empty first page", func(t *testing.T) {
		result, storeErr = nil, nil
		body := getSaved(t, s, "/saved").Body.String()
		assertBodyContains(t, body, "Nothing saved yet. Use the bookmark on any article to keep it here.")
	})

	t.Run("empty later page", func(t *testing.T) {
		result, storeErr = nil, nil
		cur := encodeCursor(rss.ArticleCursor{At: time.Now(), ID: "123e4567-e89b-12d3-a456-426614174000"})
		body := getSaved(t, s, "/saved?before="+url.QueryEscape(cur)).Body.String()
		if !strings.Contains(body, "No older saved articles.") || strings.Contains(body, "Nothing saved yet") {
			t.Errorf("wrong empty message: %s", body)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		result, storeErr = nil, errors.New("boom")
		if rr := getSaved(t, s, "/saved"); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("read store error is 500", func(t *testing.T) {
		result, storeErr = savedItems(2), nil
		s.services.ReadService = &fakeReadStore{readArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("boom")
		}}
		t.Cleanup(func() { s.services.ReadService = &fakeReadStore{} })
		if rr := getSaved(t, s, "/saved"); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("anonymous is sent to log in", func(t *testing.T) {
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/saved", nil))
		assertRedirect(t, rr, "/login")
	})
}

func TestSavedHandlerSiteIcons(t *testing.T) {
	items := savedItems(2)
	for i := range items {
		items[i].Feed.SiteID = testSiteID
	}
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SiteService: &fakeSiteStore{},
		SaveService: &fakeSaveStore{listSavedFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.SavedArticle, error) {
			return items, nil
		}}})
	testSiteIcons(t, s, testSiteID, "F", func(t *testing.T) string {
		rr := getSaved(t, s, "/saved")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}
