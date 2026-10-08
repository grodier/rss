package ingest

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

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/rss"
)

func TestExternalID(t *testing.T) {
	tests := []struct {
		name string
		item feedparse.Item
		want string
	}{
		{"ID wins over URL", feedparse.Item{ID: "id-1", URL: "https://example.com/a"}, "id-1"},
		{"URL when no ID", feedparse.Item{URL: "https://example.com/a", Title: "A"}, "https://example.com/a"},
		{"empty", feedparse.Item{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := externalID(tt.item); got != tt.want {
				t.Errorf("externalID = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("hash when no ID or URL", func(t *testing.T) {
		base := feedparse.Item{Title: "T", Summary: "S", Content: "C"}
		got := externalID(base)
		if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
			t.Fatalf("externalID = %q, want sha256: and 64 hex digits", got)
		}
		if again := externalID(base); again != got {
			t.Errorf("same input gave %q, then %q", got, again)
		}

		changed := map[string]feedparse.Item{
			"title":   {Title: "T2", Summary: "S", Content: "C"},
			"summary": {Title: "T", Summary: "S2", Content: "C"},
			"content": {Title: "T", Summary: "S", Content: "C2"},
		}
		for field, it := range changed {
			if id := externalID(it); id == got {
				t.Errorf("changing the %s didn't change the hash", field)
			}
		}
	})
}

func TestArticles(t *testing.T) {
	t.Run("fields copied, order preserved", func(t *testing.T) {
		published := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		items := []feedparse.Item{
			{ID: "b", URL: "https://example.com/b", Title: "B", Summary: "<p>sb</p>", Content: "<p>cb</p>", Published: published},
			{ID: "a", URL: "https://example.com/a", Title: "A"},
		}
		want := []rss.Article{
			{ExternalID: "b", URL: "https://example.com/b", Title: "B", Summary: "<p>sb</p>", Content: "<p>cb</p>", PublishedAt: published},
			{ExternalID: "a", URL: "https://example.com/a", Title: "A"},
		}
		got := Articles(items)
		if len(got) != len(want) {
			t.Fatalf("got %d articles, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("article %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("duplicates dropped, first kept", func(t *testing.T) {
		items := []feedparse.Item{
			{ID: "https://example.com/x", Title: "first"},
			{ID: "https://example.com/x", Title: "same ID"},
			{URL: "https://example.com/x", Title: "URL equals earlier ID"},
			{ID: "y", Title: "other"},
		}
		got := Articles(items)
		if len(got) != 2 {
			t.Fatalf("got %d articles, want 2: %+v", len(got), got)
		}
		if got[0].Title != "first" || got[1].ExternalID != "y" {
			t.Errorf("got %+v, want first and y", got)
		}
	})

	t.Run("empty items skipped", func(t *testing.T) {
		got := Articles([]feedparse.Item{{}, {ID: "a"}, {}})
		if len(got) != 1 || got[0].ExternalID != "a" {
			t.Errorf("got %+v, want only a", got)
		}
	})

	t.Run("capped at MaxArticles", func(t *testing.T) {
		items := make([]feedparse.Item, 250)
		for i := range items {
			items[i].ID = fmt.Sprintf("id-%d", i)
		}
		got := Articles(items)
		if len(got) != MaxArticles {
			t.Fatalf("got %d articles, want %d", len(got), MaxArticles)
		}
		if got[0].ExternalID != "id-0" || got[MaxArticles-1].ExternalID != "id-199" {
			t.Errorf("got %q to %q, want id-0 to id-199", got[0].ExternalID, got[MaxArticles-1].ExternalID)
		}
	})

	t.Run("nil and empty input", func(t *testing.T) {
		if got := Articles(nil); len(got) != 0 {
			t.Errorf("Articles(nil) = %+v, want empty", got)
		}
		if got := Articles([]feedparse.Item{}); len(got) != 0 {
			t.Errorf("Articles(empty) = %+v, want empty", got)
		}
	})
}

const testRSS = `<?xml version="1.0"?>
<rss version="2.0">
<channel>
  <title>Example Feed</title>
  <link>https://example.com/</link>
  <description>An example</description>
  <item><guid>item-1</guid><title>One</title><link>https://example.com/1</link></item>
  <item><title>Two</title><link>https://example.com/2</link></item>
</channel>
</rss>`

const testHTML = `<!doctype html><html><head><title>Not a feed</title></head><body>hi</body></html>`

// fakeStore records the arguments of SaveFetch, RecordFailure,
// RecordNotModified and MarkGone.
type fakeStore struct {
	calls    int
	feed     rss.Feed
	articles []rss.Article
	result   rss.FetchResult
	err      error

	failures  []failure
	recordErr error

	notModified    []notModified
	notModifiedErr error

	gone    []gone
	goneErr error
}

type gone struct {
	id     string
	ctxErr error // the context's error when MarkGone was called
}

type notModified struct {
	id   string
	next time.Time
}

type failure struct {
	id, msg string
	next    time.Time
	ctxErr  error // the context's error when RecordFailure was called
}

func (s *fakeStore) SaveFetch(_ context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error) {
	s.calls++
	s.feed = f
	s.articles = articles
	return s.result, s.err
}

func (s *fakeStore) RecordFailure(ctx context.Context, id, msg string, next time.Time) error {
	s.failures = append(s.failures, failure{id, msg, next, ctx.Err()})
	return s.recordErr
}

func (s *fakeStore) RecordNotModified(_ context.Context, id string, next time.Time) error {
	s.notModified = append(s.notModified, notModified{id, next})
	return s.notModifiedErr
}

func (s *fakeStore) MarkGone(ctx context.Context, id string) error {
	s.gone = append(s.gone, gone{id, ctx.Err()})
	return s.goneErr
}

func serve(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRefresh(t *testing.T) {
	fetcher := fetch.New(fetch.Options{AllowPrivate: true})

	t.Run("saves feed and articles", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{result: rss.FetchResult{New: 2}}
		r := &Refresher{Fetcher: fetcher, Store: store}

		before := time.Now()
		res, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL + "/feed.xml", Title: "old"})
		after := time.Now()
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if res != store.result {
			t.Errorf("result = %+v, want %+v", res, store.result)
		}
		if store.calls != 1 {
			t.Fatalf("SaveFetch called %d times, want 1", store.calls)
		}
		checkWithin(t, "NextFetch", store.feed.NextFetch, before, after, RefreshInterval)
		gotFeed := store.feed
		gotFeed.NextFetch = time.Time{}
		wantFeed := rss.Feed{ID: "feed-1", Title: "Example Feed", Description: "An example", SiteUrl: "https://example.com/"}
		if gotFeed != wantFeed {
			t.Errorf("feed = %+v, want %+v", gotFeed, wantFeed)
		}
		if len(store.articles) != 2 {
			t.Fatalf("got %d articles, want 2", len(store.articles))
		}
		if got := store.articles[0].ExternalID; got != "item-1" {
			t.Errorf("articles[0].ExternalID = %q, want item-1", got)
		}
		if got := store.articles[1].ExternalID; got != "https://example.com/2" {
			t.Errorf("articles[1].ExternalID = %q, want https://example.com/2", got)
		}
		if len(store.failures) != 0 {
			t.Errorf("RecordFailure called %d times, want 0", len(store.failures))
		}
	})

	t.Run("sends stored validators", func(t *testing.T) {
		tests := []struct {
			name               string
			etag, lastModified string
			wantINM, wantIMS   []string
		}{
			{"both set", `W/"v1"`, "Mon, 02 Jan 2006 15:04:05 GMT", []string{`W/"v1"`}, []string{"Mon, 02 Jan 2006 15:04:05 GMT"}},
			{"ETag only", `"v2"`, "", []string{`"v2"`}, nil},
			{"Last-Modified only", "", "Tue, 03 Jan 2006 15:04:05 GMT", nil, []string{"Tue, 03 Jan 2006 15:04:05 GMT"}},
			{"neither", "", "", nil, nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var got http.Header
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got = r.Header.Clone()
					w.Header().Set("Content-Type", "application/rss+xml")
					fmt.Fprint(w, testRSS)
				}))
				t.Cleanup(srv.Close)

				r := &Refresher{Fetcher: fetcher, Store: &fakeStore{}}
				feed := rss.Feed{ID: "feed-1", Url: srv.URL, ETag: tt.etag, LastModified: tt.lastModified}
				if _, err := r.Refresh(context.Background(), feed); err != nil {
					t.Fatalf("Refresh: %v", err)
				}
				if v := got.Values("If-None-Match"); !slices.Equal(v, tt.wantINM) {
					t.Errorf("If-None-Match = %q, want %q", v, tt.wantINM)
				}
				if v := got.Values("If-Modified-Since"); !slices.Equal(v, tt.wantIMS) {
					t.Errorf("If-Modified-Since = %q, want %q", v, tt.wantIMS)
				}
			})
		}
	})

	t.Run("304 records not modified", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotModified)
		}))
		t.Cleanup(srv.Close)
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}

		before := time.Now()
		res, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL, ETag: `"v1"`, ConsecutiveFailures: 2})
		after := time.Now()
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if want := (rss.FetchResult{NotModified: true}); res != want {
			t.Errorf("result = %+v, want %+v", res, want)
		}
		if store.calls != 0 || len(store.failures) != 0 {
			t.Errorf("SaveFetch called %d times, RecordFailure %d times; want 0, 0", store.calls, len(store.failures))
		}
		if len(store.notModified) != 1 {
			t.Fatalf("RecordNotModified called %d times, want 1", len(store.notModified))
		}
		if id := store.notModified[0].id; id != "feed-1" {
			t.Errorf("RecordNotModified id = %q, want feed-1", id)
		}
		checkWithin(t, "RecordNotModified next", store.notModified[0].next, before, after, RefreshInterval)
	})

	t.Run("RecordNotModified error returned and not recorded", func(t *testing.T) {
		srv := serve(t, http.StatusNotModified, "", "")
		store := &fakeStore{notModifiedErr: rss.ErrNoRecord}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		if len(store.failures) != 0 {
			t.Errorf("RecordFailure called %d times, want 0", len(store.failures))
		}
	})

	t.Run("saves response validators", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Header().Set("ETag", `W/"new"`)
			w.Header().Set("Last-Modified", "Wed, 04 Jan 2006 15:04:05 GMT")
			fmt.Fprint(w, testRSS)
		}))
		t.Cleanup(srv.Close)
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}
		if _, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL, ETag: `"old"`, LastModified: "old"}); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if store.feed.ETag != `W/"new"` || store.feed.LastModified != "Wed, 04 Jan 2006 15:04:05 GMT" {
			t.Errorf("got ETag %q, LastModified %q; want %q, %q", store.feed.ETag, store.feed.LastModified, `W/"new"`, "Wed, 04 Jan 2006 15:04:05 GMT")
		}
	})

	t.Run("response without validators clears them", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}
		if _, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL, ETag: `"old"`, LastModified: "old"}); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if store.feed.ETag != "" || store.feed.LastModified != "" {
			t.Errorf("got ETag %q, LastModified %q; want both empty", store.feed.ETag, store.feed.LastModified)
		}
	})

	errorTests := []struct {
		name    string
		feedURL func(t *testing.T) string
		wantErr error
		wantMsg string // substring of the recorded message
	}{
		{
			name:    "404",
			feedURL: func(t *testing.T) string { return serve(t, http.StatusNotFound, "text/plain", "not found").URL },
			wantErr: ErrUnreachable,
			wantMsg: "status 404",
		},
		{
			name:    "HTML page",
			feedURL: func(t *testing.T) string { return serve(t, http.StatusOK, "text/html", testHTML).URL },
			wantErr: ErrNotFeed,
			wantMsg: ErrNotFeed.Error(),
		},
		{
			name: "server down",
			feedURL: func(t *testing.T) string {
				srv := httptest.NewServer(http.NotFoundHandler())
				srv.Close()
				return srv.URL
			},
			wantErr: ErrUnreachable,
			wantMsg: ErrUnreachable.Error(),
		},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			r := &Refresher{Fetcher: fetcher, Store: store}
			before := time.Now()
			_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: tt.feedURL(t), ConsecutiveFailures: 2})
			after := time.Now()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if store.calls != 0 {
				t.Errorf("SaveFetch called %d times, want 0", store.calls)
			}
			if len(store.failures) != 1 {
				t.Fatalf("RecordFailure called %d times, want 1", len(store.failures))
			}
			f := store.failures[0]
			if f.id != "feed-1" {
				t.Errorf("RecordFailure id = %q, want feed-1", f.id)
			}
			if f.msg != err.Error() || !strings.Contains(f.msg, tt.wantMsg) {
				t.Errorf("RecordFailure msg = %q, want %q (containing %q)", f.msg, err.Error(), tt.wantMsg)
			}
			// Third failure in a row: RetryAt(now, 3).
			checkWithin(t, "RecordFailure next", f.next, before, after, 4*RefreshInterval)
		})
	}

	t.Run("410 marks the feed gone", func(t *testing.T) {
		srv := serve(t, http.StatusGone, "text/plain", "gone")
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, ErrGone) {
			t.Errorf("err = %v, want %v", err, ErrGone)
		}
		if errors.Is(err, ErrUnreachable) {
			t.Errorf("err = %v, want it not to match %v", err, ErrUnreachable)
		}
		if len(store.gone) != 1 || store.gone[0].id != "feed-1" {
			t.Fatalf("MarkGone calls = %+v, want one for feed-1", store.gone)
		}
		if len(store.failures) != 0 || store.calls != 0 {
			t.Errorf("RecordFailure called %d times, SaveFetch %d times; want 0, 0", len(store.failures), store.calls)
		}
	})

	t.Run("410 with expired deadline is recorded", func(t *testing.T) {
		store := &fakeStore{}
		r := &Refresher{Fetcher: &stubFetcher{status: http.StatusGone}, Store: store}
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := r.Refresh(ctx, rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})
		if !errors.Is(err, ErrGone) {
			t.Errorf("err = %v, want %v", err, ErrGone)
		}
		if len(store.gone) != 1 {
			t.Fatalf("MarkGone called %d times, want 1", len(store.gone))
		}
		if err := store.gone[0].ctxErr; err != nil {
			t.Errorf("MarkGone ctx.Err() = %v, want nil", err)
		}
	})

	t.Run("410 with canceled context records nothing", func(t *testing.T) {
		store := &fakeStore{}
		r := &Refresher{Fetcher: &stubFetcher{status: http.StatusGone}, Store: store}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := r.Refresh(ctx, rss.Feed{ID: "feed-1", Url: "https://example.com/feed"})
		if !errors.Is(err, ErrGone) {
			t.Errorf("err = %v, want %v", err, ErrGone)
		}
		if len(store.gone) != 0 {
			t.Errorf("MarkGone called %d times, want 0", len(store.gone))
		}
	})

	t.Run("MarkGone error is joined", func(t *testing.T) {
		srv := serve(t, http.StatusGone, "text/plain", "gone")
		recErr := errors.New("db down")
		store := &fakeStore{goneErr: recErr}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, ErrGone) || !errors.Is(err, recErr) {
			t.Errorf("err = %v, want it to match %v and %v", err, ErrGone, recErr)
		}
	})

	t.Run("canceled context records nothing", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := r.Refresh(ctx, rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("err = %v, want %v", err, ErrUnreachable)
		}
		if len(store.failures) != 0 || store.calls != 0 {
			t.Errorf("RecordFailure called %d times, SaveFetch %d times; want 0, 0", len(store.failures), store.calls)
		}
	})

	t.Run("expired deadline is recorded", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{}
		r := &Refresher{Fetcher: fetcher, Store: store}
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := r.Refresh(ctx, rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("err = %v, want %v", err, ErrUnreachable)
		}
		if len(store.failures) != 1 {
			t.Fatalf("RecordFailure called %d times, want 1", len(store.failures))
		}
		if err := store.failures[0].ctxErr; err != nil {
			t.Errorf("RecordFailure ctx.Err() = %v, want nil", err)
		}
	})

	t.Run("RecordFailure error is joined", func(t *testing.T) {
		srv := serve(t, http.StatusNotFound, "text/plain", "not found")
		recErr := errors.New("db down")
		store := &fakeStore{recordErr: recErr}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("err = %v, want it to match %v", err, ErrUnreachable)
		}
		if !errors.Is(err, recErr) {
			t.Errorf("err = %v, want it to match %v", err, recErr)
		}
	})

	t.Run("schedules by server hints", func(t *testing.T) {
		tests := []struct {
			name     string
			status   int
			header   string // name: value
			failures int    // feed.ConsecutiveFailures
			want     time.Duration
		}{
			{"max-age of 6h", http.StatusOK, "Cache-Control: max-age=21600", 0, 6 * time.Hour},
			{"2-day max-age capped", http.StatusOK, "Cache-Control: max-age=172800", 0, MaxRetryDelay},
			{"short max-age keeps the interval", http.StatusOK, "Cache-Control: max-age=60", 0, RefreshInterval},
			{"no-cache keeps the interval", http.StatusOK, "Cache-Control: no-cache", 0, RefreshInterval},
			{"304 with max-age", http.StatusNotModified, "Cache-Control: max-age=21600", 0, 6 * time.Hour},
			{"429 Retry-After on a first failure", http.StatusTooManyRequests, "Retry-After: 7200", 0, 2 * time.Hour},
			{"503 3-day Retry-After capped", http.StatusServiceUnavailable, "Retry-After: 259200", 0, MaxRetryDelay},
			{"short Retry-After keeps the backoff", http.StatusTooManyRequests, "Retry-After: 60", 2, 4 * RefreshInterval},
			{"Retry-After ignored on 404", http.StatusNotFound, "Retry-After: 7200", 0, RefreshInterval},
			{"max-age ignored on failure", http.StatusServiceUnavailable, "Cache-Control: max-age=21600", 0, RefreshInterval},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				name, value, _ := strings.Cut(tt.header, ": ")
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set(name, value)
					w.Header().Set("Content-Type", "application/rss+xml")
					w.WriteHeader(tt.status)
					fmt.Fprint(w, testRSS)
				}))
				t.Cleanup(srv.Close)
				store := &fakeStore{}
				r := &Refresher{Fetcher: fetcher, Store: store}

				before := time.Now()
				_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL, ConsecutiveFailures: tt.failures})
				after := time.Now()

				var next time.Time
				switch {
				case tt.status == http.StatusOK && store.calls == 1:
					next = store.feed.NextFetch
				case tt.status == http.StatusNotModified && len(store.notModified) == 1:
					next = store.notModified[0].next
				case tt.status >= 400 && len(store.failures) == 1:
					next = store.failures[0].next
				default:
					t.Fatalf("Refresh: err = %v, SaveFetch calls %d, RecordNotModified %d, RecordFailure %d",
						err, store.calls, len(store.notModified), len(store.failures))
				}
				if tt.status == http.StatusTooManyRequests || tt.status == http.StatusServiceUnavailable {
					if !errors.Is(err, ErrUnreachable) {
						t.Errorf("err = %v, want %v", err, ErrUnreachable)
					}
				}
				checkWithin(t, "next fetch", next, before, after, tt.want)
			})
		}
	})

	t.Run("store error returned and not recorded", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{err: rss.ErrNoRecord}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
		if len(store.failures) != 0 {
			t.Errorf("RecordFailure called %d times, want 0", len(store.failures))
		}
	})
}

// stubFetcher answers every request with status, ignoring ctx, so tests can
// see what Refresh does with an expired or canceled context after a response.
type stubFetcher struct {
	status int
}

func (f *stubFetcher) Get(_ context.Context, rawURL string, _ http.Header) (*fetch.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return &fetch.Response{StatusCode: f.status, URL: u, Header: http.Header{}}, nil
}
