package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const (
	testTagID  = "33333333-3333-4333-8333-333333333333"
	otherTagID = "44444444-4444-4444-8444-444444444444"
)

// serveTag serves handler as user-1, with id as the URL's {id} unless it's
// "". form is posted when not nil. It returns the response and the flash
// left in the session.
func serveTag(t *testing.T, s *Server, handler http.HandlerFunc, id string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tags", nil)
	if form != nil {
		req = httptest.NewRequest(http.MethodPost, "/tags", strings.NewReader(form.Encode()))
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

// userTag is a fakeTagStore whose user-1 has tag testTagID, called AI.
func userTag() *fakeTagStore {
	return &fakeTagStore{getFn: func(ctx context.Context, userID, id string) (rss.Tag, error) {
		if userID != "user-1" || id != testTagID {
			return rss.Tag{}, rss.ErrNoRecord
		}
		return rss.Tag{ID: testTagID, Name: "AI"}, nil
	}}
}

// assertNameError checks that body shows msg as the name field's error,
// with the field's error markup and value.
func assertNameError(t *testing.T, body, msg, value string) {
	t.Helper()
	assertBodyContains(t, body,
		`<p class="error" id="name-error">`+html.EscapeString(msg)+`</p>`,
		`value="`+html.EscapeString(value)+`"`,
		`aria-invalid="true"`,
		`aria-describedby="name-hint name-error"`,
		"autofocus",
	)
}

func TestTagsHandler(t *testing.T) {
	t.Run("lists tags with their counts", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{
			listFn: func(ctx context.Context, userID string) ([]rss.Tag, error) {
				if userID != "user-1" {
					return nil, fmt.Errorf("listed for user %q", userID)
				}
				return []rss.Tag{
					{ID: testTagID, Name: "AI", Feeds: 3},
					{ID: otherTagID, Name: "News", Feeds: 1},
				}, nil
			},
		}})
		rr, _ := serveTag(t, s, s.tagsHandler, "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>Tags</h1>",
			`<form class="settings-form" action="/tags" method="post">`,
			`<p class="hint" id="name-hint">Only you see your tags.</p>`,
			`<a class="settings-row" href="/tags/`+testTagID+`">`,
			`<span class="settings-label">AI</span>`,
			`<span class="settings-value">3 feeds</span>`,
			`<span class="settings-label">News</span>`,
			`<span class="settings-value">1 feed</span>`,
		)
		if strings.Contains(body, "You don't have any tags yet.") {
			t.Errorf("body shows the empty state: %s", body)
		}
		if strings.Contains(body, "autofocus") {
			t.Errorf("body autofocuses a field without an error: %s", body)
		}
	})

	t.Run("empty state", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{}})
		rr, _ := serveTag(t, s, s.tagsHandler, "", nil)
		assertBodyContains(t, rr.Body.String(),
			`You don't have any tags yet. Tags group your feeds, for example "News" or "AI", so you can make timelines from them.`)
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{TagService: &fakeTagStore{
			listFn: func(context.Context, string) ([]rss.Tag, error) { return nil, errors.New("boom") },
		}})
		rr, _ := serveTag(t, s, s.tagsHandler, "", nil)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestTagCreateHandler(t *testing.T) {
	maxLen := strings.Repeat("é", 50)
	tests := []struct {
		name      string
		input     string
		storeErr  error
		wantCode  int
		wantName  string // the name passed to Create; "" if not called
		wantError string // the field's error on 422
		wantValue string // the field's value on 422
	}{
		{"valid", "  Machine \t learning ", nil, http.StatusSeeOther, "Machine learning", "", ""},
		{"50 runes", maxLen, nil, http.StatusSeeOther, maxLen, "", ""},
		{"blank", " \t ", nil, http.StatusUnprocessableEntity, "", "Enter a name.", ""},
		{"51 runes", maxLen + "x", nil, http.StatusUnprocessableEntity, "", "Must be 50 characters or fewer.", maxLen + "x"},
		{"comma", "AI, ML", nil, http.StatusUnprocessableEntity, "", "Tags can't contain commas.", "AI, ML"},
		{"duplicate", " ai ", rss.ErrDuplicateTag, http.StatusUnprocessableEntity, "ai", "You already have a tag called “ai”.", "ai"},
		{"too many", "AI", rss.ErrTooManyTags, http.StatusUnprocessableEntity, "AI", "You can have up to 100 tags.", "AI"},
		{"store error", "AI", errors.New("boom"), http.StatusInternalServerError, "AI", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotName string
			s := newTestServerWith(t, Services{TagService: &fakeTagStore{
				createFn: func(ctx context.Context, userID, name string) (rss.Tag, error) {
					if userID != "user-1" {
						t.Errorf("Create for user %q, want user-1", userID)
					}
					gotName = name
					if tt.storeErr != nil {
						return rss.Tag{}, tt.storeErr
					}
					return rss.Tag{ID: testTagID, Name: name}, nil
				},
			}})

			rr, _ := serveTag(t, s, s.tagCreateHandler, "", url.Values{"name": {tt.input}})

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if gotName != tt.wantName {
				t.Errorf("Create name = %q, want %q", gotName, tt.wantName)
			}
			switch tt.wantCode {
			case http.StatusSeeOther:
				assertRedirect(t, rr, "/tags/"+testTagID)
			case http.StatusUnprocessableEntity:
				assertNameError(t, rr.Body.String(), tt.wantError, tt.wantValue)
			}
		})
	}
}

// tagPageServer serves user-1's tag testTagID, with subs as their
// subscriptions and tagged as the feeds that have the tag.
func tagPageServer(t *testing.T, tags *fakeTagStore, subs []rss.Subscription, tagged map[string]bool) *Server {
	t.Helper()
	tags.feedIDsFn = func(ctx context.Context, userID, id string) (map[string]bool, error) {
		if userID != "user-1" || id != testTagID {
			return nil, fmt.Errorf("FeedIDs(%q, %q)", userID, id)
		}
		return tagged, nil
	}
	return newTestServerWith(t, Services{TagService: tags, SubscriptionService: listing(subs...)})
}

func TestTagHandler(t *testing.T) {
	const otherFeedID = "22222222-2222-4222-8222-222222222222"
	subs := []rss.Subscription{
		{Feed: rss.Feed{ID: testFeedID, SiteID: testSiteID, Title: "One"}, Site: rss.Site{ID: testSiteID, Title: "Example Site"}},
		{Feed: rss.Feed{ID: otherFeedID, SiteID: testSiteID, Title: "Two"}, Site: rss.Site{ID: testSiteID, Title: "Example Site"}, Title: "My Two"},
	}

	t.Run("ticks the tagged subscriptions", func(t *testing.T) {
		s := tagPageServer(t, userTag(), subs, map[string]bool{testFeedID: true})
		rr, _ := serveTag(t, s, s.tagHandler, testTagID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			"<h1>AI</h1>",
			`<form class="tag-feeds" action="/tags/`+testTagID+`/feeds" method="post">`,
			"<legend>Feeds tagged “AI”</legend>",
			`<input type="checkbox" name="feeds" value="`+testFeedID+`" checked>`,
			`<input type="checkbox" name="feeds" value="`+otherFeedID+`">`,
			`<span class="check-row-title">One</span>`,
			`<span class="check-row-title">My Two</span>`,
			`<form class="settings-form" action="/tags/`+testTagID+`/rename" method="post">`,
			`value="AI"`,
			`<p class="hint" id="name-hint">Only you see your tags.</p>`,
			`<form class="danger-zone" action="/tags/`+testTagID+`/delete" method="post">`,
			`<button class="btn-sm btn-danger-quiet" type="submit">Delete tag</button>`,
		)
		if n := strings.Count(body, `<button class="btn-primary" type="submit">Save</button>`); n != 1 {
			t.Errorf("Save buttons = %d, want 1", n)
		}
		if strings.Index(body, "One</span>") > strings.Index(body, "My Two</span>") {
			t.Errorf("subscriptions are not in ListByUser order: %s", body)
		}
	})

	t.Run("more than 20 subscriptions get a Save button above the list too", func(t *testing.T) {
		many := make([]rss.Subscription, 21)
		for i := range many {
			many[i] = rss.Subscription{Feed: rss.Feed{ID: fmt.Sprintf("11111111-1111-4111-8111-%012d", i), SiteID: testSiteID}}
		}
		s := tagPageServer(t, userTag(), many, map[string]bool{})
		rr, _ := serveTag(t, s, s.tagHandler, testTagID, nil)
		if n := strings.Count(rr.Body.String(), `<button class="btn-primary" type="submit">Save</button>`); n != 2 {
			t.Errorf("Save buttons = %d, want 2", n)
		}
	})

	t.Run("empty state", func(t *testing.T) {
		s := tagPageServer(t, userTag(), nil, map[string]bool{})
		rr, _ := serveTag(t, s, s.tagHandler, testTagID, nil)
		body := rr.Body.String()
		assertBodyContains(t, body,
			"You're not subscribed to any feeds yet.",
			`<a href="/search">Search</a>`,
			`action="/tags/`+testTagID+`/rename"`,
		)
		if strings.Contains(body, `class="tag-feeds"`) {
			t.Errorf("body has the checklist form: %s", body)
		}
	})

	t.Run("non-UUID id", func(t *testing.T) {
		s := tagPageServer(t, userTag(), subs, nil)
		rr, _ := serveTag(t, s, s.tagHandler, "abc", nil)
		assertNotFoundHTML(t, rr)
	})

	t.Run("another user's tag", func(t *testing.T) {
		s := tagPageServer(t, userTag(), subs, nil)
		rr, _ := serveTag(t, s, s.tagHandler, otherTagID, nil)
		assertNotFoundHTML(t, rr)
	})
}

func TestTagFeedsHandler(t *testing.T) {
	const (
		subscribedA  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		subscribedB  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		unsubscribed = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	)

	// server records the IDs asked about and the SetFeeds calls (nil if not
	// called), and fails SetFeeds with setErr.
	server := func(t *testing.T, asked, set *[]string, setErr error) *Server {
		tags := userTag()
		tags.setFeedsFn = func(ctx context.Context, userID, id string, feedIDs []string) error {
			if userID != "user-1" || id != testTagID {
				t.Errorf("SetFeeds(%q, %q)", userID, id)
			}
			*set = feedIDs
			return setErr
		}
		return newTestServerWith(t, Services{TagService: tags, SubscriptionService: &fakeSubscriptionStore{
			subscribedFeedIDsFn: func(ctx context.Context, userID string, feedIDs []string) (map[string]bool, error) {
				*asked = feedIDs
				return map[string]bool{subscribedA: true, subscribedB: true}, nil
			},
		}})
	}

	t.Run("passes the ticked IDs the user subscribes to", func(t *testing.T) {
		var asked, set []string
		s := server(t, &asked, &set, nil)
		form := url.Values{"feeds": {subscribedA, unsubscribed, "not-a-uuid", subscribedB}}

		rr, flash := serveTag(t, s, s.tagFeedsHandler, testTagID, form)

		assertRedirect(t, rr, "/tags/"+testTagID)
		if want := []string{subscribedA, unsubscribed, subscribedB}; !slices.Equal(asked, want) {
			t.Errorf("SubscribedFeedIDs asked for %v, want %v", asked, want)
		}
		if want := []string{subscribedA, subscribedB}; !slices.Equal(set, want) {
			t.Errorf("SetFeeds = %v, want %v", set, want)
		}
		if want := "Saved the feeds tagged “AI”."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("nothing ticked removes the tag from all", func(t *testing.T) {
		var asked, set []string
		s := server(t, &asked, &set, nil)

		rr, _ := serveTag(t, s, s.tagFeedsHandler, testTagID, url.Values{})

		assertRedirect(t, rr, "/tags/"+testTagID)
		if set == nil || len(set) != 0 {
			t.Errorf("SetFeeds = %#v, want an empty list", set)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		var asked, set []string
		s := server(t, &asked, &set, nil)

		rr, _ := serveTag(t, s, s.tagFeedsHandler, otherTagID, url.Values{"feeds": {subscribedA}})

		assertNotFoundHTML(t, rr)
		if set != nil {
			t.Errorf("SetFeeds called with %v", set)
		}
	})

	t.Run("store error", func(t *testing.T) {
		var asked, set []string
		s := server(t, &asked, &set, errors.New("boom"))

		rr, _ := serveTag(t, s, s.tagFeedsHandler, testTagID, url.Values{"feeds": {subscribedA}})

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestTagRenameHandler(t *testing.T) {
	type renameCall struct{ userID, id, name string }
	server := func(t *testing.T, renames *[]renameCall, err error) *Server {
		tags := userTag()
		tags.renameFn = func(ctx context.Context, userID, id, name string) error {
			*renames = append(*renames, renameCall{userID, id, name})
			return err
		}
		return tagPageServer(t, tags, nil, map[string]bool{})
	}

	t.Run("renames", func(t *testing.T) {
		var renames []renameCall
		s := server(t, &renames, nil)

		rr, flash := serveTag(t, s, s.tagRenameHandler, testTagID, url.Values{"name": {"  Artificial   intelligence "}})

		assertRedirect(t, rr, "/tags/"+testTagID)
		if want := []renameCall{{"user-1", testTagID, "Artificial intelligence"}}; !slices.Equal(renames, want) {
			t.Errorf("Rename calls = %v, want %v", renames, want)
		}
		if flash != "Tag renamed." {
			t.Errorf("flash = %q, want %q", flash, "Tag renamed.")
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		var renames []renameCall
		s := server(t, &renames, rss.ErrDuplicateTag)

		rr, _ := serveTag(t, s, s.tagRenameHandler, testTagID, url.Values{"name": {"News"}})

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		body := rr.Body.String()
		assertNameError(t, body, "You already have a tag called “News”.", "News")
		assertBodyContains(t, body, "<h1>AI</h1>")
	})

	t.Run("invalid", func(t *testing.T) {
		var renames []renameCall
		s := server(t, &renames, nil)

		rr, _ := serveTag(t, s, s.tagRenameHandler, testTagID, url.Values{"name": {"A,B"}})

		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		assertNameError(t, rr.Body.String(), "Tags can't contain commas.", "A,B")
		if len(renames) != 0 {
			t.Errorf("Rename calls = %v, want none", renames)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		var renames []renameCall
		s := server(t, &renames, nil)

		rr, _ := serveTag(t, s, s.tagRenameHandler, otherTagID, url.Values{"name": {"Mine"}})

		assertNotFoundHTML(t, rr)
		if len(renames) != 0 {
			t.Errorf("Rename calls = %v, want none", renames)
		}
	})
}

func TestTagDeleteHandler(t *testing.T) {
	server := func(t *testing.T, deleted *[]string) *Server {
		tags := userTag()
		tags.deleteFn = func(ctx context.Context, userID, id string) error {
			*deleted = append(*deleted, userID+" "+id)
			return nil
		}
		return newTestServerWith(t, Services{TagService: tags})
	}

	t.Run("deletes", func(t *testing.T) {
		var deleted []string
		s := server(t, &deleted)

		rr, flash := serveTag(t, s, s.tagDeleteHandler, testTagID, url.Values{})

		assertRedirect(t, rr, "/tags")
		if want := []string{"user-1 " + testTagID}; !slices.Equal(deleted, want) {
			t.Errorf("Delete calls = %v, want %v", deleted, want)
		}
		if want := "Deleted the tag “AI”."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("another user's tag", func(t *testing.T) {
		var deleted []string
		s := server(t, &deleted)

		rr, _ := serveTag(t, s, s.tagDeleteHandler, otherTagID, url.Values{})

		assertNotFoundHTML(t, rr)
		if len(deleted) != 0 {
			t.Errorf("Delete calls = %v, want none", deleted)
		}
	})
}

func TestFeedsHandlerTagsLink(t *testing.T) {
	_, body := getFeeds(t, listing())
	assertBodyContains(t, body, `<a class="btn btn-sm" href="/tags">Tags</a>`)
}
