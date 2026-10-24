package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/grodier/rss/internal/rss"
)

const testImportID = "44444444-4444-4444-8444-444444444444"

const importOPML = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Feeds</title></head>
  <body>
    <outline text="Tech">
      <outline type="rss" text="A blog" xmlUrl="https://a.example/feed.xml"/>
    </outline>
    <outline type="rss" text="" xmlUrl="https://b.example/rss"/>
  </body>
</opml>`

// opmlWithFeeds returns an OPML file with n distinct feeds.
func opmlWithFeeds(n int) string {
	var b strings.Builder
	b.WriteString(`<opml version="2.0"><body>`)
	for i := range n {
		fmt.Fprintf(&b, `<outline xmlUrl="https://example.com/%d.xml"/>`, i)
	}
	b.WriteString(`</body></opml>`)
	return b.String()
}

// multipartBody returns a multipart form body with a "file" part holding
// content as filename, or no file part when filename is "".
func multipartBody(t *testing.T, filename, content string) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
	} else {
		// What a browser sends when no file was chosen.
		fw, err := mw.CreateFormFile("file", "")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(nil)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body, mw.FormDataContentType()
}

func withSession(req *http.Request, sess rss.Session) *http.Request {
	ctx := context.WithValue(req.Context(), authenticatedUserIDContextKey, sess.User.ID)
	ctx = context.WithValue(ctx, authenticatedSessionContextKey, sess)
	return req.WithContext(ctx)
}

func postImport(t *testing.T, s *Server, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, filename, content)
	req := httptest.NewRequest(http.MethodPost, "/settings/import", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsImportFormHandler)).ServeHTTP(rr, withSession(req, settingsSession))
	return rr
}

func importServer(t *testing.T, store *fakeImportStore) *Server {
	t.Helper()
	return newTestServerWith(t, Services{ImportService: store})
}

func TestSettingsImportForm(t *testing.T) {
	noCreate := func(t *testing.T) *fakeImportStore {
		return &fakeImportStore{createFn: func(context.Context, string, string, []rss.ImportFeed) (string, error) {
			t.Error("Create must not be called")
			return "", nil
		}}
	}

	t.Run("valid file", func(t *testing.T) {
		var gotUser, gotName string
		var gotFeeds []rss.ImportFeed
		store := &fakeImportStore{createFn: func(_ context.Context, userID, filename string, feeds []rss.ImportFeed) (string, error) {
			gotUser, gotName, gotFeeds = userID, filename, feeds
			return testImportID, nil
		}}
		rr := postImport(t, importServer(t, store), `C:\Users\ada\Downloads\feeds.opml`, importOPML)

		assertRedirect(t, rr, "/settings/imports/"+testImportID)
		if gotUser != settingsSession.User.ID || gotName != "feeds.opml" {
			t.Errorf("Create(%q, %q), want (%q, %q)", gotUser, gotName, settingsSession.User.ID, "feeds.opml")
		}
		want := []rss.ImportFeed{
			{URL: "https://a.example/feed.xml", Title: "A blog"},
			{URL: "https://b.example/rss"},
		}
		if !reflect.DeepEqual(gotFeeds, want) {
			t.Errorf("feeds = %+v, want %+v", gotFeeds, want)
		}
	})

	t.Run("long filename is cut at a character boundary", func(t *testing.T) {
		var gotName string
		store := &fakeImportStore{createFn: func(_ context.Context, _, filename string, _ []rss.ImportFeed) (string, error) {
			gotName = filename
			return testImportID, nil
		}}
		name := "a" + strings.Repeat("é", 200) + ".opml"
		postImport(t, importServer(t, store), name, importOPML)

		if want := "a" + strings.Repeat("é", 127); gotName != want {
			t.Errorf("filename = %q (%d bytes), want %q", gotName, len(gotName), want)
		}
	})

	errorTests := []struct {
		name, filename, content string
		status                  int
		message                 string
	}{
		{"no file", "", "", http.StatusUnprocessableEntity, "Choose an OPML file to import."},
		{"file over 1 MiB", "big.opml", importOPML + strings.Repeat(" ", maxImportBytes), http.StatusRequestEntityTooLarge, "That file is too large. Imports can be up to 1 MB."},
		{"body over the cap", "big.opml", strings.Repeat(" ", maxImportBytes+128<<10), http.StatusRequestEntityTooLarge, "That file is too large. Imports can be up to 1 MB."},
		{"not OPML", "page.html", "<html><body>hi</body></html>", http.StatusUnprocessableEntity, "That file isn&#39;t an OPML file. Export one from your other reader&#39;s settings."},
		{"no feeds", "empty.opml", `<opml version="2.0"><body><outline text="Folder"/></body></opml>`, http.StatusUnprocessableEntity, "That file has no feeds in it."},
		{"too many feeds", "huge.opml", opmlWithFeeds(maxImportFeeds + 1), http.StatusUnprocessableEntity, "That file has more than 1,000 feeds. Split it into smaller files and import each one."},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postImport(t, importServer(t, noCreate(t)), tt.filename, tt.content)

			if rr.Code != tt.status {
				t.Fatalf("status = %d, want %d", rr.Code, tt.status)
			}
			assertBodyContains(t, rr.Body.String(),
				`<p class="error" id="file-error">`+tt.message+`</p>`,
				`aria-invalid="true"`,
				`aria-describedby="file-hint file-error"`,
				"autofocus",
				`enctype="multipart/form-data"`,
			)
		})
	}

	t.Run("1,000 feeds is allowed", func(t *testing.T) {
		store := &fakeImportStore{createFn: func(_ context.Context, _, _ string, feeds []rss.ImportFeed) (string, error) {
			if len(feeds) != maxImportFeeds {
				t.Errorf("got %d feeds, want %d", len(feeds), maxImportFeeds)
			}
			return testImportID, nil
		}}
		assertRedirect(t, postImport(t, importServer(t, store), "max.opml", opmlWithFeeds(maxImportFeeds)), "/settings/imports/"+testImportID)
	})

	t.Run("over the rate limit", func(t *testing.T) {
		calls := 0
		store := &fakeImportStore{createFn: func(context.Context, string, string, []rss.ImportFeed) (string, error) {
			calls++
			return testImportID, nil
		}}
		s := importServer(t, store)
		for range 5 {
			assertRedirect(t, postImport(t, s, "feeds.opml", importOPML), "/settings/imports/"+testImportID)
		}

		rr := postImport(t, s, "feeds.opml", importOPML)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
		if rr.Header().Get("Retry-After") == "" {
			t.Error("missing Retry-After")
		}
		assertBodyContains(t, rr.Body.String(), "You&#39;ve started several imports in the last hour. Try again later.")
		if calls != 5 {
			t.Errorf("Create called %d times, want 5", calls)
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := &fakeImportStore{createFn: func(context.Context, string, string, []rss.ImportFeed) (string, error) {
			return "", errors.New("boom")
		}}
		if rr := postImport(t, importServer(t, store), "feeds.opml", importOPML); rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("not multipart", func(t *testing.T) {
		s := importServer(t, noCreate(t))
		req := httptest.NewRequest(http.MethodPost, "/settings/import", strings.NewReader("file=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		s.settingsImportFormHandler(rr, withSession(req, settingsSession))

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		assertBodyContains(t, rr.Body.String(), "That form submission couldn&#39;t be read.")
	})

	t.Run("anonymous", func(t *testing.T) {
		s := importServer(t, noCreate(t))
		body, contentType := multipartBody(t, "feeds.opml", importOPML)
		req := httptest.NewRequest(http.MethodPost, "/settings/import", body)
		req.Header.Set("Content-Type", contentType)
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, req)

		assertRedirect(t, rr, "/login")
	})

	t.Run("cross-origin", func(t *testing.T) {
		s := importServer(t, noCreate(t))
		body, contentType := multipartBody(t, "feeds.opml", importOPML)
		req := httptest.NewRequest(http.MethodPost, "/settings/import", body)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
	})
}

func TestSettingsImportHandler(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var gotUser string
	var gotLimit int
	store := &fakeImportStore{listByUserFn: func(_ context.Context, userID string, limit int) ([]rss.ImportSummary, error) {
		gotUser, gotLimit = userID, limit
		return []rss.ImportSummary{
			{Import: rss.Import{ID: "imp-running", Filename: "running.opml", CreatedAt: at}, Total: 12, Pending: 3, Failed: 1},
			{Import: rss.Import{ID: "imp-failed", Filename: "failed.opml", CreatedAt: at}, Total: 8, Failed: 2},
			{Import: rss.Import{ID: "imp-ok", Filename: "ok.opml", CreatedAt: at}, Total: 1},
		}, nil
	}}
	s := importServer(t, store)
	rr := httptest.NewRecorder()
	s.settingsImportHandler(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser != settingsSession.User.ID || gotLimit != 5 {
		t.Errorf("ListByUser(%q, %d), want (%q, 5)", gotUser, gotLimit, settingsSession.User.ID)
	}
	body := rr.Body.String()
	assertBodyContains(t, body,
		"<h1>Import subscriptions</h1>",
		`<form class="settings-form" action="/settings/import" method="post" enctype="multipart/form-data">`,
		`<input type="file" id="file" name="file" required`,
		"Folders aren't kept: every feed goes into your timeline.",
		`<a class="rail-account" href="/settings" aria-current="page">`,
		"Recent imports",
		`href="/settings/imports/imp-running"`,
		"running.opml",
		`<time data-local="date" datetime="2026-10-01T12:00:00Z">1 Oct 2026</time>`,
		"12 feeds · importing…",
		`href="/settings/imports/imp-failed"`,
		"8 feeds · 2 couldn't be added",
		`href="/settings/imports/imp-ok"`,
		"1 feed</span>",
	)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}

	t.Run("no imports", func(t *testing.T) {
		s := importServer(t, &fakeImportStore{})
		rr := httptest.NewRecorder()
		s.settingsImportHandler(rr, requestWithSession(settingsSession))
		if strings.Contains(rr.Body.String(), "Recent imports") {
			t.Error("shows Recent imports without any")
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := importServer(t, &fakeImportStore{listByUserFn: func(context.Context, string, int) ([]rss.ImportSummary, error) {
			return nil, errors.New("boom")
		}})
		rr := httptest.NewRecorder()
		s.settingsImportHandler(rr, requestWithSession(settingsSession))
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func serveImport(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := requestWithSession(settingsSession)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	s.importHandler(rr, req)
	return rr
}

func getReturns(imp rss.Import, feeds []rss.ImportFeed, err error) *fakeImportStore {
	return &fakeImportStore{getFn: func(context.Context, string) (rss.Import, []rss.ImportFeed, error) {
		return imp, feeds, err
	}}
}

func TestImportHandler(t *testing.T) {
	imp := rss.Import{ID: testImportID, UserID: settingsSession.User.ID, Filename: "feeds.opml"}

	t.Run("in progress", func(t *testing.T) {
		feeds := []rss.ImportFeed{
			{URL: "https://a.example/feed", Title: "A", Status: rss.ImportSubscribed, FeedID: "feed-a"},
			{URL: "https://b.example/feed", Status: rss.ImportFailed, Failure: rss.ImportNotFeed},
			{URL: "https://c.example/feed", Status: rss.ImportRunning},
			{URL: "https://d.example/feed", Status: rss.ImportPending},
			{URL: "https://e.example/feed", Status: rss.ImportPending},
		}
		rr := serveImport(t, importServer(t, getReturns(imp, feeds, nil)), testImportID)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			`<meta http-equiv="refresh" content="2" />`,
			"Importing feeds.opml…",
			"Checked 2 of 5 feeds.",
			`<a class="feed-row-title" href="/feeds/feed-a">A</a>`,
			"There&#39;s no feed at this address.",
		)
		for _, unwanted := range []string{"Import finished", "Go to your timeline", "c.example", "d.example"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("body contains %q while in progress", unwanted)
			}
		}
	})

	t.Run("finished", func(t *testing.T) {
		feeds := []rss.ImportFeed{
			{URL: "https://a.example/feed", Title: "Outline A", FeedTitle: "Feed A", Status: rss.ImportSubscribed, FeedID: "feed-a"},
			{URL: "https://b.example/feed", FeedTitle: "Feed B", Status: rss.ImportSubscribed, FeedID: "feed-b"},
			{URL: "https://c.example/feed", Status: rss.ImportAlreadySubscribed, FeedID: "feed-c"},
			{URL: "not a url", Title: "Broken", Status: rss.ImportFailed, Failure: rss.ImportInvalidURL, Error: "secret detail"},
			{URL: "https://example.com/feed", Status: rss.ImportFailed, Failure: rss.ImportUnreachable},
			{URL: "https://d.example/page", Title: "D", Status: rss.ImportFailed, Failure: rss.ImportNotFeed},
		}
		rr := serveImport(t, importServer(t, getReturns(imp, feeds, nil)), testImportID)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		if strings.Contains(body, "http-equiv") {
			t.Error("finished page refreshes itself")
		}
		if strings.Contains(body, "secret detail") {
			t.Error("page shows the internal error")
		}
		assertBodyContains(t, body,
			"<h1>Import finished</h1>",
			"2 subscribed, 1 already subscribed, 3 couldn&#39;t be added.",
			`<a href="/">Go to your timeline</a>`,
			`<a class="feed-row-title" href="/feeds/feed-a">Outline A</a>`,
			`<a class="feed-row-title" href="/feeds/feed-b">Feed B</a>`,
			`<a class="feed-row-title" href="/feeds/feed-c">https://c.example/feed</a>`,
			`<p class="import-title">Broken</p>`,
			"Not a valid web address.",
			`<p class="import-title">https://example.com/feed</p>`,
			"Couldn&#39;t reach the site.",
			`<a href="/search?q=example.com">Search for example.com</a>`,
			"There&#39;s no feed at this address.",
			`<a href="/search?q=d.example">Search for d.example</a>`,
		)
		// Groups in order.
		subscribed := strings.Index(body, "<h2>Subscribed</h2>")
		already := strings.Index(body, "<h2>Already subscribed</h2>")
		failed := strings.Index(body, "<h2>Couldn't add</h2>")
		if subscribed < 0 || already < subscribed || failed < already {
			t.Errorf("groups out of order or missing: %d, %d, %d", subscribed, already, failed)
		}
		if strings.Contains(body, "Search for not") {
			t.Error("invalid URL has a search link")
		}
	})

	t.Run("summary leaves out empty groups", func(t *testing.T) {
		feeds := []rss.ImportFeed{{URL: "https://a.example/feed", Status: rss.ImportAlreadySubscribed, FeedID: "feed-a"}}
		body := serveImport(t, importServer(t, getReturns(imp, feeds, nil)), testImportID).Body.String()
		assertBodyContains(t, body, "<p>1 already subscribed.</p>")
		if strings.Contains(body, "<h2>Subscribed</h2>") || strings.Contains(body, "<h2>Couldn't add</h2>") {
			t.Error("shows empty groups")
		}
	})

	t.Run("another user's import", func(t *testing.T) {
		other := imp
		other.UserID = "123e4567-e89b-12d3-a456-426614174999"
		rr := serveImport(t, importServer(t, getReturns(other, nil, nil)), testImportID)
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("unknown import", func(t *testing.T) {
		rr := serveImport(t, importServer(t, getReturns(rss.Import{}, nil, rss.ErrNoRecord)), testImportID)
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("malformed ID", func(t *testing.T) {
		store := &fakeImportStore{getFn: func(context.Context, string) (rss.Import, []rss.ImportFeed, error) {
			t.Error("Get must not be called")
			return rss.Import{}, nil, nil
		}}
		rr := serveImport(t, importServer(t, store), "not-a-uuid")
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("store error", func(t *testing.T) {
		rr := serveImport(t, importServer(t, getReturns(rss.Import{}, nil, errors.New("boom"))), testImportID)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}
