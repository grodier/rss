package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

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
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
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
		if !strings.Contains(body, `<script src="/static/js/app.js?v=`+jsVersion+`" defer></script>`) {
			t.Errorf("missing app.js script tag: %s", body)
		}
		if strings.Contains(body, "Older articles") || strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected pager: %s", body)
		}
	})

	t.Run("nav shows the user's monogram", func(t *testing.T) {
		result, storeErr = timelineItems(1), nil
		sess := rss.Session{ID: "session-1", User: rss.User{ID: "user-1", Name: "zoë"}}
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.homeHandler)).ServeHTTP(rr, requestWithSession(sess))
		want := `aria-hidden="true">Z</span><span>Settings</span>`
		if body := rr.Body.String(); !strings.Contains(body, want) {
			t.Errorf("missing %s in: %s", want, body)
		}
	})

	t.Run("authors follow the feed name", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		result[0].Article.Authors = []string{"Ada"}
		body := getHome(t, s, "/", true).Body.String()
		want := regexp.MustCompile(`Feed One</a>\s*<span aria-hidden="true">·</span><span class="article-author">Ada</span>`)
		if !want.MatchString(body) {
			t.Errorf("missing %s in: %s", want, body)
		}
		if got := strings.Count(body, "article-author"); got != 1 {
			t.Errorf("article-author appears %d times, want 1 (only the row with authors)", got)
		}
	})

	t.Run("also in links each other feed", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		result[0].AlsoIn = []rss.Feed{{ID: "feed-2", Title: "Feed Two"}, {ID: "feed-3", Url: "https://example.com/three.xml"}}
		body := getHome(t, s, "/", true).Body.String()
		want := `<p class="article-also-in">Also in: <a href="/feeds/feed-2">Feed Two</a>, <a href="/feeds/feed-3">https://example.com/three.xml</a></p>`
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in: %s", want, body)
		}
		if got := strings.Count(body, "Also in:"); got != 1 {
			t.Errorf(`"Also in:" appears %d times, want 1 (only the row with AlsoIn)`, got)
		}
	})

	t.Run("page values fill gaps", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		result[0].Article.Title = ""
		result[0].Article.PageTitle = "Page title"
		result[0].Article.PageImageURL = "https://example.com/page.jpg"
		result[0].Article.PageDescription = "Page description"
		result[1].Article.ImageURL = "https://example.com/feed.jpg"
		result[1].Article.Excerpt = "Feed excerpt"
		result[1].Article.PageTitle = "Other title"
		result[1].Article.PageImageURL = "https://example.com/other.jpg"
		result[1].Article.PageDescription = "Other description"
		body := getHome(t, s, "/", true).Body.String()
		for _, want := range []string{
			`src="https://example.com/page.jpg"`,
			">Page title</a>",
			`<p class="article-excerpt">Page description</p>`,
			`src="https://example.com/feed.jpg"`,
			">Article 1</a>",
			`<p class="article-excerpt">Feed excerpt</p>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %s in: %s", want, body)
			}
		}
		for _, bad := range []string{"other.jpg", "Other title", "Other description"} {
			if strings.Contains(body, bad) {
				t.Errorf("page value %q shown over the feed's: %s", bad, body)
			}
		}
	})

	t.Run("no also in renders nothing", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		body := getHome(t, s, "/", true).Body.String()
		if strings.Contains(body, "Also in") || strings.Contains(body, "article-also-in") {
			t.Errorf("unexpected Also in: %s", body)
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
		assertBodyContains(t, body,
			"Your timeline is empty.",
			`<a href="/search">Search</a>`,
			`<a href="/settings/import">import your subscriptions</a>`,
		)
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

	t.Run("saved rows show a pressed Save button", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		var gotUser string
		var gotIDs []string
		s.services.SaveService = saveStoreReporting(&gotUser, &gotIDs, result[0].Article.ID)
		t.Cleanup(func() { s.services.SaveService = &fakeSaveStore{} })

		cur := encodeCursor(rss.ArticleCursor{At: time.Now(), ID: "123e4567-e89b-12d3-a456-426614174000"})
		target := "/?before=" + url.QueryEscape(cur)
		rr := getHome(t, s, target, true)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if gotUser != "user-1" || !slices.Equal(gotIDs, []string{result[0].Article.ID, result[1].Article.ID}) {
			t.Errorf("SavedArticleIDs(%q, %v)", gotUser, gotIDs)
		}
		assertSaveButtons(t, rr.Body.String(), target, result[0].Article.ID, result[1].Article.ID)
	})

	t.Run("starred rows show a pressed Star button", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		var gotUser string
		var gotIDs []string
		s.services.StarService = starStoreReporting(&gotUser, &gotIDs, result[1].Article.ID)
		t.Cleanup(func() { s.services.StarService = &fakeStarStore{} })

		rr := getHome(t, s, "/", true)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if gotUser != "user-1" || !slices.Equal(gotIDs, []string{result[0].Article.ID, result[1].Article.ID}) {
			t.Errorf("StarredArticleIDs(%q, %v)", gotUser, gotIDs)
		}
		assertStarButtons(t, rr.Body.String(), "/", result[1].Article.ID, result[0].Article.ID)
	})

	t.Run("star store error is 500", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		s.services.StarService = &fakeStarStore{starredArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("boom")
		}}
		t.Cleanup(func() { s.services.StarService = &fakeStarStore{} })

		if rr := getHome(t, s, "/", true); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("save store error is 500", func(t *testing.T) {
		result, storeErr = timelineItems(2), nil
		s.services.SaveService = &fakeSaveStore{savedArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return nil, errors.New("boom")
		}}
		t.Cleanup(func() { s.services.SaveService = &fakeSaveStore{} })

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

func TestHomeHandlerSiteIcons(t *testing.T) {
	items := timelineItems(2)
	for i := range items {
		items[i].Feed.SiteID = testSiteID
	}
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, SiteService: &fakeSiteStore{},
		ArticleService: &fakeArticleStore{
			listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
				return items, nil
			},
		}})
	testSiteIcons(t, s, testSiteID, "F", func(t *testing.T) string {
		rr := getHome(t, s, "/", true)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	})
}

func TestHomeHandlerTabs(t *testing.T) {
	var gotFilter *rss.TimelineFilter
	articles := &fakeArticleStore{listTimelineFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
		gotFilter = &filter
		return timelineItems(1), nil
	}}
	timelines := &fakeTimelineStore{}
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: articles, TimelineService: timelines})

	// app.js scrolls the list in .timeline-tabs to its [aria-current=page] link.
	t.Run("no timelines offers to make one", func(t *testing.T) {
		body := getHome(t, s, "/", true).Body.String()
		assertBodyContains(t, body,
			`<header class="page-head tabs-head">
        <h1 class="visually-hidden">Home</h1>`,
			`<nav class="timeline-tabs" aria-label="Timelines">`,
			`<li><a href="/" aria-current="page">Home</a></li>`,
			`<li class="tab-manage"><a href="/timelines/new">New timeline</a></li>`,
		)
		if gotFilter == nil || gotFilter.OnlyIncluded || gotFilter.IncludeTagIDs != nil || gotFilter.IncludeFeedIDs != nil ||
			gotFilter.ExcludeTagIDs != nil || gotFilter.ExcludeFeedIDs != nil {
			t.Errorf("ListTimeline filter = %+v, want the zero filter", gotFilter)
		}
	})

	t.Run("lists the timelines in order with Home current", func(t *testing.T) {
		timelines.listFn = func(ctx context.Context, userID string) ([]rss.Timeline, error) {
			if userID != "user-1" {
				return nil, errors.New("wrong user")
			}
			return []rss.Timeline{{ID: "t-1", Name: "AI"}, {ID: "t-2", Name: "News & views"}}, nil
		}
		t.Cleanup(func() { timelines.listFn = nil })
		body := getHome(t, s, "/", true).Body.String()
		assertBodyContains(t, body,
			`<li><a href="/" aria-current="page">Home</a></li>`,
			`<li><a href="/timelines/t-1">AI</a></li>`,
			`<li><a href="/timelines/t-2">News &amp; views</a></li>`,
			`<li class="tab-manage"><a href="/timelines">Timelines</a></li>`,
		)
		if strings.Index(body, ">AI<") > strings.Index(body, ">News &amp; views<") {
			t.Errorf("tabs out of order: %s", body)
		}
		if strings.Contains(body, `href="/timelines/new"`) {
			t.Errorf("New timeline link shown with timelines: %s", body)
		}
	})

	t.Run("tab store error is 500", func(t *testing.T) {
		timelines.listFn = func(context.Context, string) ([]rss.Timeline, error) { return nil, errors.New("boom") }
		t.Cleanup(func() { timelines.listFn = nil })
		if rr := getHome(t, s, "/", true); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

// dividerItems returns timeline items at the given positions, titled
// "Article {hour}:{minute}".
func dividerItems(at ...time.Time) []rss.ArticleWithFeed {
	items := make([]rss.ArticleWithFeed, len(at))
	for i, t := range at {
		items[i] = rss.ArticleWithFeed{
			Article: rss.Article{ID: fmt.Sprintf(timelineUUID, i), Title: "Article " + t.Format("15:04"), TimelineAt: t},
			Feed:    rss.Feed{ID: "feed-1", Title: "Feed One"},
		}
	}
	return items
}

// dividerAbove returns the titles of the rows the "You were here" line is
// right above in body, in order.
func dividerAbove(t *testing.T, body string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var above []string
	for _, d := range findAll(doc, func(n *html.Node) bool { return hasClass(n, "timeline-divider") }) {
		next := d.NextSibling
		for next != nil && next.Type != html.ElementNode {
			next = next.NextSibling
		}
		if next == nil || !hasClass(next, "article-row") {
			t.Errorf("the divider isn't followed by a row")
			continue
		}
		titles := findAll(next, func(n *html.Node) bool { return hasClass(n, "article-title") })
		if len(titles) == 0 {
			t.Fatalf("row without .article-title")
		}
		var text strings.Builder
		for c := range titles[0].Descendants() {
			if c.Type == html.TextNode {
				text.WriteString(c.Data)
			}
		}
		above = append(above, strings.TrimSpace(text.String()))
	}
	return above
}

func TestTimelineDivider(t *testing.T) {
	at := func(hour, min int) time.Time { return time.Date(2026, 1, 1, hour, min, 0, 0, time.UTC) }
	var items []rss.ArticleWithFeed
	var mark rss.TimelineMark
	var markErr error
	markCalls := 0
	reads := &fakeReadStore{timelineMarkFn: func(ctx context.Context, userID string) (rss.TimelineMark, error) {
		markCalls++
		if userID != "user-1" {
			return rss.TimelineMark{}, errors.New("wrong user")
		}
		return mark, markErr
	}}
	articles := &fakeArticleStore{listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
		return items, nil
	}}
	s := newTestServerWith(t, Services{ReadService: reads, SaveService: &fakeSaveStore{}, ArticleService: articles})
	// withDivider is a mark whose current visit's divider is d.
	withDivider := func(d time.Time) rss.TimelineMark {
		return rss.TimelineMark{SeenUpTo: at(10, 0), SeenAt: time.Now().Add(-10 * time.Minute), DividerAt: d}
	}

	tests := []struct {
		name    string
		target  string
		items   []rss.ArticleWithFeed
		mark    rss.TimelineMark
		markErr error
		want    []string
	}{
		{"above the first row at or before the divider", "/", dividerItems(at(10, 0), at(9, 0), at(8, 0)), withDivider(at(9, 0)), nil, []string{"Article 09:00"}},
		{"nothing new", "/", dividerItems(at(10, 0), at(9, 0), at(8, 0)), withDivider(at(10, 30)), nil, nil},
		{"all new on this page", "/", dividerItems(at(10, 0), at(9, 0), at(8, 0)), withDivider(at(7, 0)), nil, nil},
		{"a later page whose cursor is newer", "/?before=" + encodeCursor(rss.ArticleCursor{At: at(8, 0), ID: fmt.Sprintf(timelineUUID, 2)}),
			dividerItems(at(7, 30), at(7, 0), at(6, 0)), withDivider(at(7, 0)), nil, []string{"Article 07:00"}},
		{"a later page starting at the divider", "/?before=" + encodeCursor(rss.ArticleCursor{At: at(8, 0), ID: fmt.Sprintf(timelineUUID, 2)}),
			dividerItems(at(7, 0), at(6, 0)), withDivider(at(7, 0)), nil, []string{"Article 07:00"}},
		{"first visit", "/", dividerItems(at(10, 0), at(9, 0)), rss.TimelineMark{SeenUpTo: at(10, 0), SeenAt: time.Now()}, nil, nil},
		{"a new visit moves it to where the last one ended", "/", dividerItems(at(11, 0), at(10, 0), at(9, 0)),
			rss.TimelineMark{SeenUpTo: at(10, 0), SeenAt: time.Now().Add(-2 * time.Hour), DividerAt: at(9, 0)}, nil, []string{"Article 10:00"}},
		{"no mark", "/", dividerItems(at(10, 0), at(9, 0)), rss.TimelineMark{}, nil, nil},
		{"store error", "/", dividerItems(at(10, 0), at(9, 0)), rss.TimelineMark{}, errors.New("db down"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, mark, markErr = tt.items, tt.mark, tt.markErr
			rr := getHome(t, s, tt.target, true)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if got := dividerAbove(t, rr.Body.String()); !slices.Equal(got, tt.want) {
				t.Errorf("divider above %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("starred-only timeline", func(t *testing.T) {
		markCalls = 0
		stars := &fakeStarStore{listStarredFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.StarredArticle, error) {
			return starredItems(3), nil
		}}
		s := newTestServerWith(t, Services{ReadService: reads, SaveService: &fakeSaveStore{}, StarService: stars,
			TimelineService: userTimeline(rss.Timeline{Name: "Stars", Filter: rss.TimelineFilter{StarredOnly: true}})})
		mark = withDivider(time.Now().Add(-24 * time.Hour))
		rr, _ := serveTimeline(t, s, s.timelineHandler, "/timelines/"+testTimelineID, testTimelineID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if got := dividerAbove(t, rr.Body.String()); got != nil {
			t.Errorf("divider above %q, want none", got)
		}
		if markCalls != 0 {
			t.Errorf("TimelineMark called %d times, want 0", markCalls)
		}
	})
}

func TestTimelineSeenUpTo(t *testing.T) {
	first := time.Date(2026, 1, 1, 10, 0, 0, 123456789, time.FixedZone("EST", -5*3600))
	var items []rss.ArticleWithFeed
	articles := &fakeArticleStore{listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
		return items, nil
	}}
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: articles})
	seenUpTo := func(t *testing.T, body string) (string, bool) {
		t.Helper()
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		lists := findAll(doc, func(n *html.Node) bool { return hasClass(n, "article-list") && hasAttr(n, "data-seen-up-to") })
		if len(lists) == 0 {
			return "", false
		}
		return attr(lists[0], "data-seen-up-to"), true
	}

	t.Run("Home's first page", func(t *testing.T) {
		items = dividerItems(first, first.Add(-time.Hour))
		got, ok := seenUpTo(t, getHome(t, s, "/", true).Body.String())
		if want := "2026-01-01T15:00:00.123456789Z"; !ok || got != want {
			t.Errorf("data-seen-up-to = %q (present: %v), want %q", got, ok, want)
		}
	})

	t.Run("a later page", func(t *testing.T) {
		items = dividerItems(first, first.Add(-time.Hour))
		target := "/?before=" + encodeCursor(rss.ArticleCursor{At: first.Add(time.Hour), ID: fmt.Sprintf(timelineUUID, 9)})
		if got, ok := seenUpTo(t, getHome(t, s, target, true).Body.String()); ok {
			t.Errorf("data-seen-up-to = %q, want none", got)
		}
	})

	t.Run("an empty timeline", func(t *testing.T) {
		items = nil
		body := getHome(t, s, "/", true).Body.String()
		if strings.Contains(body, "data-seen-up-to") {
			t.Errorf("body has data-seen-up-to: %s", body)
		}
	})

	t.Run("a starred-only timeline", func(t *testing.T) {
		stars := &fakeStarStore{listStarredFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.StarredArticle, error) {
			return starredItems(2), nil
		}}
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, StarService: stars,
			TimelineService: userTimeline(rss.Timeline{Name: "Stars", Filter: rss.TimelineFilter{StarredOnly: true}})})
		rr, _ := serveTimeline(t, s, s.timelineHandler, "/timelines/"+testTimelineID, testTimelineID, nil)
		if got, ok := seenUpTo(t, rr.Body.String()); ok {
			t.Errorf("data-seen-up-to = %q, want none", got)
		}
	})
}

func postTimelineSeen(t *testing.T, s *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/timeline/seen", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1"))
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.timelineSeenHandler)).ServeHTTP(rr, req)
	return rr
}

func TestTimelineSeenHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotUser string
		var gotUpTo time.Time
		calls := 0
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markSeenFn: func(ctx context.Context, userID string, upTo time.Time) error {
			calls++
			gotUser, gotUpTo = userID, upTo
			return nil
		}}})

		rr := postTimelineSeen(t, s, url.Values{"up_to": {"2026-01-01T15:00:00.123456789Z"}})

		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
		}
		want := time.Date(2026, 1, 1, 15, 0, 0, 123456789, time.UTC)
		if calls != 1 || gotUser != "user-1" || !gotUpTo.Equal(want) {
			t.Errorf("MarkSeen called %d times with (%q, %v), want once with (user-1, %v)", calls, gotUser, gotUpTo, want)
		}
	})

	for _, tt := range []struct {
		name string
		form url.Values
	}{
		{"missing up_to", url.Values{}},
		{"invalid up_to", url.Values{"up_to": {"yesterday"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markSeenFn: func(context.Context, string, time.Time) error {
				calls++
				return nil
			}}})
			rr := postTimelineSeen(t, s, tt.form)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
			if calls != 0 {
				t.Errorf("MarkSeen called %d times, want 0", calls)
			}
		})
	}

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{ReadService: &fakeReadStore{markSeenFn: func(context.Context, string, time.Time) error {
			return errors.New("db down")
		}}})
		rr := postTimelineSeen(t, s, url.Values{"up_to": {"2026-01-01T15:00:00Z"}})
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}
