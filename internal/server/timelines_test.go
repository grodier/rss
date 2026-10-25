package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const (
	testTimelineID  = "55555555-5555-4555-8555-555555555555"
	newsTimelineID  = "66666666-6666-4666-8666-666666666666"
	timelineFeedID  = "77777777-7777-4777-8777-777777777777"
	timelineFeed2ID = "88888888-8888-4888-8888-888888888888"
)

// serveTimeline serves handler at target as user-1, with id as the URL's
// {id} unless it's "". form is posted when not nil. It returns the response
// and the flash left in the session.
func serveTimeline(t *testing.T, s *Server, handler http.HandlerFunc, target, id string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return serveTimelineSession(t, s, nil, handler, target, id, form)
}

// serveTimelineSession is serveTimeline with sess (user-1's) as the
// logged-in session, when it isn't nil.
func serveTimelineSession(t *testing.T, s *Server, sess *rss.Session, handler http.HandlerFunc, target, id string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if form != nil {
		req = httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(req.Context(), authenticatedUserIDContextKey, "user-1")
	if sess != nil {
		ctx = context.WithValue(ctx, authenticatedSessionContextKey, *sess)
	}
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
// testTimelineID).
func userTimeline(t rss.Timeline) *fakeTimelineStore {
	t.ID = testTimelineID
	return &fakeTimelineStore{getFn: func(ctx context.Context, userID, id string) (rss.Timeline, error) {
		switch {
		case userID != "user-1":
			return rss.Timeline{}, rss.ErrNoRecord
		case id == testTimelineID:
			return t, nil
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
				return []rss.Timeline{{ID: testTimelineID, Name: "AI", Description: "Models & more", ShareToken: testShareToken}, {ID: newsTimelineID, Name: "News"}}, nil
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
			`<a class="timeline-list-name" href="/timelines/`+newsTimelineID+`">News</a>`,
		)
		if strings.Contains(body, "You don't have any timelines yet.") {
			t.Errorf("shows the empty state: %s", body)
		}
		// Only AI is shared.
		if n := strings.Count(body, `<span class="chip">Shared</span>`); n != 1 {
			t.Errorf("%d timelines marked Shared, want 1: %s", n, body)
		}
		assertBodyContains(t, body, `<a class="timeline-list-name" href="/timelines/`+testTimelineID+`">AI</a>
            <span class="chip">Shared</span>`)
	})

	t.Run("move buttons", func(t *testing.T) {
		const thirdID = "99999999-9999-4999-8999-999999999999"
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			listFn: func(ctx context.Context, userID string) ([]rss.Timeline, error) {
				return []rss.Timeline{{ID: testTimelineID, Name: "AI"}, {ID: newsTimelineID, Name: "News"}, {ID: thirdID, Name: "Sports"}}, nil
			},
		}})
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		body := rr.Body.String()
		up := func(name string) string {
			return `<button class="btn-sm" type="submit" name="direction" value="up" aria-label="Move ` + name + ` up">Move up</button>`
		}
		down := func(name string) string {
			return `<button class="btn-sm" type="submit" name="direction" value="down" aria-label="Move ` + name + ` down">Move down</button>`
		}
		assertBodyContains(t, body,
			`<li id="timeline-`+testTimelineID+`">`,
			`<form class="timeline-move" action="/timelines/`+testTimelineID+`/move" method="post">`,
			down("AI"),
			up("News"), down("News"),
			up("Sports"),
		)
		for _, unwanted := range []string{up("AI"), down("Sports")} {
			if strings.Contains(body, unwanted) {
				t.Errorf("body has %s", unwanted)
			}
		}
	})

	t.Run("no move buttons for one timeline", func(t *testing.T) {
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			listFn: func(ctx context.Context, userID string) ([]rss.Timeline, error) {
				return []rss.Timeline{{ID: testTimelineID, Name: "AI"}}, nil
			},
		}})
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		if body := rr.Body.String(); strings.Contains(body, "timeline-move") {
			t.Errorf("body has move buttons: %s", body)
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

	t.Run("offers the presets", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new", "", nil)
		body := rr.Body.String()
		assertBodyContains(t, body,
			`<span id="presets-label">Start from:</span>`,
			`<li><a href="/timelines/new?preset=starred">Starred</a></li>`,
			`<li><a href="/timelines/new?preset=quiet">Quiet posters</a></li>`,
			"<legend>Only show</legend>",
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" />`,
			`<p class="hint" id="starred-only-hint">Newest starred first. Stars stay here after you unsubscribe from a feed, unless you chose tags or feeds above.</p>`,
			`<label for="max_posts_per_week">Feeds that post at most</label>`,
			`<input type="number" id="max_posts_per_week" name="max_posts_per_week" min="1" max="100" inputmode="numeric"`,
			`value=""`,
			`aria-describedby="max_posts_per_week-hint"`,
			"times a week",
			`<p class="hint" id="max_posts_per_week-hint">Based on the last 30 days. My feeds shows each feed's rate.</p>`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" />`,
			"Articles with audio or video",
			`<p class="hint" id="media-only-hint">Podcast episodes and videos attached to the post.</p>`,
		)
	})

	t.Run("?preset=quiet prefills Quiet posters", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?preset=quiet", "", nil)
		assertBodyContains(t, rr.Body.String(),
			`<input type="text" id="name" name="name" value="Quiet posters"`,
			`<input type="radio" name="show" value="all" checked`,
			`value="2"`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" />`,
		)
	})

	t.Run("?preset=starred prefills Starred", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?preset=starred", "", nil)
		assertBodyContains(t, rr.Body.String(),
			`<input type="text" id="name" name="name" value="Starred"`,
			`<input type="radio" name="show" value="all" checked`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`,
		)
	})

	t.Run("?preset=starred with ?tag= is named after the tag", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?preset=starred&tag="+testTagID, "", nil)
		assertBodyContains(t, rr.Body.String(),
			`<input type="text" id="name" name="name" value="AI"`,
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" checked>`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`,
		)
	})

	t.Run("an unknown preset is ignored", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new?preset=junk", "", nil)
		body := rr.Body.String()
		assertBodyContains(t, body, `name="name" value=""`, `<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" />`)
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

	t.Run("saves the posting rate and audio or video", func(t *testing.T) {
		for _, tt := range []struct {
			value string
			want  int
		}{{"", 0}, {"2", 2}, {" 100 ", 100}} {
			form := url.Values{"name": {"Quiet"}, "show": {"all"}, "max_posts_per_week": {tt.value}, "media_only": {"1"}}
			res := post(t, form, nil)
			if res.code != http.StatusSeeOther || res.created == nil {
				t.Fatalf("%q: code %d, created %v", tt.value, res.code, res.created)
			}
			if f := res.created.Filter; f.MaxPostsPerWeek != tt.want || !f.MediaOnly {
				t.Errorf("%q: filter = %+v, want MaxPostsPerWeek %d and MediaOnly", tt.value, f, tt.want)
			}
		}
	})

	t.Run("saves the words and phrases", func(t *testing.T) {
		form := url.Values{"name": {"AI"}, "show": {"all"},
			"include_words": {" AI, llm ,, machine \t learning, ai, C++ "}, "exclude_words": {"Sponsored"}}
		res := post(t, form, nil)
		if res.code != http.StatusSeeOther || res.created == nil {
			t.Fatalf("code %d, created %v", res.code, res.created)
		}
		f := res.created.Filter
		if !slices.Equal(f.IncludeWords, []string{"AI", "llm", "machine learning", "C++"}) || !slices.Equal(f.ExcludeWords, []string{"Sponsored"}) {
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
	for _, rate := range []string{"0", "101", "2.5", "abc", "-1"} {
		tests = append(tests, struct {
			name    string
			form    url.Values
			field   string
			message string
		}{"posts a week " + rate, url.Values{"name": {"X"}, "show": {"all"}, "max_posts_per_week": {rate}}, "max_posts_per_week", "Enter a whole number from 1 to 100, or leave it blank."})
	}
	for _, field := range []string{"include_words", "exclude_words"} {
		for _, tt := range []struct{ name, value, message string }{
			{"21 terms", numberedTerms(21), "At most 20 words or phrases."},
			{"a term over 50 runes", "AI, " + strings.Repeat("é", 51), "Each word or phrase must be 50 characters or fewer."},
			{"+++", "AI, +++", "Each word or phrase needs at least one letter or number."},
			{"--", "--", "Each word or phrase needs at least one letter or number."},
		} {
			tests = append(tests, struct {
				name    string
				form    url.Values
				field   string
				message string
			}{field + " " + tt.name, url.Values{"name": {"X"}, "show": {"all"}, field: {tt.value}}, field, tt.message})
		}
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
		form := url.Values{"name": {""}, "description": {"Kept"}, "show": {"some"}, "include_feeds": {timelineFeedID}, "exclude_tags": {testTagID},
			"max_posts_per_week": {"7"}, "media_only": {"1"}, "include_words": {"AI,  C++"}, "exclude_words": {"deal"}}
		res := post(t, form, nil)
		assertBodyContains(t, res.body,
			">Kept</textarea>",
			`name="include_words" value="AI,  C&#43;&#43;"`,
			`name="exclude_words" value="deal"`,
			`value="7"`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" checked />`,
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
			`<h1 class="visually-hidden">AI</h1>`,
			"<p>Models &amp; more</p>",
			`<a href="/timelines/`+testTimelineID+`/edit">Edit<span class="visually-hidden"> AI</span></a>`,
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

// starredItems returns n starred articles, newest starred first, starred an
// hour apart and long after they arrived.
func starredItems(n int) []rss.StarredArticle {
	items := make([]rss.StarredArticle, n)
	for i, item := range timelineItems(n) {
		items[i] = rss.StarredArticle{
			Article:   item.Article,
			Feed:      item.Feed,
			StarredAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Hour),
		}
	}
	return items
}

func TestStarredTimeline(t *testing.T) {
	var gotFilter rss.TimelineFilter
	var gotBefore rss.ArticleCursor
	var result []rss.StarredArticle
	stars := &fakeStarStore{
		listStarredFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.StarredArticle, error) {
			if userID != "user-1" {
				return nil, errors.New("wrong user")
			}
			gotFilter, gotBefore = filter, before
			return result, nil
		},
	}
	articles := &fakeArticleStore{listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
		return nil, errors.New("ListTimeline called for a starred-only timeline")
	}}
	filter := rss.TimelineFilter{StarredOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{testTagID}}
	s := newTestServerWith(t, Services{ArticleService: articles, StarService: stars, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{},
		TimelineService: userTimeline(rss.Timeline{Name: "AI picks", Filter: filter})})
	get := func(t *testing.T, target string) *httptest.ResponseRecorder {
		t.Helper()
		rr, _ := serveTimeline(t, s, s.timelineHandler, target, testTimelineID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		return rr
	}

	t.Run("lists stars with Star pressed", func(t *testing.T) {
		result = starredItems(2)
		body := get(t, "/timelines/"+testTimelineID).Body.String()
		if !gotFilter.StarredOnly || !gotFilter.OnlyIncluded || !slices.Equal(gotFilter.IncludeTagIDs, filter.IncludeTagIDs) || !gotBefore.IsZero() {
			t.Errorf("ListStarred got filter %+v, before %+v", gotFilter, gotBefore)
		}
		assertBodyContains(t, body, "Article 0", "Article 1", `aria-pressed="true" aria-label="Star"`)
		if strings.Contains(body, `aria-pressed="false" aria-label="Star"`) {
			t.Errorf("a row's Star isn't pressed: %s", body)
		}
	})

	t.Run("the pager's cursor is the last row's starred_at", func(t *testing.T) {
		result = starredItems(timelinePageSize + 1)
		body := get(t, "/timelines/"+testTimelineID).Body.String()
		last := result[timelinePageSize-1]
		m := regexp.MustCompile(`rel="next" href="/timelines/` + testTimelineID + `\?before=([^"]+)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no pager: %s", body)
		}
		next, err := url.QueryUnescape(m[1])
		if err != nil {
			t.Fatalf("unescape %q: %v", m[1], err)
		}
		cur, err := decodeCursor(next)
		if err != nil || !cur.At.Equal(last.StarredAt) || cur.ID != last.Article.ID {
			t.Errorf("next cursor = %+v (%v), want %v/%s", cur, err, last.StarredAt, last.Article.ID)
		}
	})

	t.Run("empty state", func(t *testing.T) {
		result = nil
		body := get(t, "/timelines/"+testTimelineID).Body.String()
		assertBodyContains(t, body, "Nothing starred here yet. Star articles in your timelines and they'll show up here.")
		if strings.Contains(body, "Nothing in this timeline yet.") {
			t.Errorf("shows the plain empty state: %s", body)
		}
	})
}

func TestTimelineEditHandler(t *testing.T) {
	tags, subs := timelineSourcesStores()
	timelines := userTimeline(rss.Timeline{Name: "AI", Description: "Models", Filter: rss.TimelineFilter{
		OnlyIncluded: true, IncludeTagIDs: []string{testTagID}, ExcludeFeedIDs: []string{timelineFeed2ID}, StarredOnly: true,
		MaxPostsPerWeek: 3, MediaOnly: true, IncludeWords: []string{"AI", "machine learning"}, ExcludeWords: []string{"Sponsored"},
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
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`,
			`value="3"`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" checked />`,
			`name="include_words" value="AI, machine learning"`,
			`name="exclude_words" value="Sponsored"`,
			`<a class="btn" href="/timelines/`+testTimelineID+`">Cancel</a>`,
			`<form class="tag-delete" action="/timelines/`+testTimelineID+`/delete" method="post">`,
			`<button class="btn-sm" type="submit">Delete timeline</button>`,
		)
	})

	for _, id := range []string{"junk", otherTagID} {
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
			!slices.Equal(updated.Filter.ExcludeTagIDs, []string{testTagID}) || updated.Filter.StarredOnly {
			t.Errorf("updated %+v", updated)
		}
	})

	t.Run("saves starred only", func(t *testing.T) {
		updated = nil
		form := url.Values{"name": {"Starred"}, "show": {"all"}, "starred_only": {"1"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || !updated.Filter.StarredOnly {
			t.Errorf("updated %+v, want StarredOnly", updated)
		}
	})

	t.Run("saves the posting rate and audio or video, and clears them", func(t *testing.T) {
		updated = nil
		form := url.Values{"name": {"Podcasts"}, "show": {"all"}, "max_posts_per_week": {"2"}, "media_only": {"1"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || updated.Filter.MaxPostsPerWeek != 2 || !updated.Filter.MediaOnly {
			t.Errorf("updated %+v, want MaxPostsPerWeek 2 and MediaOnly", updated)
		}

		updated = nil
		form = url.Values{"name": {"Podcasts"}, "show": {"all"}, "max_posts_per_week": {""}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || updated.Filter.MaxPostsPerWeek != 0 || updated.Filter.MediaOnly {
			t.Errorf("updated %+v, want neither condition", updated)
		}
	})

	t.Run("saves the words and phrases, and clears them", func(t *testing.T) {
		updated = nil
		form := url.Values{"name": {"AI"}, "show": {"all"}, "include_words": {"AI, LLM"}, "exclude_words": {"deal"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || !slices.Equal(updated.Filter.IncludeWords, []string{"AI", "LLM"}) || !slices.Equal(updated.Filter.ExcludeWords, []string{"deal"}) {
			t.Errorf("updated %+v, want the words", updated)
		}

		updated = nil
		form = url.Values{"name": {"AI"}, "show": {"all"}, "include_words": {" , "}, "exclude_words": {""}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || len(updated.Filter.IncludeWords) != 0 || len(updated.Filter.ExcludeWords) != 0 {
			t.Errorf("updated %+v, want no words", updated)
		}
	})

	t.Run("starred only stays ticked when the form is invalid", func(t *testing.T) {
		rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, url.Values{"name": {""}, "show": {"all"}, "starred_only": {"1"}})
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d", rr.Code)
		}
		assertBodyContains(t, rr.Body.String(), `<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`)
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

func TestSplitTerms(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ,, ", nil},
		{"AI", []string{"AI"}},
		{" AI , LLM,GPT ", []string{"AI", "LLM", "GPT"}},
		{"machine \t\n  learning", []string{"machine learning"}},
		{"AI, ai, Ai, LLM", []string{"AI", "LLM"}},
		{"C++, C#, .NET, +++", []string{"C++", "C#", ".NET", "+++"}},
		{"Café, CAFÉ", []string{"Café"}},
	}
	for _, tt := range tests {
		if got := splitTerms(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("splitTerms(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// numberedTerms returns n different terms, comma-separated: t1, t2, ….
func numberedTerms(n int) string {
	terms := make([]string, n)
	for i := range terms {
		terms[i] = fmt.Sprintf("t%d", i+1)
	}
	return strings.Join(terms, ", ")
}

func TestTimelineFormTerms(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		message string // "": valid
	}{
		{"blank", "", ""},
		{"C++ and C#", "C++, C#, F#, .NET, @handle, #hashtag", ""},
		{"20 terms", numberedTerms(20), ""},
		{"21 terms", numberedTerms(21), "At most 20 words or phrases."},
		{"21 terms with repeats", strings.Repeat("a,", 30), ""},
		{"50 runes", strings.Repeat("é", 50), ""},
		{"51 runes", strings.Repeat("é", 51), "Each word or phrase must be 50 characters or fewer."},
		{"+++", "+++", "Each word or phrase needs at least one letter or number."},
		{"--", "AI, --", "Each word or phrase needs at least one letter or number."},
		{"a digit is enough", "2026", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, field := range []string{"include_words", "exclude_words"} {
				f := timelineForm{Name: "X", Show: "all"}
				if field == "include_words" {
					f.IncludeWords = tt.value
				} else {
					f.ExcludeWords = tt.value
				}
				f.check(nil, nil)
				if got := f.FieldErrors[field]; got != tt.message {
					t.Errorf("%s: error = %q, want %q", field, got, tt.message)
				}
			}
		})
	}
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

	for _, id := range []string{"junk", otherTagID} {
		t.Run(id+" is 404", func(t *testing.T) {
			deleted = ""
			rr, _ := serveTimeline(t, s, s.timelineDeleteHandler, "/", id, url.Values{})
			if rr.Code != http.StatusNotFound || deleted != "" {
				t.Errorf("status = %d, deleted %q", rr.Code, deleted)
			}
		})
	}
}

func TestTimelineMoveHandler(t *testing.T) {
	type call struct {
		userID, id string
		up         bool
	}
	var calls []call
	s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
		moveFn: func(ctx context.Context, userID, id string, up bool) error {
			calls = append(calls, call{userID, id, up})
			if id != testTimelineID {
				return rss.ErrNoRecord
			}
			return nil
		},
	}})

	for _, tt := range []struct {
		direction string
		up        bool
	}{{"up", true}, {"down", false}} {
		t.Run(tt.direction, func(t *testing.T) {
			calls = nil
			rr, flash := serveTimeline(t, s, s.timelineMoveHandler, "/", testTimelineID, url.Values{"direction": {tt.direction}})
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/timelines#timeline-"+testTimelineID {
				t.Fatalf("code %d location %q", rr.Code, rr.Header().Get("Location"))
			}
			if want := []call{{"user-1", testTimelineID, tt.up}}; !slices.Equal(calls, want) {
				t.Errorf("calls = %v, want %v", calls, want)
			}
			if flash != "" {
				t.Errorf("flash = %q, want none", flash)
			}
		})
	}

	for _, direction := range []string{"", "left", "UP"} {
		t.Run("direction "+direction+" is 400", func(t *testing.T) {
			calls = nil
			rr, _ := serveTimeline(t, s, s.timelineMoveHandler, "/", testTimelineID, url.Values{"direction": {direction}})
			if rr.Code != http.StatusBadRequest || calls != nil {
				t.Errorf("status = %d, calls %v", rr.Code, calls)
			}
		})
	}

	for _, id := range []string{"junk", otherTagID} {
		t.Run(id+" is 404", func(t *testing.T) {
			rr, _ := serveTimeline(t, s, s.timelineMoveHandler, "/", id, url.Values{"direction": {"up"}})
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d", rr.Code)
			}
		})
	}

	t.Run("store error is 500", func(t *testing.T) {
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			moveFn: func(context.Context, string, string, bool) error { return errors.New("boom") },
		}})
		rr, _ := serveTimeline(t, s, s.timelineMoveHandler, "/", testTimelineID, url.Values{"direction": {"down"}})
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func TestTagPageMakeTimelineLink(t *testing.T) {
	s := newTestServerWith(t, Services{TagService: userTag(), SubscriptionService: &fakeSubscriptionStore{}})
	rr, _ := serveTag(t, s, s.tagHandler, testTagID, nil)
	assertBodyContains(t, rr.Body.String(), `<a href="/timelines/new?tag=`+testTagID+`">Make a timeline</a>`)
}

const testShareToken = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJ0123456"

var (
	sharingConfirmed   = &rss.Session{User: rss.User{ID: "user-1", Email: "ada@example.com", EmailConfirmedAt: time.Now()}}
	sharingUnconfirmed = &rss.Session{User: rss.User{ID: "user-1", Email: "ada@example.com"}}
)

// newSharingTestServer is a server with base URL https://rss.example and
// timelines, where user-1 has a tag and two feeds (timelineSourcesStores).
func newSharingTestServer(t *testing.T, timelines *fakeTimelineStore) *Server {
	t.Helper()
	tags, subs := timelineSourcesStores()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example"},
		Services{TagService: tags, SubscriptionService: subs, TimelineService: timelines})
}

func TestTimelineEditSharing(t *testing.T) {
	edit := func(t *testing.T, s *Server, sess *rss.Session) string {
		t.Helper()
		rr, _ := serveTimelineSession(t, s, sess, s.timelineEditHandler, "/timelines/"+testTimelineID+"/edit", testTimelineID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	}

	t.Run("unconfirmed email", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI"}))
		body := edit(t, s, sharingUnconfirmed)
		assertBodyContains(t, body,
			`<section class="timeline-sharing" id="sharing" aria-labelledby="sharing-heading">`,
			`<h2 id="sharing-heading">Sharing</h2>`,
			"<p>Confirm your email address to share timelines.</p>",
			`<form action="/confirm-email/resend" method="post">`,
			`<input type="hidden" name="return_to" value="/timelines/`+testTimelineID+`/edit#sharing" />`,
		)
		if strings.Contains(body, "Share this timeline") {
			t.Errorf("offers sharing without a confirmed email: %s", body)
		}
	})

	t.Run("not shared", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI"}))
		body := edit(t, s, sharingConfirmed)
		assertBodyContains(t, body,
			"Anyone with the link will be able to follow this timeline's newest articles in a feed reader",
			`<form action="/timelines/`+testTimelineID+`/share" method="post">`,
			`<button class="btn-sm" type="submit">Share this timeline</button>`,
		)
		if strings.Contains(body, "/shared/") {
			t.Errorf("shows a link while not shared: %s", body)
		}
	})

	t.Run("shared", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI", ShareToken: testShareToken}))
		body := edit(t, s, sharingConfirmed)
		assertBodyContains(t, body,
			"Anyone with the link can follow this timeline's newest articles in a feed reader",
			`<label for="share-feed-url">Feed link</label>`,
			`<input type="text" id="share-feed-url" value="https://rss.example/shared/`+testShareToken+`/feed.atom" readonly />`,
			`<label for="share-page-url">Page link</label>`,
			`<input type="text" id="share-page-url" value="https://rss.example/shared/`+testShareToken+`" readonly`,
			"A page to send to people: it says how to subscribe.",
			`<form action="/timelines/`+testTimelineID+`/unshare" method="post">`,
			`<button class="btn-sm" type="submit">Stop sharing</button>`,
			`<form action="/timelines/`+testTimelineID+`/share/new-link" method="post">`,
			`<button class="btn-sm" type="submit" aria-describedby="share-new-link-hint">New link</button>`,
			"The current links will stop working.",
		)
	})

	t.Run("shows the flash", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI"}))
		rr, flash := serveTimelineSession(t, s, sharingConfirmed, func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "flash", "Sharing turned on.")
			s.timelineEditHandler(w, r)
		}, "/", testTimelineID, nil)
		assertBodyContains(t, rr.Body.String(), `<div class="flash">Sharing turned on.</div>`)
		if flash != "" {
			t.Errorf("flash left in the session: %q", flash)
		}
	})

	t.Run("New timeline has no Sharing section", func(t *testing.T) {
		s := newSharingTestServer(t, &fakeTimelineStore{})
		rr, _ := serveTimelineSession(t, s, sharingConfirmed, s.timelineNewHandler, "/timelines/new", "", nil)
		if body := rr.Body.String(); strings.Contains(body, `id="sharing"`) {
			t.Errorf("New timeline has a Sharing section: %s", body)
		}
	})
}

func TestTimelineSharingHandlers(t *testing.T) {
	type call struct{ userID, id, token string }
	// newServer returns a server whose store records each call, and
	// fails NewShareLink with ErrNoRecord when notShared is set.
	newServer := func(t *testing.T, notShared bool) (*Server, *[]call) {
		var calls []call
		timelines := userTimeline(rss.Timeline{Name: "AI"})
		timelines.shareFn = func(ctx context.Context, userID, id, newToken string) (string, error) {
			calls = append(calls, call{userID, id, newToken})
			return newToken, nil
		}
		timelines.unshareFn = func(ctx context.Context, userID, id string) error {
			calls = append(calls, call{userID, id, ""})
			return nil
		}
		timelines.newShareLinkFn = func(ctx context.Context, userID, id, newToken string) error {
			calls = append(calls, call{userID, id, newToken})
			if notShared {
				return rss.ErrNoRecord
			}
			return nil
		}
		return newSharingTestServer(t, timelines), &calls
	}
	token43 := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

	tests := []struct {
		name       string
		handler    func(*Server) http.HandlerFunc
		sess       *rss.Session
		wantCall   bool
		wantToken  bool
		wantFlash  string
		notShared  bool
		wantStatus int
	}{
		{"share", func(s *Server) http.HandlerFunc { return s.timelineShareHandler }, sharingConfirmed, true, true, "Sharing turned on.", false, http.StatusSeeOther},
		{"share unconfirmed changes nothing", func(s *Server) http.HandlerFunc { return s.timelineShareHandler }, sharingUnconfirmed, false, false, "Confirm your email address to share timelines.", false, http.StatusSeeOther},
		{"unshare", func(s *Server) http.HandlerFunc { return s.timelineUnshareHandler }, sharingConfirmed, true, false, "Sharing turned off. The links no longer work.", false, http.StatusSeeOther},
		{"unshare unconfirmed still works", func(s *Server) http.HandlerFunc { return s.timelineUnshareHandler }, sharingUnconfirmed, true, false, "Sharing turned off. The links no longer work.", false, http.StatusSeeOther},
		{"new link", func(s *Server) http.HandlerFunc { return s.timelineNewLinkHandler }, sharingConfirmed, true, true, "New link made. The old one no longer works.", false, http.StatusSeeOther},
		{"new link unconfirmed changes nothing", func(s *Server) http.HandlerFunc { return s.timelineNewLinkHandler }, sharingUnconfirmed, false, false, "Confirm your email address to share timelines.", false, http.StatusSeeOther},
		{"new link when not shared is 404", func(s *Server) http.HandlerFunc { return s.timelineNewLinkHandler }, sharingConfirmed, true, true, "", true, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, calls := newServer(t, tt.notShared)
			rr, flash := serveTimelineSession(t, s, tt.sess, tt.handler(s), "/", testTimelineID, url.Values{})
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusSeeOther {
				if loc := rr.Header().Get("Location"); loc != "/timelines/"+testTimelineID+"/edit#sharing" {
					t.Errorf("Location = %q", loc)
				}
			}
			if flash != tt.wantFlash {
				t.Errorf("flash = %q, want %q", flash, tt.wantFlash)
			}
			if !tt.wantCall {
				if len(*calls) != 0 {
					t.Errorf("store called: %v", *calls)
				}
				return
			}
			if len(*calls) != 1 || (*calls)[0].userID != "user-1" || (*calls)[0].id != testTimelineID {
				t.Fatalf("calls = %v, want one for user-1's timeline", *calls)
			}
			if got := (*calls)[0].token; tt.wantToken != token43.MatchString(got) {
				t.Errorf("token = %q, want a new 43-character token: %v", got, tt.wantToken)
			}
		})
	}

	for name, handler := range map[string]func(*Server) http.HandlerFunc{
		"share":    func(s *Server) http.HandlerFunc { return s.timelineShareHandler },
		"unshare":  func(s *Server) http.HandlerFunc { return s.timelineUnshareHandler },
		"new link": func(s *Server) http.HandlerFunc { return s.timelineNewLinkHandler },
	} {
		for _, id := range []string{"junk", otherTagID} {
			t.Run(name+" "+id+" is 404", func(t *testing.T) {
				s, calls := newServer(t, false)
				rr, _ := serveTimelineSession(t, s, sharingConfirmed, handler(s), "/", id, url.Values{})
				if rr.Code != http.StatusNotFound || len(*calls) != 0 {
					t.Errorf("status = %d, calls %v; want 404 and none", rr.Code, *calls)
				}
			})
		}
	}
}
