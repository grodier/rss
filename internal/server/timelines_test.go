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
	const thirdID = "99999999-9999-4999-8999-999999999999"
	moveButton := func(name, dir string, disabled bool) string {
		b := `<button class="row-icon-btn" type="submit" name="direction" value="` + dir + `" aria-label="Move ` + name + ` ` + dir + `" title="Move ` + dir + `"`
		if disabled {
			b += " disabled"
		}
		return b + ">"
	}

	t.Run("Home first, then each timeline with its summary and controls", func(t *testing.T) {
		s := newTestServerWith(t, Services{
			TimelineService: &fakeTimelineStore{listWithFiltersFn: func(ctx context.Context, userID string) ([]rss.Timeline, error) {
				if userID != "user-1" {
					return nil, errors.New("wrong user")
				}
				return []rss.Timeline{
					{},
					{ID: testTimelineID, Name: "AI", Description: "Models & more", ShareToken: testShareToken,
						Filter: rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{testTagID}, IncludeWords: []string{"GPT"}}},
					{ID: newsTimelineID, Name: "News", Filter: rss.TimelineFilter{MaxPostsPerWeek: 2}},
					{ID: thirdID, Name: "Sports"},
				}, nil
			}},
			TagService: &fakeTagStore{listFn: func(ctx context.Context, userID string) ([]rss.Tag, error) {
				return []rss.Tag{{ID: testTagID, Name: "AI"}}, nil
			}},
		})
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>Timelines</h1>",
			`<a class="btn btn-sm" href="/timelines/new">New timeline</a>`,
			`<li class="timeline-row" id="timeline-all" data-id="all" data-name="All feeds">`,
			`<a class="timeline-row-name" href="/timelines/all">`, `All feeds</a>`, "<p>Every feed you follow</p>",
			`<form class="timeline-row-move" action="/timelines/all/move" method="post" data-move>`,
			`<span class="row-icon-slot" aria-hidden="true"></span>`,
			`<ul class="timeline-list" role="list" data-reorder>`,
			// Rendered hidden: app.js shows it, since dragging needs JavaScript.
			`<span class="reorder-handle" aria-hidden="true" hidden>`,
			`<li class="timeline-row" id="timeline-`+testTimelineID+`" data-id="`+testTimelineID+`" data-name="AI">`,
			`<a class="timeline-row-name" href="/timelines/`+testTimelineID+`">AI</a>`,
			"<p>Tagged AI · Mentions “GPT”</p>",
			`<form class="timeline-row-move" action="/timelines/`+testTimelineID+`/move" method="post" data-move>`,
			`<a class="row-icon-btn" href="/timelines/`+testTimelineID+`/edit" aria-label="Edit AI" title="Edit">`,
			`<a class="timeline-row-name" href="/timelines/`+newsTimelineID+`">News</a>`,
			"<p>At most 2 posts a week</p>",
			`<a class="timeline-row-name" href="/timelines/`+thirdID+`">Sports</a>`,
			"<p>All your feeds</p>",
			// Move buttons are disabled at the ends, not left out; Home is first.
			moveButton("All feeds", "up", true), moveButton("All feeds", "down", false),
			moveButton("AI", "up", false), moveButton("AI", "down", false),
			moveButton("News", "up", false), moveButton("News", "down", false),
			moveButton("Sports", "up", false), moveButton("Sports", "down", true),
		)
		if home, first := strings.Index(body, `id="timeline-all"`), strings.Index(body, `id="timeline-`+testTimelineID+`"`); home < 0 || home > first {
			t.Errorf("Home's row isn't first: %s", body)
		}
		if strings.Index(body, ">AI</a>") > strings.Index(body, ">News</a>") || strings.Index(body, ">News</a>") > strings.Index(body, ">Sports</a>") {
			t.Errorf("rows out of order: %s", body)
		}
		// The description is shown under the tabs and in the editor, not here.
		if strings.Contains(body, "Models &amp; more") {
			t.Errorf("shows the description: %s", body)
		}
		if strings.Contains(body, "You don't have any timelines yet.") {
			t.Errorf("shows the empty state: %s", body)
		}
		// Only AI is shared.
		if n := strings.Count(body, `<span class="chip">Shared</span>`); n != 1 {
			t.Errorf("%d timelines marked Shared, want 1: %s", n, body)
		}
		assertBodyContains(t, body, `<a class="timeline-row-name" href="/timelines/`+testTimelineID+`">AI</a>
            <span class="chip">Shared</span>`)
	})

	t.Run("Home alone has both move buttons disabled", func(t *testing.T) {
		s := newTestServer(t)
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		assertBodyContains(t, rr.Body.String(), moveButton("All feeds", "up", true), moveButton("All feeds", "down", true))
	})

	t.Run("Home last can move up, not down", func(t *testing.T) {
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
			listFn: func(ctx context.Context, userID string) ([]rss.Timeline, error) {
				return []rss.Timeline{{ID: testTimelineID, Name: "AI"}, {}}, nil
			},
		}})
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		body := rr.Body.String()
		assertBodyContains(t, body, moveButton("AI", "up", true), moveButton("AI", "down", false), moveButton("All feeds", "up", false), moveButton("All feeds", "down", true))
		if strings.Index(body, `id="timeline-all"`) < strings.Index(body, `id="timeline-`+testTimelineID+`"`) {
			t.Errorf("Home's row isn't last: %s", body)
		}
	})

	t.Run("empty state follows Home's row", func(t *testing.T) {
		s := newTestServer(t)
		rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil)
		body := rr.Body.String()
		assertBodyContains(t, body,
			`<a class="timeline-row-name" href="/timelines/all">`,
			"You don't have any timelines yet. A timeline shows some of your feeds, for example everything tagged “News”.")
		if strings.Index(body, `href="/timelines/all">`) > strings.Index(body, "You don't have any timelines yet.") {
			t.Errorf("the empty state comes before Home's row: %s", body)
		}
	})

	t.Run("store errors are 500", func(t *testing.T) {
		boom := func(context.Context, string) ([]rss.Timeline, error) { return nil, errors.New("boom") }
		s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{listWithFiltersFn: boom}})
		if rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("timelines: status = %d, want 500", rr.Code)
		}
		s = newTestServerWith(t, Services{
			TimelineService: &fakeTimelineStore{listFn: func(context.Context, string) ([]rss.Timeline, error) {
				return []rss.Timeline{{ID: testTimelineID, Name: "AI"}}, nil
			}},
			TagService: &fakeTagStore{listFn: func(context.Context, string) ([]rss.Tag, error) { return nil, errors.New("boom") }},
		})
		if rr, _ := serveTimeline(t, s, s.timelinesHandler, "/timelines", "", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("tags: status = %d, want 500", rr.Code)
		}
	})
}

func TestTimelineSummary(t *testing.T) {
	const newsTagID, sportsTagID, goneTagID = "a1", "a2", "a3"
	// Ordered by name, as TagRepository.List returns them.
	tags := []rss.Tag{{ID: testTagID, Name: "AI"}, {ID: newsTagID, Name: "News"}, {ID: sportsTagID, Name: "Sports"}}

	tests := []struct {
		name   string
		filter rss.TimelineFilter
		want   string
	}{
		{"no conditions", rss.TimelineFilter{}, "All your feeds"},
		{"starred, all feeds", rss.TimelineFilter{StarredOnly: true}, "Your stars, newest star first"},
		{"starred, some feeds", rss.TimelineFilter{StarredOnly: true, OnlyIncluded: true, IncludeTagIDs: []string{newsTagID}}, "Tagged News · Starred only"},
		{"one tag", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{newsTagID}}, "Tagged News"},
		{"two tags, in name order", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{newsTagID, testTagID}}, "Tagged AI or News"},
		{"three tags", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{sportsTagID, newsTagID, testTagID}}, "Tagged AI, News or Sports"},
		{"one feed", rss.TimelineFilter{OnlyIncluded: true, IncludeFeedIDs: []string{timelineFeedID}}, "1 chosen feed"},
		{"two feeds", rss.TimelineFilter{OnlyIncluded: true, IncludeFeedIDs: []string{timelineFeedID, timelineFeed2ID}}, "2 chosen feeds"},
		{"tags and feeds", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{testTagID}, IncludeFeedIDs: []string{timelineFeedID}}, "Tagged AI · 1 chosen feed"},
		{"no sources left", rss.TimelineFilter{OnlyIncluded: true}, "No feeds chosen"},
		{"an unknown tag is skipped", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{goneTagID, newsTagID}}, "Tagged News"},
		{"only unknown tags", rss.TimelineFilter{OnlyIncluded: true, IncludeTagIDs: []string{goneTagID}}, "No feeds chosen"},
		{"one post a week", rss.TimelineFilter{MaxPostsPerWeek: 1}, "At most 1 post a week"},
		{"two posts a week", rss.TimelineFilter{MaxPostsPerWeek: 2}, "At most 2 posts a week"},
		{"audio or video", rss.TimelineFilter{MediaOnly: true}, "Audio or video"},
		{"one word", rss.TimelineFilter{IncludeWords: []string{"AI"}}, "Mentions “AI”"},
		{"two words", rss.TimelineFilter{IncludeWords: []string{"AI", "machine learning"}}, "Mentions “AI” or “machine learning”"},
		{"three words", rss.TimelineFilter{IncludeWords: []string{"AI", "LLM", "GPT"}}, "Mentions “AI”, “LLM” or “GPT”"},
		{"four words are counted", rss.TimelineFilter{IncludeWords: []string{"a", "b", "c", "d"}}, "Mentions 4 words or phrases"},
		{"left-out words", rss.TimelineFilter{ExcludeWords: []string{"crypto"}}, "Leaves out “crypto”"},
		{"three left-out words", rss.TimelineFilter{ExcludeWords: []string{"a", "b", "c"}}, "Leaves out “a”, “b” or “c”"},
		{"four left-out words are counted", rss.TimelineFilter{ExcludeWords: []string{"a", "b", "c", "d"}}, "Leaves out 4 words or phrases"},
		{"left-out tags", rss.TimelineFilter{ExcludeTagIDs: []string{sportsTagID, newsTagID}}, "Leaves out feeds tagged News or Sports"},
		{"one left-out feed", rss.TimelineFilter{ExcludeFeedIDs: []string{timelineFeedID}}, "Leaves out 1 feed"},
		{"two left-out feeds", rss.TimelineFilter{ExcludeFeedIDs: []string{timelineFeedID, timelineFeed2ID}}, "Leaves out 2 feeds"},
		{
			"everything, in order",
			rss.TimelineFilter{
				OnlyIncluded: true, IncludeTagIDs: []string{testTagID}, IncludeFeedIDs: []string{timelineFeedID}, StarredOnly: true,
				MaxPostsPerWeek: 3, MediaOnly: true, IncludeWords: []string{"GPT"}, ExcludeWords: []string{"crypto"},
				ExcludeTagIDs: []string{sportsTagID}, ExcludeFeedIDs: []string{timelineFeed2ID},
			},
			"Tagged AI · 1 chosen feed · Starred only · At most 3 posts a week · Audio or video · Mentions “GPT” · Leaves out “crypto” · Leaves out feeds tagged Sports · Leaves out 1 feed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timelineSummary(rss.Timeline{Filter: tt.filter}, tags); got != tt.want {
				t.Errorf("timelineSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimelineNewHandler(t *testing.T) {
	tags, subs := timelineSourcesStores()
	s := newTestServerWith(t, Services{TagService: tags, SubscriptionService: subs})
	get := func(t *testing.T, s *Server, target string) string {
		t.Helper()
		rr, _ := serveTimeline(t, s, s.timelineNewHandler, target, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	}
	// startRow is the start of a starting point's row on the list.
	startRow := func(href string) string { return `<a class="settings-row" href="` + href + `">` }
	assertList := func(t *testing.T, body string) {
		t.Helper()
		assertBodyContains(t, body, "<h1>New timeline</h1>", `<h2 id="start-heading">Start from</h2>`)
		if strings.Contains(body, `<form class="timeline-form"`) {
			t.Errorf("shows the form, not the list: %s", body)
		}
	}

	t.Run("lists the starting points", func(t *testing.T) {
		body := get(t, s, "/timelines/new")
		assertList(t, body)
		assertBodyContains(t, body,
			startRow("/timelines/new?preset=tags"), "Feeds with a tag", "Everything with the tags you choose, like News or Tech.",
			startRow("/timelines/new?preset=quiet"), "Quiet posters", "Feeds that post a couple of times a week or less, so busy ones can’t bury them.",
			startRow("/timelines/new?preset=starred"), "Starred", "Articles you’ve starred, newest star first.",
			startRow("/timelines/new?preset=keywords"), "Keywords", "Articles that mention words or phrases you choose.",
			startRow("/timelines/new?preset=media"), "Audio and video", "Podcast episodes and videos attached to posts.",
			startRow("/timelines/new?preset=blank"), "Start from scratch", "All your feeds, with filters you choose.",
		)
	})

	t.Run("without tags, Feeds with a tag goes to Tags", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{}, SubscriptionService: subs})
		body := get(t, s, "/timelines/new")
		assertBodyContains(t, body, startRow("/tags"), "Make a tag first: tags group your feeds, like News or Tech.")
		if strings.Contains(body, "preset=tags") {
			t.Errorf("offers the tags preset without tags: %s", body)
		}
	})

	t.Run("the list's tag store error is 500", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{listFn: func(context.Context, string) ([]rss.Tag, error) {
			return nil, errors.New("boom")
		}}})
		if rr, _ := serveTimeline(t, s, s.timelineNewHandler, "/timelines/new", "", nil); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	for _, target := range []string{"/timelines/new?preset=junk", "/timelines/new?preset=", "/timelines/new?tag=" + otherTagID, "/timelines/new?tag=not-a-uuid", "/timelines/new?preset=quiet&tag=" + otherTagID} {
		t.Run(target+" shows the list", func(t *testing.T) {
			assertList(t, get(t, s, target))
		})
	}

	t.Run("?preset=blank is the empty form, showing all feeds", func(t *testing.T) {
		body := get(t, s, "/timelines/new?preset=blank")
		assertBodyContains(t, body,
			"<h1>New timeline</h1>",
			// The summary reads the form back.
			`<p class="timeline-summary-sentence" data-summary-sentence>Articles from all your feeds.</p>`,
			`<summary data-summary-count>From 2 of your 2 feeds</summary>`,
			"<li>Feed One</li><li>Feed Two</li>",
			`<form class="timeline-form" action="/timelines" method="post" data-timeline-form>`,
			`name="name" value=""`,
			`<label for="description">Description <span class="label-optional">(optional)</span></label>`,
			`<p class="hint" id="description-hint">Shown under the tabs.</p>`,
			"<legend>Feeds</legend>",
			`<input type="radio" name="show" value="all" checked`,
			"Only some",
			`<p class="timeline-form-label" id="include-tags-label">Feeds with any of these tags</p>`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" data-name="AI">`,
			`<summary>Or choose feeds <span class="timeline-disclosure-count" data-feeds-count></span></summary>`,
			`<input type="checkbox" name="include_feeds" value="`+timelineFeedID+`" data-name="Feed One" data-rate="0" data-tags="">`,
			`<summary>Leave out <span class="timeline-disclosure-count" data-leave-out-count></span></summary>`,
			`<p class="hint">Always left out, even if they match above.</p>`,
			`<input type="checkbox" name="exclude_tags" value="`+testTagID+`" data-name="AI">`,
			`<input type="checkbox" name="exclude_feeds" value="`+timelineFeed2ID+`" data-name="Feed Two"`,
			"<legend>Articles</legend>",
			`<p class="hint timeline-form-lede">Only show articles that match everything you tick.</p>`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" />`,
			`<span class="hint" id="starred-only-hint">Ordered by when you starred them.</span>`,
			`<input type="checkbox" name="quiet" value="1" aria-describedby="quiet-hint" />`,
			`<label for="max_posts_per_week">At most</label>`,
			`<input type="number" id="max_posts_per_week" name="max_posts_per_week" min="1" max="100" inputmode="numeric"`,
			`value="2"`,
			`<p class="hint" id="max_posts_per_week-hint" data-rate-hint>2 of your feeds post this little right now.</p>`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" />`,
			`<input type="checkbox" name="mentions" value="1" />`,
			`<label class="visually-hidden" for="include_words">Words or phrases to look for</label>`,
			`<input type="checkbox" name="not_mentions" value="1" />`,
			`<button class="btn-primary" type="submit">Create timeline</button>`,
			`<a class="btn" href="/timelines">Cancel</a>`,
		)
		for _, bad := range []string{"Delete timeline", " checked>", " checked />", "autofocus", "Start from", "/share"} {
			if strings.Contains(body, bad) {
				t.Errorf("body has %q: %s", bad, body)
			}
		}
		// Save is the form's last field set's follower: after Articles.
		if strings.Index(body, "Create timeline</button>") < strings.Index(body, "<legend>Articles</legend>") {
			t.Errorf("the button comes before the fields: %s", body)
		}
	})

	presets := []struct {
		preset string
		want   []string
	}{
		{"tags", []string{`name="name" value=""`, `<input type="radio" name="show" value="some" checked`, `<input type="checkbox" name="include_tags" value="` + testTagID + `" data-name="AI">`,
			"Choose at least one tag or feed, or show all your feeds.", `<summary data-summary-count>No feeds yet</summary>`}},
		{"quiet", []string{`<input type="text" id="name" name="name" value="Quiet posters"`, `<input type="radio" name="show" value="all" checked`, `<input type="checkbox" name="quiet" value="1" aria-describedby="quiet-hint" checked />`, `value="2"`, `<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" />`,
			"Articles from feeds that post at most 2 times a week."}},
		{"starred", []string{`<input type="text" id="name" name="name" value="Starred"`, `<input type="radio" name="show" value="all" checked`, `<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`}},
		{"keywords", []string{`name="name" value=""`, `<input type="radio" name="show" value="all" checked`, `<input type="checkbox" name="mentions" value="1" checked />`, `<input type="text" id="include_words" name="include_words" value=""`}},
		{"media", []string{`<input type="text" id="name" name="name" value="Audio and video"`, `<input type="radio" name="show" value="all" checked`, `<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" checked />`}},
	}
	for _, tt := range presets {
		t.Run("?preset="+tt.preset+" fills in the form", func(t *testing.T) {
			assertBodyContains(t, get(t, s, "/timelines/new?preset="+tt.preset), tt.want...)
		})
	}

	t.Run("?preset=starred with ?tag= is named after the tag", func(t *testing.T) {
		assertBodyContains(t, get(t, s, "/timelines/new?preset=starred&tag="+testTagID),
			`<input type="text" id="name" name="name" value="AI"`,
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" data-name="AI" checked>`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`,
		)
	})

	t.Run("?tag= prefills from one of the user's tags", func(t *testing.T) {
		assertBodyContains(t, get(t, s, "/timelines/new?tag="+testTagID),
			`<input type="text" id="name" name="name" value="AI"`,
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" data-name="AI" checked>`,
		)
	})

	t.Run("no tags or feeds", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{}, SubscriptionService: &fakeSubscriptionStore{}})
		body := get(t, s, "/timelines/new?preset=blank")
		assertBodyContains(t, body, `You don't have any tags yet. <a href="/tags">Make a tag</a> to group feeds.`)
		if strings.Contains(body, "Or choose feeds") {
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

	t.Run("saves the posting rate when From quiet feeds is ticked, and audio or video", func(t *testing.T) {
		for _, tt := range []struct {
			quiet string // "" for unticked
			value string
			want  int
		}{{"1", "2", 2}, {"1", " 100 ", 100}, {"", "", 0}, {"", "5", 0}, {"", "junk", 0}} {
			form := url.Values{"name": {"Quiet"}, "show": {"all"}, "max_posts_per_week": {tt.value}, "media_only": {"1"}}
			if tt.quiet != "" {
				form.Set("quiet", tt.quiet)
			}
			res := post(t, form, nil)
			if res.code != http.StatusSeeOther || res.created == nil {
				t.Fatalf("%q: code %d, created %v", tt.value, res.code, res.created)
			}
			if f := res.created.Filter; f.MaxPostsPerWeek != tt.want || !f.MediaOnly {
				t.Errorf("%q: filter = %+v, want MaxPostsPerWeek %d and MediaOnly", tt.value, f, tt.want)
			}
		}
	})

	t.Run("an unticked condition stores nothing, whatever its field holds", func(t *testing.T) {
		form := url.Values{"name": {"AI"}, "show": {"all"}, "include_words": {"AI"}, "exclude_words": {"+++"}, "max_posts_per_week": {"0"}}
		res := post(t, form, nil)
		if res.code != http.StatusSeeOther || res.created == nil {
			t.Fatalf("code %d, created %v", res.code, res.created)
		}
		if f := res.created.Filter; len(f.IncludeWords) != 0 || len(f.ExcludeWords) != 0 || f.MaxPostsPerWeek != 0 {
			t.Errorf("filter = %+v, want no words and no rate", f)
		}
	})

	t.Run("saves the words and phrases", func(t *testing.T) {
		form := url.Values{"name": {"AI"}, "show": {"all"}, "mentions": {"1"}, "not_mentions": {"1"},
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
	for _, rate := range []string{"", "0", "101", "2.5", "abc", "-1"} {
		tests = append(tests, struct {
			name    string
			form    url.Values
			field   string
			message string
		}{"posts a week " + rate, url.Values{"name": {"X"}, "show": {"all"}, "quiet": {"1"}, "max_posts_per_week": {rate}}, "max_posts_per_week", "Enter a whole number from 1 to 100."})
	}
	for field, box := range map[string]string{"include_words": "mentions", "exclude_words": "not_mentions"} {
		for _, tt := range []struct{ name, value, message string }{
			{"empty", " , ", "Enter at least one word or phrase."},
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
			}{field + " " + tt.name, url.Values{"name": {"X"}, "show": {"all"}, box: {"1"}, field: {tt.value}}, field, tt.message})
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
			"quiet": {"1"}, "max_posts_per_week": {"7"}, "media_only": {"1"}, "mentions": {"1"}, "include_words": {"AI,  C++"}, "not_mentions": {"1"}, "exclude_words": {"deal"}}
		res := post(t, form, nil)
		assertBodyContains(t, res.body,
			">Kept</textarea>",
			`name="include_words" value="AI,  C&#43;&#43;"`,
			`name="exclude_words" value="deal"`,
			`value="7"`,
			`<input type="checkbox" name="quiet" value="1" aria-describedby="quiet-hint" checked />`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" checked />`,
			`<input type="checkbox" name="mentions" value="1" checked />`,
			`<input type="checkbox" name="not_mentions" value="1" checked />`,
			`<input type="radio" name="show" value="some" checked`,
			`<input type="checkbox" name="include_feeds" value="`+timelineFeedID+`" data-name="Feed One" data-rate="0" data-tags="" checked>`,
			`<input type="checkbox" name="exclude_tags" value="`+testTagID+`" data-name="AI" checked>`,
			`<details class="timeline-disclosure" open>
              <summary>Or choose feeds <span class="timeline-disclosure-count" data-feeds-count>(1)</span></summary>`,
			`<summary>Leave out <span class="timeline-disclosure-count" data-leave-out-count>(1)</span></summary>`,
			// The summary reads back the submitted values.
			`data-summary-sentence>Articles with audio or video mentioning “AI” or “C&#43;&#43;” but not “deal” from Feed One, if they post at most 7 times a week, except feeds tagged AI.</p>`,
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
		return []rss.Timeline{{}, {ID: testTimelineID, Name: "AI"}}, nil
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
			`<div class="timeline-about">
        <p>Models &amp; more</p>`,
			`<a href="/timelines/`+testTimelineID+`" aria-current="page">AI</a>`,
			`<li><a href="/timelines/all">All feeds</a></li>`,
			// The tab row's Edit goes to this timeline's row on /timelines.
			`<a class="btn btn-sm" href="/timelines#timeline-`+testTimelineID+`">Edit<span class="visually-hidden"> timelines</span></a>`,
			"Article 0", "Article 1",
		)
		if strings.Contains(body, "Back to newest") {
			t.Errorf("unexpected Back to newest")
		}
		if strings.Contains(body, `/timelines/`+testTimelineID+`/edit`) {
			t.Errorf("links to the edit page; the tab row's Edit goes to /timelines: %s", body)
		}
	})

	t.Run("no row under the tabs without a description", func(t *testing.T) {
		result = timelineItems(1)
		plain := userTimeline(rss.Timeline{Name: "AI", Filter: filter})
		s := newTestServerWith(t, Services{ArticleService: articles, TimelineService: plain, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}})
		rr, _ := serveTimeline(t, s, s.timelineHandler, "/timelines/"+testTimelineID, testTimelineID, nil)
		if body := rr.Body.String(); strings.Contains(body, "timeline-about") {
			t.Errorf("has the row under the tabs: %s", body)
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

	// A timeline of quiet feeds says what will show up, rather than that
	// nothing has; one with words and no rate keeps the plain text.
	for _, tt := range []struct {
		name   string
		filter rss.TimelineFilter
		want   string
	}{
		{"empty, at most 2 posts a week", rss.TimelineFilter{MaxPostsPerWeek: 2, IncludeWords: []string{"AI"}},
			"When a feed you follow posts at most 2 times a week, its articles show up here, so busy feeds never bury them."},
		{"empty, at most 1 post a week", rss.TimelineFilter{MaxPostsPerWeek: 1},
			"When a feed you follow posts at most once a week, its articles show up here, so busy feeds never bury them."},
		{"empty, words only", rss.TimelineFilter{IncludeWords: []string{"AI"}}, "Nothing in this timeline yet."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result = nil
			s := newTestServerWith(t, Services{ArticleService: articles, ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{},
				TimelineService: userTimeline(rss.Timeline{Name: "Quiet", Filter: tt.filter})})
			rr, _ := serveTimeline(t, s, s.timelineHandler, "/timelines/"+testTimelineID, testTimelineID, nil)
			body := rr.Body.String()
			assertBodyContains(t, body, tt.want)
			if tt.filter.MaxPostsPerWeek > 0 && strings.Contains(body, "Nothing in this timeline yet.") {
				t.Errorf("also says nothing's here: %s", body)
			}
		})
	}

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
			`<input type="checkbox" name="include_tags" value="`+testTagID+`" data-name="AI" checked>`,
			`<input type="checkbox" name="exclude_feeds" value="`+timelineFeed2ID+`" data-name="Feed Two" data-rate="0" data-tags="" checked>`,
			`<input type="checkbox" name="starred_only" value="1" aria-describedby="starred-only-hint" checked />`,
			`<input type="checkbox" name="quiet" value="1" aria-describedby="quiet-hint" checked />`,
			`value="3"`,
			`<input type="checkbox" name="media_only" value="1" aria-describedby="media-only-hint" checked />`,
			`<input type="checkbox" name="mentions" value="1" checked />`,
			`name="include_words" value="AI, machine learning"`,
			`<input type="checkbox" name="not_mentions" value="1" checked />`,
			`name="exclude_words" value="Sponsored"`,
			// The head's Share opens the share page; the form has no Sharing section.
			`<a class="btn btn-sm" href="/timelines/`+testTimelineID+`/share">Share</a>`,
			`data-summary-sentence>Articles you’ve starred with audio or video mentioning “AI” or “machine learning” but not “Sponsored” from feeds tagged AI that post at most 3 times a week, except Feed Two. Newest star first.</p>`,
			`<button class="btn-primary" type="submit">Save timeline</button>`,
			`<a class="btn" href="/timelines/`+testTimelineID+`">Cancel</a>`,
			`<form class="danger-zone" action="/timelines/`+testTimelineID+`/delete" method="post">`,
			`<button class="btn-sm btn-danger-quiet" type="submit">Delete timeline</button>`,
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
		form := url.Values{"name": {"Podcasts"}, "show": {"all"}, "quiet": {"1"}, "max_posts_per_week": {"2"}, "media_only": {"1"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || updated.Filter.MaxPostsPerWeek != 2 || !updated.Filter.MediaOnly {
			t.Errorf("updated %+v, want MaxPostsPerWeek 2 and MediaOnly", updated)
		}

		updated = nil
		form = url.Values{"name": {"Podcasts"}, "show": {"all"}, "max_posts_per_week": {"2"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || updated.Filter.MaxPostsPerWeek != 0 || updated.Filter.MediaOnly {
			t.Errorf("updated %+v, want neither condition", updated)
		}
	})

	t.Run("saves the words and phrases, and clears them", func(t *testing.T) {
		updated = nil
		form := url.Values{"name": {"AI"}, "show": {"all"}, "mentions": {"1"}, "include_words": {"AI, LLM"}, "not_mentions": {"1"}, "exclude_words": {"deal"}}
		if rr, _ := serveTimeline(t, s, s.timelineEditPostHandler, "/", testTimelineID, form); rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", rr.Code)
		}
		if updated == nil || !slices.Equal(updated.Filter.IncludeWords, []string{"AI", "LLM"}) || !slices.Equal(updated.Filter.ExcludeWords, []string{"deal"}) {
			t.Errorf("updated %+v, want the words", updated)
		}

		updated = nil
		form = url.Values{"name": {"AI"}, "show": {"all"}, "include_words": {"AI, LLM"}, "exclude_words": {"deal"}}
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
		{"blank, while ticked", "", "Enter at least one word or phrase."},
		{"only commas", " , ,", "Enter at least one word or phrase."},
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
					f.Mentions, f.IncludeWords = true, tt.value
				} else {
					f.NotMentions, f.ExcludeWords = true, tt.value
				}
				f.check(nil, nil)
				if got := f.FieldErrors[field]; got != tt.message {
					t.Errorf("%s: error = %q, want %q", field, got, tt.message)
				}

				// Unticked, the field isn't checked: it won't be stored.
				f = timelineForm{Name: "X", Show: "all", IncludeWords: tt.value, ExcludeWords: tt.value}
				f.check(nil, nil)
				if !f.Valid() {
					t.Errorf("%s unticked: errors %v, want none", field, f.FieldErrors)
				}
			}
		})
	}
}

func TestTimelineFormFor(t *testing.T) {
	t.Run("ticks the conditions that have a value", func(t *testing.T) {
		f := timelineFormFor(rss.Timeline{Filter: rss.TimelineFilter{MaxPostsPerWeek: 3, IncludeWords: []string{"AI", "C++"}}})
		if !f.Quiet || f.MaxPostsPerWeek != "3" || !f.Mentions || f.IncludeWords != "AI, C++" || f.NotMentions || f.StarredOnly || f.MediaOnly {
			t.Errorf("form = %+v", f)
		}
	})
	t.Run("an unticked rate starts at 2", func(t *testing.T) {
		f := timelineFormFor(rss.Timeline{Filter: rss.TimelineFilter{ExcludeWords: []string{"crypto"}}})
		if f.Quiet || f.MaxPostsPerWeek != "2" || f.Mentions || !f.NotMentions || f.ExcludeWords != "crypto" {
			t.Errorf("form = %+v", f)
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
			if id != testTimelineID && id != "" {
				return rss.ErrNoRecord
			}
			return nil
		},
	}})

	t.Run("all moves Home", func(t *testing.T) {
		calls = nil
		rr, _ := serveTimeline(t, s, s.timelineMoveHandler, "/", "all", url.Values{"direction": {"down"}})
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/timelines#timeline-all" {
			t.Fatalf("code %d location %q", rr.Code, rr.Header().Get("Location"))
		}
		if want := []call{{"user-1", "", false}}; !slices.Equal(calls, want) {
			t.Errorf("calls = %v, want %v (Home as \"\")", calls, want)
		}
	})

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
	assertBodyContains(t, rr.Body.String(), `<a class="btn btn-sm" href="/timelines/new?tag=`+testTagID+`">Make a timeline</a>`)
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

func TestTimelineSharePage(t *testing.T) {
	edit := func(t *testing.T, s *Server, sess *rss.Session) string {
		t.Helper()
		rr, _ := serveTimelineSession(t, s, sess, s.timelineSharePageHandler, "/timelines/"+testTimelineID+"/share", testTimelineID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	}

	t.Run("unconfirmed email", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI"}))
		body := edit(t, s, sharingUnconfirmed)
		assertBodyContains(t, body,
			"<title>Paper Round - Share timeline</title>",
			"<h1>Share “AI”</h1>",
			"<p>Confirm your email address to share timelines.</p>",
			`<form action="/confirm-email/resend" method="post">`,
			`<input type="hidden" name="return_to" value="/timelines/`+testTimelineID+`/share" />`,
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
			s.timelineSharePageHandler(w, r)
		}, "/", testTimelineID, nil)
		assertBodyContains(t, rr.Body.String(), `<div class="flash">Sharing turned on.</div>`)
		if flash != "" {
			t.Errorf("flash left in the session: %q", flash)
		}
	})

	for _, id := range []string{"junk", otherTagID} {
		t.Run(id+" is 404", func(t *testing.T) {
			s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI"}))
			if rr, _ := serveTimelineSession(t, s, sharingConfirmed, s.timelineSharePageHandler, "/", id, nil); rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
		})
	}

	t.Run("the edit page links to it and has no sharing of its own", func(t *testing.T) {
		s := newSharingTestServer(t, userTimeline(rss.Timeline{Name: "AI", ShareToken: testShareToken}))
		rr, _ := serveTimelineSession(t, s, sharingConfirmed, s.timelineEditHandler, "/", testTimelineID, nil)
		body := rr.Body.String()
		assertBodyContains(t, body, `<a class="btn btn-sm" href="/timelines/`+testTimelineID+`/share">Share</a>`)
		for _, bad := range []string{"Stop sharing", "/shared/", "Share this timeline"} {
			if strings.Contains(body, bad) {
				t.Errorf("edit page has %q: %s", bad, body)
			}
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
				if loc := rr.Header().Get("Location"); loc != "/timelines/"+testTimelineID+"/share" {
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

func TestTimelineSentence(t *testing.T) {
	const newsTagID = "a1"
	tags := []rss.Tag{{ID: testTagID, Name: "AI"}, {ID: newsTagID, Name: "News"}}
	subs := []rss.Subscription{
		{Feed: rss.Feed{ID: timelineFeedID, Title: "Feed One"}},
		{Feed: rss.Feed{ID: timelineFeed2ID, Title: "Feed Two"}, Title: "My Two"},
	}
	all := timelineForm{Show: "all"}
	with := func(f timelineForm, change func(*timelineForm)) timelineForm { change(&f); return f }

	tests := []struct {
		name string
		form timelineForm
		want string
	}{
		{"all feeds", all, "Articles from all your feeds."},
		{"the Quiet posters default", with(all, func(f *timelineForm) { f.Quiet, f.MaxPostsPerWeek = true, "2" }), "Articles from feeds that post at most 2 times a week."},
		{"once a week", with(all, func(f *timelineForm) { f.Quiet, f.MaxPostsPerWeek = true, "1" }), "Articles from feeds that post at most once a week."},
		{"an unticked rate is ignored", with(all, func(f *timelineForm) { f.MaxPostsPerWeek = "2" }), "Articles from all your feeds."},
		{"the Starred default", with(all, func(f *timelineForm) { f.StarredOnly = true }), "Articles you’ve starred from all your feeds. Newest star first."},
		{"audio or video", with(all, func(f *timelineForm) { f.MediaOnly = true }), "Articles with audio or video from all your feeds."},
		{"mentioning", with(all, func(f *timelineForm) { f.Mentions, f.IncludeWords = true, "AI, LLM, ai" }), "Articles mentioning “AI” or “LLM” from all your feeds."},
		{"not mentioning alone", with(all, func(f *timelineForm) { f.NotMentions, f.ExcludeWords = true, "crypto" }), "Articles not mentioning “crypto” from all your feeds."},
		{"mentioning but not", with(all, func(f *timelineForm) {
			f.Mentions, f.IncludeWords, f.NotMentions, f.ExcludeWords = true, "AI", true, "crypto, NFT, web3"
		}), "Articles mentioning “AI” but not “crypto”, “NFT” or “web3” from all your feeds."},
		{"unticked words are ignored", with(all, func(f *timelineForm) { f.IncludeWords, f.ExcludeWords = "AI", "crypto" }), "Articles from all your feeds."},
		{"some, tags", timelineForm{Show: "some", IncludeTags: []string{newsTagID, testTagID}}, "Articles from feeds tagged AI or News."},
		{"some, feeds, by the user's names", timelineForm{Show: "some", IncludeFeeds: []string{timelineFeed2ID, timelineFeedID}}, "Articles from Feed One and My Two."},
		{"some, tags and a feed", timelineForm{Show: "some", IncludeTags: []string{testTagID}, IncludeFeeds: []string{timelineFeedID}}, "Articles from feeds tagged AI and Feed One."},
		{"some, tags with a rate", timelineForm{Show: "some", IncludeTags: []string{testTagID}, Quiet: true, MaxPostsPerWeek: "3"}, "Articles from feeds tagged AI that post at most 3 times a week."},
		{"some, a feed with a rate", timelineForm{Show: "some", IncludeFeeds: []string{timelineFeedID}, Quiet: true, MaxPostsPerWeek: "3"}, "Articles from Feed One, if they post at most 3 times a week."},
		{"some, nothing chosen", timelineForm{Show: "some", ExcludeTags: []string{testTagID}}, "Choose at least one tag or feed, or show all your feeds."},
		{"leave out tags", with(all, func(f *timelineForm) { f.ExcludeTags = []string{testTagID, newsTagID} }), "Articles from all your feeds, except feeds tagged AI or News."},
		{"leave out tags and feeds", with(all, func(f *timelineForm) {
			f.ExcludeTags, f.ExcludeFeeds = []string{newsTagID}, []string{timelineFeedID, timelineFeed2ID}
		}),
			"Articles from all your feeds, except feeds tagged News, Feed One and My Two."},
		{"a tag with words and a left-out feed", timelineForm{Show: "some", IncludeTags: []string{testTagID}, Mentions: true, IncludeWords: "AI, LLM", ExcludeFeeds: []string{timelineFeedID}},
			"Articles mentioning “AI” or “LLM” from feeds tagged AI, except Feed One."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timelineSentence(tt.form, tags, subs); got != tt.want {
				t.Errorf("timelineSentence = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimelineFeedCount(t *testing.T) {
	const quietID, busyID, untaggedID = "f1", "f2", "f3"
	ai, news := rss.Tag{ID: testTagID, Name: "AI"}, rss.Tag{ID: "t2", Name: "News"}
	subs := []rss.Subscription{
		{Feed: rss.Feed{ID: quietID, Title: "Quiet", PostsPerWeek: 2}, Tags: []rss.Tag{ai}},
		{Feed: rss.Feed{ID: busyID, Title: "Busy", PostsPerWeek: 40}, Tags: []rss.Tag{ai, news}},
		{Feed: rss.Feed{ID: untaggedID, Title: "Untagged", PostsPerWeek: 0.5}},
	}
	tests := []struct {
		name string
		form timelineForm
		want []string
	}{
		{"all", timelineForm{Show: "all"}, []string{quietID, busyID, untaggedID}},
		{"some by tag", timelineForm{Show: "some", IncludeTags: []string{testTagID}}, []string{quietID, busyID}},
		{"some by feed", timelineForm{Show: "some", IncludeFeeds: []string{untaggedID}}, []string{untaggedID}},
		{"leave out a tag wins over a shown tag", timelineForm{Show: "some", IncludeTags: []string{testTagID}, ExcludeTags: []string{"t2"}}, []string{quietID}},
		{"leave out a feed", timelineForm{Show: "all", ExcludeFeeds: []string{busyID}}, []string{quietID, untaggedID}},
		{"a rate, equal counts", timelineForm{Show: "all", Quiet: true, MaxPostsPerWeek: "2"}, []string{quietID, untaggedID}},
		{"an unticked rate", timelineForm{Show: "all", MaxPostsPerWeek: "2"}, []string{quietID, busyID, untaggedID}},
		{"articles conditions don't count", timelineForm{Show: "all", StarredOnly: true, MediaOnly: true, Mentions: true, IncludeWords: "x"}, []string{quietID, busyID, untaggedID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, sub := range timelineFeedCount(tt.form, subs) {
				got = append(got, sub.Feed.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("timelineFeedCount = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("the summary's count line", func(t *testing.T) {
		for _, tt := range []struct {
			subs []rss.Subscription
			form timelineForm
			want string
		}{
			{subs, timelineForm{Show: "all"}, "From 3 of your 3 feeds"},
			{subs, timelineForm{Show: "some", IncludeTags: []string{testTagID}, ExcludeTags: []string{testTagID}}, "None of your feeds match yet"},
			{subs[:1], timelineForm{Show: "all"}, "From your only feed"},
			{subs, timelineForm{Show: "some"}, "No feeds yet"},
		} {
			if got := tt.form.summary(nil, tt.subs).Count; got != tt.want {
				t.Errorf("Count = %q, want %q", got, tt.want)
			}
		}
		if got := (&timelineForm{Show: "all", MaxPostsPerWeek: "1"}).summary(nil, subs).RateHint; got != "1 of your feeds posts this little right now." {
			t.Errorf("RateHint = %q", got)
		}
		if got := (&timelineForm{Show: "all", MaxPostsPerWeek: "zero"}).summary(nil, subs).RateHint; got != "" {
			t.Errorf("RateHint for an invalid number = %q, want none", got)
		}
	})
}

func TestTimelineOrderHandler(t *testing.T) {
	type call struct {
		userID string
		ids    []string
	}
	var calls []call
	var storeErr error
	s := newTestServerWith(t, Services{TimelineService: &fakeTimelineStore{
		reorderFn: func(ctx context.Context, userID string, ids []string) error {
			calls = append(calls, call{userID, ids})
			return storeErr
		},
	}})
	post := func(order ...string) (*httptest.ResponseRecorder, string) {
		form := url.Values{}
		for _, id := range order {
			form.Add("order", id)
		}
		return serveTimeline(t, s, s.timelineOrderHandler, "/timelines/order", "", form)
	}

	t.Run("a valid order is saved, Home as \"\"", func(t *testing.T) {
		calls, storeErr = nil, nil
		rr, flash := post(newsTimelineID, "all", testTimelineID)
		if rr.Code != http.StatusNoContent || flash != "" {
			t.Fatalf("code %d, flash %q; want 204 and none", rr.Code, flash)
		}
		if len(calls) != 1 || calls[0].userID != "user-1" || !slices.Equal(calls[0].ids, []string{newsTimelineID, "", testTimelineID}) {
			t.Errorf("calls = %+v", calls)
		}
	})

	for _, bad := range []string{"junk", "", "ALL"} {
		t.Run(fmt.Sprintf("%q is 400", bad), func(t *testing.T) {
			calls, storeErr = nil, nil
			if rr, _ := post("all", bad); rr.Code != http.StatusBadRequest || calls != nil {
				t.Errorf("code %d, calls %v; want 400 and none", rr.Code, calls)
			}
		})
	}

	t.Run("a changed set of tabs goes back to /timelines with the flash", func(t *testing.T) {
		calls, storeErr = nil, rss.ErrOrderChanged
		rr, flash := post("all", testTimelineID)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/timelines" {
			t.Fatalf("code %d location %q", rr.Code, rr.Header().Get("Location"))
		}
		if flash != "Your timelines changed in another window, so the new order wasn't saved." {
			t.Errorf("flash = %q", flash)
		}
	})

	t.Run("another store error is 500", func(t *testing.T) {
		calls, storeErr = nil, errors.New("boom")
		if rr, _ := post("all"); rr.Code != http.StatusInternalServerError {
			t.Errorf("code %d, want 500", rr.Code)
		}
	})
}
