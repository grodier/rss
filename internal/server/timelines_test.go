package server

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const (
	testTimelineID  = "55555555-5555-4555-8555-555555555555"
	homeTimelineID  = "66666666-6666-4666-8666-666666666666"
	timelineFeedID  = "77777777-7777-4777-8777-777777777777"
	timelineFeed2ID = "88888888-8888-4888-8888-888888888888"
)

// serveTimeline serves handler at target as user-1, with id as the URL's
// {id} unless it's "". form is posted when not nil. It returns the response
// and the flash left in the session.
func serveTimeline(t *testing.T, s *Server, handler http.HandlerFunc, target, id string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if form != nil {
		req = httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1")
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

// userTimeline is a fakeTimelineStore whose user-1 has t (with ID
// testTimelineID) and a Home row, homeTimelineID.
func userTimeline(t rss.Timeline) *fakeTimelineStore {
	t.ID = testTimelineID
	return &fakeTimelineStore{getFn: func(ctx context.Context, userID, id string) (rss.Timeline, error) {
		switch {
		case userID != "user-1":
			return rss.Timeline{}, rss.ErrNoRecord
		case id == testTimelineID:
			return t, nil
		case id == homeTimelineID:
			return rss.Timeline{ID: homeTimelineID, Home: true}, nil
		}
		return rss.Timeline{}, rss.ErrNoRecord
	}}
}

// timelineSourcesStores returns stores where user-1 has tag testTagID (AI)
// and subscribes to timelineFeedID and timelineFeed2ID.
func timelineSourcesStores() (*fakeTagStore, *fakeSubscriptionStore) {
	tags := userTag()
	tags.listFn = func(ctx context.Context, userID string) ([]rss.Tag, error) {
		return []rss.Tag{{ID: testTagID, Name: "AI"}}, nil
	}
	subs := &fakeSubscriptionStore{listByUserFn: func(ctx context.Context, userID string) ([]rss.Subscription, error) {
		return []rss.Subscription{
			{Feed: rss.Feed{ID: timelineFeedID, Title: "Feed One"}, Site: rss.Site{Host: "one.example"}},
			{Feed: rss.Feed{ID: timelineFeed2ID, Title: "Feed Two"}, Site: rss.Site{Host: "two.example"}},
		}, nil
	}}
	return tags, subs
}

func TestTimelinesHandler(t *testing.T) {
	t.Run("lists timelines with descriptions and edit links", func(t *testing.T) {
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			listFn: func(ctx context.Context, userID string) ([]rss.Timeline, error) {
				if userID != "user-1" {
					return nil, errors.New("wrong user")
				}
				return []rss.Timeline{{ID: testTimelineID, Name: "AI", Description: "Models & more"}, {ID: homeTimelineID, Name: "News"}}, nil
			},
		}})
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>Timelines</h1>",
			`<a class="btn btn-sm btn-primary" href="/timelines/new">New timeline</a>`,
			`<a class="timeline-list-name" href="/timelines/`+testTimelineID+`">AI</a>`,
			"<p>Models &amp; more</p>",
			`<a href="/timelines/`+testTimelineID+`/edit">Edit<span class="visually-hidden"> AI</span></a>`,
			`<a class="timeline-list-name" href="/timelines/`+homeTimelineID+`">News</a>`,
		)
		if strings.Contains(body, "You don't have any timelines yet.") {
			t.Errorf("shows the empty state: %s", body)
		}
	})

	t.Run("empty state", func(t *testing.T) {
		s := newTestServer(t)
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		assertBodyContains(t, rr.Body.String(),
			"You don't have any timelines yet. A timeline shows some of your feeds, for example everything tagged “News”.")
	})

	t.Run("store error is 500", func(t *testing.T) {
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			listFn: func(context.Context, string) ([]rss.Timeline, error) { return nil, errors.New("boom") },
		}})
		if rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func TestTimelineNewHandler(t *testing.T) {
	tags, subs := timelineSourcesStores()
	s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs})

	t.Run("shows all feeds by default", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>New timeline</h1>",
			`<form class="timeline-form" action="/timelines" method="post">`,
			`<input type="radio" name="show" value="all" checked`,
			`<p class="hint" id="description-hint">Shown under the tabs. Optional.</p>`,
			"<legend>Show</legend>",
			"Only feeds with these tags, or these feeds:",
			"<legend>Leave out</legend>",
			`<p class="hint" id="exclude-hint">Feeds left out never show here, even if they have a tag you chose above.</p>`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`">`,
			`<input type="checkbox" name="exclude_tags" value="`+testTagID+`">`,
			`<input type="checkbox" name="include_feeds" value="`+timelineFeedID+`">`,
			`<input type="checkbox" name="exclude_feeds" value="`+timelineFeed2ID+`">`,
			`<details class="feed-picker">`,
			"<summary>Choose feeds</summary>",
			`<a class="btn" href="/timelines">Cancel</a>`,
		)
		for _, bad := range []string{"Delete timeline", " checked>", "autofocus"} {
			if strings.Contains(body, bad) {
				t.Errorf("body has %q: %s", bad, body)
			}
		}
	})

	t.Run("?tag= prefills from one of the user's tags", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?tag="+testTagID, "", nil)
		assertBodyContains(t, rr.Body.String(),
			`<input type="text" id="name" name="name" value="AI"`,
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" checked>`,
		)
	})

	for _, tag := range []string{otherTagID, "not-a-uuid"} {
		t.Run("?tag= ignores "+tag, func(t *testing.T) {
			rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?tag="+tag, "", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			body := rr.Body.String()
			assertBodyContains(t, body, `<input type="radio" name="show" value="all" checked`, `name="name" value=""`)
		})
	}

	t.Run("no tags or feeds", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{}, SubscriptionService: &fakeSubscriptionStore{}})
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new", "", nil)
		body := rr.Body.String()
		assertBodyContains(t, body, `You don't have any tags yet. <a href="/tags">Make a tag</a> to group feeds.`)
		if strings.Contains(body, "Choose feeds") {
			t.Errorf("feed picker shown without subscriptions: %s", body)
		}
	})
}

func TestTimelineCreateHandler(t *testing.T) {
	valid := func() url.Values {
		return url.Values{"name": {"  AI  "}, "description": {" About models "}, "show": {"some"}, "include_tags": {testTagID}}
	}
	type result struct {
		code    int
		flash   string
		body    string
		created *rss.Timeline
	}
	post := func(t *testing.T, form url.Values, storeErr error) result {
		t.Helper()
		var created *rss.Timeline
		tags, subs := timelineSourcesStores()
		s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs, TimelineService: &fakeTimelineStore{
			createFn: func(ctx context.Context, userID string, tl rss.Timeline) (string, error) {
				if userID != "user-1" {
					return "", errors.New("wrong user")
				}
				created = &tl
				return testTimelineID, storeErr
			},
		}})
		rr, flash := serveTimeline(t, s, s.timelineCreateHandler, "/timelines", "", form)
		return result{rr.Code, flash, rr.Body.String(), created}
	}

	t.Run("creates and goes to it", func(t *testing.T) {
		res := post(t, valid(), nil)
		if res.code != http.StatusSeeOther || res.flash != "Timeline created." {
			t.Fatalf("code %d flash %q", res.code, res.flash)
		}
		want := rss.Timeline{Name: "AI", Description: "About models", Filter: rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{testTagID}}}
		if res.created == nil || res.created.Name != want.Name || res.created.Description != want.Description ||
			!res.created.Filter.OnlyIncluded || !slices.Equal(res.created.Filter.IncludeTagIDs, want.Filter.IncludeTagIDs) {
			t.Errorf("created %+v, want %+v", res.created, want)
		}
	})

	t.Run("redirects to the new timeline", func(t *testing.T) {
		tags, subs := timelineSourcesStores()
		s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs, TimelineService: &fakeTimelineStore{
			createFn: func(context.Context, string, rss.Timeline) (string, error) { return testTimelineID, nil },
		}})
		rr, _ := serveTimeline(t, s, s.timelineCreateHandler, "/timelines", "", valid())
		if got := rr.Header().Get("Location"); got != "/timelines/"+testTimelineID {
			t.Errorf("Location = %q", got)
		}
	})

	t.Run("foreign and duplicate IDs are dropped", func(t *testing.T) {
		form := url.Values{
			"name":          {"Mixed"},
			"show":          {"some"},
			"include_tags":  {otherTagID, testTagID},
			"include_feeds": {timelineFeedID, timelineFeedID, "99999999-9999-4999-8999-999999999999"},
			"exclude_tags":  {otherTagID},
			"exclude_feeds": {"junk", timelineFeed2ID},
		}
		res := post(t, form, nil)
		if res.code != http.StatusSeeOther || res.created == nil {
			t.Fatalf("code %d, created %v", res.code, res.created)
		}
		f := res.created.Filter
		if !slices.Equal(f.IncludeTagIDs, []string{testTagID}) || !slices.Equal(f.IncludeFeedIDs, []string{timelineFeedID}) ||
			len(f.ExcludeTagIDs) != 0 || !slices.Equal(f.ExcludeFeedIDs, []string{timelineFeed2ID}) {
			t.Errorf("filter = %+v", f)
		}
	})

	t.Run("show all drops the included sources", func(t *testing.T) {
		form := url.Values{"name": {"Not AI"}, "show": {"all"}, "include_feeds": {timelineFeedID}, "exclude_tags": {testTagID}}
		res := post(t, form, nil)
		if res.created == nil {
			t.Fatalf("not created: %d", res.code)
		}
		f := res.created.Filter
		if f.OnlyIncluded || len(f.IncludeFeedIDs) != 0 || !slices.Equal(f.ExcludeTagIDs, []string{testTagID}) {
			t.Errorf("filter = %+v", f)
		}
	})

	long := strings.Repeat("é", 51)
	tests := []struct {
		name    string
		form    url.Values
		field   string
		message string
	}{
		{"blank name", url.Values{"name": {" \t "}, "show": {"all"}}, "name", "Enter a name."},
		{"name over 50 runes", url.Values{"name": {long}, "show": {"all"}}, "name", "Must be 50 characters or fewer."},
		{"description over 280 runes", url.Values{"name": {"X"}, "description": {strings.Repeat("é", 281)}, "show": {"all"}}, "description", "Must be 280 characters or fewer."},
		{"some with nothing ticked", url.Values{"name": {"X"}, "show": {"some"}, "exclude_tags": {testTagID}}, "show", "Choose at least one tag or feed, or show all your feeds."},
		{"some with only foreign IDs", url.Values{"name": {"X"}, "show": {"some"}, "include_tags": {otherTagID}}, "show", "Choose at least one tag or feed, or show all your feeds."},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is 422", func(t *testing.T) {
			res := post(t, tt.form, nil)
			if res.code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", res.code)
			}
			if res.created != nil {
				t.Errorf("created anyway")
			}
			assertBodyContains(t, res.body,
				`<p class="error" id="`+tt.field+`-error">`+html.EscapeString(tt.message)+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
		})
	}

	t.Run("422 keeps everything", func(t *testing.T) {
		form := url.Values{"name": {""}, "description": {"Kept"}, "show": {"some"}, "include_feeds": {timelineFeedID}, "exclude_tags": {testTagID}}
		res := post(t, form, nil)
		assertBodyContains(t, res.body,
			">Kept</textarea>",
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_feeds" value="`+timelineFeedID+`" checked>`,
			`<input type="checkbox" name="exclude_tags" value="`+testTagID+`" checked>`,
			`<details class="feed-picker" open>`,
		)
	})

	t.Run("too many timelines is a form error", func(t *testing.T) {
		res := post(t, valid(), rss.ErrTooManyTimelines)
		if res.code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", res.code)
		}
		assertBodyContains(t, res.body, `<div class="error" role="alert">You can have up to 20 timelines.</div>`)
	})

	t.Run("bad show is 400", func(t *testing.T) {
		for _, show := range []string{"", "everything"} {
			res := post(t, url.Values{"name": {"X"}, "show": {show}}, nil)
			if res.code != http.StatusBadRequest || res.created != nil {
				t.Errorf("show %q: status %d, created %v", show, res.code, res.created != nil)
			}
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		if res := post(t, valid(), errors.New("boom")); res.code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", res.code)
		}
	})
}

func TestTimelineHandler(t *testing.T) {
	var gotFilter rss.TimelineFilter
	var gotBefore rss.ArticleCursor
	var result []rss.ArticleWithFeed
	articles := &fakeArticleStore{listTimelineFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
		gotFilter, gotBefore = filter, before
		return result, nil
	}}
	filter := rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{testTagID}}
	timelines := userTimeline(rss.Timeline{Name: "AI", Description: "Models & more", Filter: filter})
	timelines.listFn = func(context.Context, string) ([]rss.Timeline, error) {
		return []rss.Timeline{{ID: testTimelineID, Name: "AI"}}, nil
	}
	s := newTestServerWith(t, Services{ArticleService: articles, TimelineService: timelines, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}})
	get := func(t *testing.T, target, id string) *httptest.ResponseRecorder {
		t.Helper()
		rr, _ := serveTimeline(t, s, s.timelineHandler, target, id, nil)
		return rr
	}

	t.Run("rows, heading, description and tabs", func(t *testing.T) {
		result = timelineItems(2)
		rr := get(t, "/timelines/"+testTimelineID, testTimelineID)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if !slices.Equal(gotFilter.IncludeTagIDs, filter.IncludeTagIDs) || !gotFilter.OnlyIncluded || !gotBefore.IsZero() {
			t.Errorf("ListTimeline got filter %+v, before %+v", gotFilter, gotBefore)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>AI</h1>",
			`<div class="timeline-about"><p>Models &amp; more</p></div>`,
			`<a href="/timelines/`+testTimelineID+`/edit">Edit</a>`,
			`<a href="/timelines/`+testTimelineID+`" aria-current="page">AI</a>`,
			`<li><a href="/">Home</a></li>`,
			"Article 0", "Article 1",
		)
		if strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected Back to newest")
		}
	})

	t.Run("pager links to the timeline", func(t *testing.T) {
		result = timelineItems(31)
		body := get(t, "/timelines/"+testTimelineID, testTimelineID).Body.String()
		assertBodyContains(t, body, `rel="next" href="/timelines/`+testTimelineID+`?before=`)
	})

	t.Run("a later page", func(t *testing.T) {
		result = nil
		cur := encodeCursor(rss.ArticleCursor{At: time.Now(), ID: "123e4567-e89b-12d3-a456-426614174000"})
		body := get(t, "/timelines/"+testTimelineID+"?before="+url.QueryEscape(cur), testTimelineID).Body.String()
		assertBodyContains(t, body, "No older articles.", `<a href="/timelines/`+testTimelineID+`">Back to newest</a>`)
		if gotBefore.IsZero() {
			t.Errorf("before not passed on")
		}
	})

	t.Run("empty", func(t *testing.T) {
		result = nil
		body := get(t, "/timelines/"+testTimelineID, testTimelineID).Body.String()
		assertBodyContains(t, body, "Nothing in this timeline yet.")
		if strings.Contains(body, "Your timeline is empty") || strings.Contains(body, "no feeds to show") {
			t.Errorf("wrong empty state: %s", body)
		}
	})

	t.Run("no sources", func(t *testing.T) {
		result = nil
		s := newTestServerWith(t, Services{ArticleService: articles, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{},
			TimelineService: userTimeline(rss.Timeline{Name: "AI", Filter: rss.TimelineFilter{OnlyIncluded: true, ExcludeTagIDs: []string{testTagID}}})})
		rr, _ := serveTimeline(t, s, s.timelineHandler, "/timelines/"+testTimelineID, testTimelineID, nil)
		assertBodyContains(t, rr.Body.String(),
			`This timeline has no feeds to show. <a href="/timelines/`+testTimelineID+`/edit">Edit it</a> to choose some.`)
	})

	for _, tt := range []struct{ name, id string }{
		{"a bad ID", "not-a-uuid"},
		{"another user's", otherTagID},
		{"the Home row", homeTimelineID},
	} {
		t.Run(tt.name+" is 404", func(t *testing.T) {
			if rr := get(t, "/timelines/"+tt.id, tt.id); rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
		})
	}

	t.Run("malformed before is 400", func(t *testing.T) {
		if rr := get(t, "/timelines/"+testTimelineID+"?before=junk", testTimelineID); rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})
}

func TestTimelineEditHandler(t *testing.T) {
	tags, subs := timelineSourcesStores()
	timelines := userTimeline(rss.Timeline{Name: "AI", Description: "Models", Filter: rss.TimelineFilter{
		OnlyIncluded: true, IncludeTagIDs: []string{testTagID}, ExcludeFeedIDs: []string{timelineFeed2ID},
	}})
	s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs, TimelineService: timelines})

	t.Run("shows the timeline's settings", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineEditHandler, "/timelines/"+testTimelineID+"/edit", testTimelineID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		assertBodyContains(t, rr.Body.String(),
			"<h1>Edit timeline</h1>",
			`action="/timelines/`+testTimelineID+`/edit"`,
			`name="name" value="AI"`,
			">Models</textarea>",
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" checked>`,
			`<input type="checkbox" name="exclude_feeds" value="`+timelineFeed2ID+`" checked>`,
			`<a class="btn" href="/timelines/`+testTimelineID+`">Cancel</a>`,
			`<form class="tag-delete" action="/timelines/`+testTimelineID+`/delete" method="post">`,
			`<button class="btn-sm" type="submit">Delete timeline</button>`,
		)
	})

	for _, id := range []string{"junk", otherTagID, homeTimelineID} {
		t.Run(id+" is 404", func(t *testing.T) {
			if rr, _ := serveTimeline(t, s, s.timelineEditHandler, "/", id, nil); rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
		})
	}
}

func TestTimelineEditPostHandler(t *testing.T) {
	var updated *rss.Timeline
	tags, subs := timelineSourcesStores()
	timelines := userTimeline(rss.Timeline{Name: "AI"})
	timelines.updateFn = func(ctx context.Context, userID string, tl rss.Timeline) error {
		updated = &tl
		return nil
	}
	s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs, TimelineService: timelines})

	t.Run("saves and goes to it", func(t *testing.T) {
		updated = nil
		form := url.Values{"name": {"Not AI"}, "show": {"all"}, "exclude_tags": {testTagID}}
		rr, flash := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form)
		if rr.Code != http.StatusSeeOther || flash != "Timeline saved." || rr.Header().Get("Location") != "/timelines/"+testTimelineID {
			t.Fatalf("code %d flash %q location %q", rr.Code, flash, rr.Header().Get("Location"))
		}
		if updated == nil || updated.ID != testTimelineID || updated.Name != "Not AI" || updated.Filter.OnlyIncluded ||
			!slices.Equal(updated.Filter.ExcludeTagIDs, []string{testTagID}) {
			t.Errorf("updated %+v", updated)
		}
	})

	t.Run("invalid is 422 on the edit form", func(t *testing.T) {
		updated = nil
		rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, url.Values{"name": {""}, "show": {"all"}})
		if rr.Code != http.StatusUnprocessableEntity || updated != nil {
			t.Fatalf("status %d, updated %v", rr.Code, updated)
		}
		assertBodyContains(t, rr.Body.String(), `action="/timelines/`+testTimelineID+`/edit"`, "Enter a name.", "Delete timeline")
	})

	t.Run("another user's is 404", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", otherTagID, url.Values{"name": {"X"}, "show": {"all"}})
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("gone by the time it's saved is 404", func(t *testing.T) {
		timelines.updateFn = func(context.Context, string, rss.Timeline) error { return rss.ErrNoRecord }
		t.Cleanup(func() { timelines.updateFn = nil })
		rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, url.Values{"name": {"X"}, "show": {"all"}})
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}

func TestTimelineDeleteHandler(t *testing.T) {
	var deleted string
	timelines := userTimeline(rss.Timeline{Name: "AI"})
	timelines.deleteFn = func(ctx context.Context, userID, id string) error {
		deleted = id
		return nil
	}
	s := newTestServerWith(t, Services{TimelineService: timelines})

	rr, flash := serveTimeline(t, s, s.timelineDeleteHandler, "/", testTimelineID, url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/timelines" {
		t.Fatalf("code %d location %q", rr.Code, rr.Header().Get("Location"))
	}
	if deleted != testTimelineID || flash != "Deleted the timeline “AI”." {
		t.Errorf("deleted %q, flash %q", deleted, flash)
	}

	for _, id := range []string{"junk", otherTagID, homeTimelineID} {
		t.Run(id+" is 404", func(t *testing.T) {
			deleted = ""
			rr, _ := serveTimeline(t, s, s.timelineDeleteHandler, "/", id, url.Values{})
			if rr.Code != http.StatusNotFound || deleted != "" {
				t.Errorf("status = %d, deleted %q", rr.Code, deleted)
			}
		})
	}
}

func TestTagPageMakeTimelineLink(t *testing.T) {
	s := newTestServerWith(t, Services{TagService: userTag(), SubscriptionService: &fakeSubscriptionStore{}})
	rr, _ := serveTag(t, s, s.tagHandler, testTagID, nil)
	assertBodyContains(t, rr.Body.String(), `<a href="/timelines/new?tag=`+testTagID+`">Make a timeline</a>`)
}
