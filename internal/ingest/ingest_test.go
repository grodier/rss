package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// fakeStore records the arguments of SaveFetch.
type fakeStore struct {
	calls    int
	feed     rss.Feed
	articles []rss.Article
	result   rss.FetchResult
	err      error
}

func (s *fakeStore) SaveFetch(_ context.Context, f rss.Feed, articles []rss.Article) (rss.FetchResult, error) {
	s.calls++
	s.feed = f
	s.articles = articles
	return s.result, s.err
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

		res, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL + "/feed.xml", Title: "old"})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if res != store.result {
			t.Errorf("result = %+v, want %+v", res, store.result)
		}
		if store.calls != 1 {
			t.Fatalf("SaveFetch called %d times, want 1", store.calls)
		}
		wantFeed := rss.Feed{ID: "feed-1", Title: "Example Feed", Description: "An example", SiteUrl: "https://example.com/"}
		if store.feed != wantFeed {
			t.Errorf("feed = %+v, want %+v", store.feed, wantFeed)
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
	})

	errorTests := []struct {
		name    string
		feedURL func(t *testing.T) string
		wantErr error
	}{
		{
			name:    "404",
			feedURL: func(t *testing.T) string { return serve(t, http.StatusNotFound, "text/plain", "not found").URL },
			wantErr: ErrUnreachable,
		},
		{
			name:    "HTML page",
			feedURL: func(t *testing.T) string { return serve(t, http.StatusOK, "text/html", testHTML).URL },
			wantErr: ErrNotFeed,
		},
		{
			name: "server down",
			feedURL: func(t *testing.T) string {
				srv := httptest.NewServer(http.NotFoundHandler())
				srv.Close()
				return srv.URL
			},
			wantErr: ErrUnreachable,
		},
	}
	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			r := &Refresher{Fetcher: fetcher, Store: store}
			_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: tt.feedURL(t)})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if store.calls != 0 {
				t.Errorf("SaveFetch called %d times, want 0", store.calls)
			}
		})
	}

	t.Run("store error returned", func(t *testing.T) {
		srv := serve(t, http.StatusOK, "application/rss+xml", testRSS)
		store := &fakeStore{err: rss.ErrNoRecord}
		r := &Refresher{Fetcher: fetcher, Store: store}
		_, err := r.Refresh(context.Background(), rss.Feed{ID: "feed-1", Url: srv.URL})
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want %v", err, rss.ErrNoRecord)
		}
	})
}
