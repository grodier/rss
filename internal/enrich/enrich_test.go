package enrich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/ingest"
	"github.com/grodier/rss/internal/rss"
)

// call is one Store write.
type call struct {
	method    string // SavePage, Retry or GiveUp
	articleID string
	page      rss.ArticlePage
	msg       string
	next      time.Time
}

type fakeStore struct {
	mu    sync.Mutex
	calls []call
}

func (s *fakeStore) ClaimNext(ctx context.Context, lease time.Duration) (rss.EnrichJob, error) {
	return rss.EnrichJob{}, rss.ErrNoRecord
}

func (s *fakeStore) record(c call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	return nil
}

func (s *fakeStore) SavePage(ctx context.Context, articleID string, p rss.ArticlePage) error {
	return s.record(call{method: "SavePage", articleID: articleID, page: p})
}

func (s *fakeStore) Retry(ctx context.Context, articleID, msg string, next time.Time) error {
	return s.record(call{method: "Retry", articleID: articleID, msg: msg, next: next})
}

func (s *fakeStore) GiveUp(ctx context.Context, articleID, msg string) error {
	return s.record(call{method: "GiveUp", articleID: articleID, msg: msg})
}

// serve returns the URL of a test server answering every request with
// status, contentType (unless "") and body, and header.
func serve(t *testing.T, status int, contentType, body string, header http.Header) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header()[k] = v
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/post"
}

// servePage returns the URL of a test server answering with the HTML page.
func servePage(t *testing.T, page string) string {
	return serve(t, http.StatusOK, "text/html; charset=utf-8", page, nil)
}

func newRunner(store *fakeStore) *Runner {
	r := &Runner{Store: store, Fetcher: fetch.New(fetch.Options{AllowPrivate: true})}
	r.setDefaults()
	return r
}

// process runs attempt number attempts of the job for pageURL and returns
// the one Store write it made.
func process(t *testing.T, pageURL string, attempts int) call {
	t.Helper()
	store := &fakeStore{}
	newRunner(store).process(t.Context(), rss.EnrichJob{ArticleID: "article-1", URL: pageURL, Attempts: attempts})
	if len(store.calls) != 1 {
		t.Fatalf("Store calls = %+v; want exactly one", store.calls)
	}
	c := store.calls[0]
	if c.articleID != "article-1" {
		t.Errorf("article ID = %q; want article-1", c.articleID)
	}
	return c
}

func TestProcessSaves(t *testing.T) {
	tests := []struct {
		name string
		page string
		want rss.ArticlePage
	}{
		{
			name: "all three tags",
			page: `<html><head><title>Site title</title>
				<meta property="og:title" content="The  article">
				<meta property="og:image" content="/img/lead.jpg">
				<meta name="description" content="What it's about.">
				</head><body></body></html>`,
			want: rss.ArticlePage{ImageURL: "/img/lead.jpg", Title: "The article", Description: "What it's about."},
		},
		{
			name: "only title and description",
			page: `<html><head><title> Plain
				title </title><meta name="description" content="  A
				description  spread out. "></head></html>`,
			want: rss.ArticlePage{Title: "Plain title", Description: "A description spread out."},
		},
		{
			name: "no tags",
			page: `<html><head></head><body><p>Hello</p></body></html>`,
			want: rss.ArticlePage{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageURL := servePage(t, tt.page)
			want := tt.want
			if want.ImageURL != "" {
				want.ImageURL = strings.TrimSuffix(pageURL, "/post") + want.ImageURL
			}
			c := process(t, pageURL, 1)
			if c.method != "SavePage" {
				t.Fatalf("method = %s (%s); want SavePage", c.method, c.msg)
			}
			if c.page != want {
				t.Errorf("page = %+v; want %+v", c.page, want)
			}
		})
	}
}

func TestProcessShortensLongDescription(t *testing.T) {
	long := strings.Repeat("word ", 200)
	c := process(t, servePage(t, `<meta name="description" content="`+long+`">`), 1)
	if c.method != "SavePage" {
		t.Fatalf("method = %s (%s); want SavePage", c.method, c.msg)
	}
	want := rss.Shorten(strings.TrimSpace(long), ingest.ExcerptLen)
	if c.page.Description != want {
		t.Errorf("description = %q; want %q", c.page.Description, want)
	}
	if !strings.HasSuffix(c.page.Description, "…") {
		t.Errorf("description = %q; want it shortened with …", c.page.Description)
	}
}

func TestPageOf(t *testing.T) {
	long := strings.Repeat("word\n\t ", 200)
	got := PageOf(discovery.PageInfo{
		Title:       "Site name",
		PageTitle:   "Page title",
		Description: "  Two\n\n  lines " + long,
		Image:       "https://example.com/a.jpg",
	})
	want := rss.ArticlePage{
		ImageURL:    "https://example.com/a.jpg",
		Title:       "Page title",
		Description: rss.Shorten("Two lines "+strings.TrimSpace(strings.Repeat("word ", 200)), ingest.ExcerptLen),
	}
	if got != want {
		t.Errorf("PageOf = %+v, want %+v", got, want)
	}
	if !strings.HasSuffix(got.Description, "…") {
		t.Errorf("description = %q; want it shortened with …", got.Description)
	}
}

func TestProcessGivesUp(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
	}{
		{"404", http.StatusNotFound, "text/html"},
		{"410", http.StatusGone, "text/html"},
		{"pdf", http.StatusOK, "application/pdf"},
		{"json", http.StatusOK, "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := process(t, serve(t, tt.status, tt.contentType, "%PDF-1.4", nil), 1)
			if c.method != "GiveUp" {
				t.Errorf("method = %s; want GiveUp", c.method)
			}
			if c.msg == "" {
				t.Error("msg is empty; want the reason")
			}
		})
	}
}

func TestProcessGivesUpOnRefusedAddress(t *testing.T) {
	store := &fakeStore{}
	r := &Runner{Store: store, Fetcher: fetch.New(fetch.Options{})} // blocks loopback
	r.setDefaults()
	r.process(t.Context(), rss.EnrichJob{ArticleID: "article-1", URL: servePage(t, "<title>x</title>"), Attempts: 1})
	if len(store.calls) != 1 || store.calls[0].method != "GiveUp" {
		t.Errorf("Store calls = %+v; want one GiveUp", store.calls)
	}
}

func TestProcessRetries(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		header   http.Header
		attempts int
		want     time.Duration // 0: gave up
	}{
		{"500 on attempt 1", http.StatusInternalServerError, nil, 1, time.Hour},
		{"500 on attempt 2", http.StatusInternalServerError, nil, 2, 6 * time.Hour},
		{"500 on attempt 3", http.StatusInternalServerError, nil, 3, 0},
		{"502 on attempt 1", http.StatusBadGateway, nil, 1, time.Hour},
		{"429 on attempt 1", http.StatusTooManyRequests, nil, 1, time.Hour},
		{"429 with Retry-After", http.StatusTooManyRequests, http.Header{"Retry-After": {"7200"}}, 1, 2 * time.Hour},
		{"503 with Retry-After", http.StatusServiceUnavailable, http.Header{"Retry-After": {"7200"}}, 1, 2 * time.Hour},
		{"Retry-After shorter than the delay", http.StatusTooManyRequests, http.Header{"Retry-After": {"60"}}, 2, 6 * time.Hour},
		{"Retry-After on a 500 is ignored", http.StatusInternalServerError, http.Header{"Retry-After": {"7200"}}, 1, time.Hour},
		{"Retry-After capped", http.StatusTooManyRequests, http.Header{"Retry-After": {"999999"}}, 1, ingest.MaxRetryDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageURL := serve(t, tt.status, "text/html", "", tt.header)
			start := time.Now()
			c := process(t, pageURL, tt.attempts)
			if tt.want == 0 {
				if c.method != "GiveUp" {
					t.Errorf("method = %s; want GiveUp", c.method)
				}
				return
			}
			if c.method != "Retry" {
				t.Fatalf("method = %s; want Retry", c.method)
			}
			if c.msg == "" {
				t.Error("msg is empty; want the reason")
			}
			if lo, hi := start.Add(tt.want), time.Now().Add(tt.want); c.next.Before(lo) || c.next.After(hi) {
				t.Errorf("next = %v; want about now + %v", c.next, tt.want)
			}
		})
	}
}

func TestProcessRetriesNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	pageURL := srv.URL + "/post"
	srv.Close() // connection refused

	if c := process(t, pageURL, 1); c.method != "Retry" {
		t.Errorf("attempt 1: method = %s; want Retry", c.method)
	}
	if c := process(t, pageURL, 3); c.method != "GiveUp" {
		t.Errorf("attempt 3: method = %s; want GiveUp", c.method)
	}
}

func TestProcessRecordsNothingWhenCanceled(t *testing.T) {
	pageURL := servePage(t, "<title>x</title>")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	store := &fakeStore{}
	newRunner(store).process(ctx, rss.EnrichJob{ArticleID: "article-1", URL: pageURL, Attempts: 1})
	if len(store.calls) != 0 {
		t.Errorf("Store calls = %+v; want none", store.calls)
	}
}

func TestRunRejectsLeaseNotAboveJobTimeout(t *testing.T) {
	r := &Runner{Store: &fakeStore{}, JobTimeout: time.Minute, Lease: time.Minute}
	if err := r.Run(t.Context()); err == nil {
		t.Error("Run() error = nil; want an error")
	}
}
