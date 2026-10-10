package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

const timelineUUID = "123e4567-e89b-12d3-a456-4266141740%02d"

func timelineItems(n int) []rss.ArticleWithFeed {
	items := make([]rss.ArticleWithFeed, n)
	for i := range items {
		items[i] = rss.ArticleWithFeed{
			Article: rss.Article{
				ID:         fmt.Sprintf(timelineUUID, i),
				Title:      fmt.Sprintf("Article %d", i),
				TimelineAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute),
			},
			Feed: rss.Feed{ID: "feed-1", Title: "Feed One"},
		}
	}
	return items
}

func getHome(t *testing.T, s *Server, target string, loggedIn bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if loggedIn {
		req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
	}
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.homeHandler)).ServeHTTP(rr, req)
	return rr
}

func TestHomeHandlerTimeline(t *testing.T) {
	var gotUser string
	var gotBefore rss.ArticleCursor
	var gotLimit int
	calls := 0
	var result []rss.ArticleWithFeed
	var storeErr error
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
			calls++
			gotUser, gotBefore, gotLimit = userID, before, limit
			return result, storeErr
		},
	}})

	t.Run("rows in order with feed links", func(t *testing.T) {
		result, storeErr = timelineItems(3), nil
		rr := getHome(t, s, "/", true)
		body := rr.Body.String()
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if gotUser != "user-1" || gotLimit != 31 || !gotBefore.IsZero() {
			t.Errorf("store called with (%q, %+v, %d)", gotUser, gotBefore, gotLimit)
		}
		i0, i1, i2 := strings.Index(body, "Article 0"), strings.Index(body, "Article 1"), strings.Index(body, "Article 2")
		if i0 < 0 || i0 > i1 || i1 > i2 {
			t.Errorf("articles out of order: %d %d %d", i0, i1, i2)
		}
		if !strings.Contains(body, `href="/feeds/feed-1"`) || !strings.Contains(body, "Feed One") {
			t.Errorf("missing feed link: %s", body)
		}
		if !strings.Contains(body, `<script src="/static/js/app.js" defer></script>`) {
			t.Errorf("missing app.js script tag: %s", body)
		}
		if strings.Contains(body, "Older articles") || strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected pager: %s", body)
		}
	})

	t.Run("31 results gives 30 rows and an older link", func(t *testing.T) {
		result, storeErr = timelineItems(31), nil
		body := getHome(t, s, "/", true).Body.String()
		if got := strings.Count(body, `class="article-row"`); got != 30 {
			t.Errorf("rows = %d, want 30", got)
		}
		want := "/?before=" + url.QueryEscape(encodeCursor(rss.ArticleCursor{At: result[29].Article.TimelineAt, ID: result[29].Article.ID}))
		if !strings.Contains(body, `rel="next"`) || !strings.Contains(body, "Older articles") {
			t.Fatalf("no older link: %s", body)
		}
		// html/template's URL escaping may differ from QueryEscape; compare decoded.
		start := strings.Index(body, `href="/?before=`)
		if start < 0 {
			t.Fatalf("no before link: %s", body)
		}
		href := body[start+len(`href="`):]
		href = href[:strings.Index(href, `"`)]
		gotQ, err := url.ParseRequestURI(href)
		wantQ, _ := url.ParseRequestURI(want)
		if err != nil || gotQ.Query().Get("before") != wantQ.Query().Get("before") {
			t.Errorf("href = %q, want cursor of 30th row %q", href, want)
		}
	})

	t.Run("30 results gives no link", func(t *testing.T) {
		result, storeErr = timelineItems(30), nil
		body := getHome(t, s, "/", true).Body.String()
		if strings.Contains(body, "Older articles") {
			t.Errorf("unexpected older link")
		}
	})

	t.Run("before is decoded and shows back to newest", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		want := rss.ArticleCursor{At: time.Date(2026, 1, 1, 0, 0, 0, 5, time.UTC), ID: "123e4567-e89b-12d3-a456-426614174000"}
		rr := getHome(t, s, "/?before="+url.QueryEscape(encodeCursor(want)), true)
		if !gotBefore.At.Equal(want.At) || gotBefore.ID != want.ID {
			t.Errorf("store before = %+v, want %+v", gotBefore, want)
		}
		if !strings.Contains(rr.Body.String(), "Back to newest") {
			t.Errorf("missing Back to newest")
		}
	})

	t.Run("malformed before is 400", func(t *testing.T) {
		before := calls
		rr := getHome(t, s, "/?before=junk", true)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
		if calls != before {
			t.Errorf("store was called")
		}
	})

	t.Run("empty timeline", func(t *testing.T) {
		result, storeErr = nil, nil
		body := getHome(t, s, "/", true).Body.String()
		if !strings.Contains(body, "Your timeline is empty.") {
			t.Errorf("missing empty message: %s", body)
		}
	})

	t.Run("empty with a cursor", func(t *testing.T) {
		result, storeErr = nil, nil
		cur := encodeCursor(rss.ArticleCursor{At: time.Now(), ID: "123e4567-e89b-12d3-a456-426614174000"})
		body := getHome(t, s, "/?before="+url.QueryEscape(cur), true).Body.String()
		if !strings.Contains(body, "No older articles.") || strings.Contains(body, "Your timeline is empty") {
			t.Errorf("wrong empty message: %s", body)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		result, storeErr = nil, errors.New("boom")
		if rr := getHome(t, s, "/", true); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("read rows are marked", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		var gotUser string
		var gotIDs []string
		s.services.ReadService = readStoreReporting(&gotUser, &gotIDs, result[0].Article.ID)
		t.Cleanup(func() { s.services.ReadService = &fakeReadStore{} })

		rr := getHome(t, s, "/", true)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if gotUser != "user-1" || !slices.Equal(gotIDs, []string{result[0].Article.ID, result[1].Article.ID}) {
			t.Errorf("ReadArticleIDs(%q, %v)", gotUser, gotIDs)
		}
		assertReadMarkers(t, rr.Body.String(), result[0].Article.ID, result[1].Article.ID)
	})

	t.Run("read store error is 500", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		s.services.ReadService = &fakeReadStore{readArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("boom")
		}}
		t.Cleanup(func() { s.services.ReadService = &fakeReadStore{} })

		if rr := getHome(t, s, "/", true); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("logged out gets the public home and skips the store", func(t *testing.T) {
		before := calls
		rr := getHome(t, s, "/", false)
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Timeline") {
			t.Errorf("status %d body %s", rr.Code, rr.Body.String())
		}
		if calls != before {
			t.Errorf("store was called")
		}
	})
}
