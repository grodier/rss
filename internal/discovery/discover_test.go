package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/fetch"
)

// fakeFetcher serves canned responses keyed by URL string. URLs with no
// response or error get a 404. A response without a URL gets the requested
// one (no redirect).
type fakeFetcher struct {
	responses map[string]*fetch.Response
	errs      map[string]error
	onGet     func(rawURL string) // optional, called before answering

	mu   sync.Mutex
	gets []string
}

func (f *fakeFetcher) Get(ctx context.Context, rawURL string) (*fetch.Response, error) {
	f.mu.Lock()
	f.gets = append(f.gets, rawURL)
	f.mu.Unlock()
	if f.onGet != nil {
		f.onGet(rawURL)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err, ok := f.errs[rawURL]; ok {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	resp, ok := f.responses[rawURL]
	if !ok {
		return &fetch.Response{URL: u, StatusCode: http.StatusNotFound, Header: http.Header{}}, nil
	}
	r := *resp
	if r.URL == nil {
		r.URL = u
	}
	if r.Header == nil {
		r.Header = http.Header{}
	}
	return &r, nil
}

func htmlResp(body string) *fetch.Response {
	return &fetch.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       []byte(body),
	}
}

func rssResp(title, link string) *fetch.Response {
	body := fmt.Sprintf(`<?xml version="1.0"?><rss version="2.0"><channel>
		<title>%s</title><link>%s</link><description>About %s</description>
		<item><guid>g1</guid><title>First</title><link>https://example.com/1</link></item>
		<item><guid>g2</guid><title>Second</title><link>https://example.com/2</link></item>
		</channel></rss>`, title, link, title)
	return &fetch.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/rss+xml"}},
		Body:       []byte(body),
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func feedURLs(feeds []FeedInfo) []string {
	var urls []string
	for _, f := range feeds {
		urls = append(urls, f.URL)
	}
	return urls
}

func assertURLs(t *testing.T, got []FeedInfo, want ...string) {
	t.Helper()
	if g := strings.Join(feedURLs(got), " "); g != strings.Join(want, " ") {
		t.Errorf("feeds = [%s], want [%s]", g, strings.Join(want, " "))
	}
}

// assertItems checks items' "ID:Title" strings.
func assertItems(t *testing.T, got []feedparse.Item, want ...string) {
	t.Helper()
	var g []string
	for _, it := range got {
		g = append(g, it.ID+":"+it.Title)
	}
	if strings.Join(g, " ") != strings.Join(want, " ") {
		t.Errorf("items = %v, want %v", g, want)
	}
}

func TestDiscoverAdvertisedFeeds(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/": htmlResp(`<html><head><title>Example Site</title>
			<meta name="description" content="A site.">
			<link rel="alternate" type="application/rss+xml" title="Link RSS" href="/rss.xml">
			<link rel="alternate" type="application/atom+xml" href="/atom.xml">
			</head></html>`),
		"https://example.com/rss.xml": rssResp("Example Posts", "https://example.com/"),
		"https://example.com/atom.xml": {
			StatusCode: http.StatusOK,
			Body: []byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
				<title>Example Atom</title><link rel="alternate" href="https://example.com/"/></feed>`),
		},
	}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	want := SiteInfo{Key: "example.com", URL: "https://example.com/", Title: "Example Site", Description: "A site."}
	if res.Site != want {
		t.Errorf("Site = %+v, want %+v", res.Site, want)
	}
	assertURLs(t, res.Feeds, "https://example.com/rss.xml", "https://example.com/atom.xml")
	if len(res.Feeds) == 2 {
		if got := res.Feeds[0]; got.Title != "Example Posts" || got.Description != "About Example Posts" || got.SiteURL != "https://example.com/" {
			t.Errorf("Feeds[0] = %+v", got)
		}
		if got := res.Feeds[1].Title; got != "Example Atom" {
			t.Errorf("Feeds[1].Title = %q, want %q", got, "Example Atom")
		}
		assertItems(t, res.Feeds[0].Items, "g1:First", "g2:Second")
	}
}

func TestDiscoverSkipsInvalidCandidates(t *testing.T) {
	f := &fakeFetcher{
		responses: map[string]*fetch.Response{
			"https://example.com/": htmlResp(`
				<link rel="alternate" type="application/rss+xml" href="/missing.xml">
				<link rel="alternate" type="application/rss+xml" href="/page.xml">
				<link rel="alternate" type="application/rss+xml" href="/broken.xml">
				<link rel="alternate" type="application/rss+xml" title="Good" href="/good.xml">`),
			"https://example.com/page.xml": htmlResp(`<html><title>Not a feed</title></html>`),
			"https://example.com/good.xml": rssResp("", "https://example.com/"),
		},
		errs: map[string]error{"https://example.com/broken.xml": errors.New("connection reset")},
	}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	assertURLs(t, res.Feeds, "https://example.com/good.xml")
	if len(res.Feeds) == 1 && res.Feeds[0].Title != "Good" {
		t.Errorf("Title = %q, want the candidate title %q", res.Feeds[0].Title, "Good")
	}
	if res.Site.Title != "example.com" {
		t.Errorf("Site.Title = %q, want the key", res.Site.Title)
	}
}

func TestDiscoverCommonPathsFallback(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/blog/": htmlResp(`<title>Blog</title>`),
		// /feed is not in responses: 404.
		"https://example.com/rss.xml": rssResp("Fallback", "https://example.com/"),
	}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/blog/"))
	if err != nil {
		t.Fatal(err)
	}
	assertURLs(t, res.Feeds, "https://example.com/rss.xml")
	if res.Site.URL != "https://example.com/" {
		t.Errorf("Site.URL = %q", res.Site.URL)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.gets) != 1+len(CommonFeedPaths) {
		t.Errorf("fetched %d URLs, want %d: %v", len(f.gets), 1+len(CommonFeedPaths), f.gets)
	}
}

func TestDiscoverNoFeeds(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/": htmlResp(`<title>Nothing</title>`),
	}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Feeds) != 0 {
		t.Errorf("Feeds = %+v, want none", res.Feeds)
	}
	if res.Site.Title != "Nothing" {
		t.Errorf("Site.Title = %q", res.Site.Title)
	}
}

func TestDiscoverTruncatedPage(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/": htmlResp(`<html><head><title>Cut Off</title>
			<link rel="alternate" type="application/rss+xml" href="/posts.xml"><me`),
		"https://example.com/posts.xml": rssResp("Posts", "https://example.com/"),
	}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Site.Title != "Cut Off" {
		t.Errorf("Site.Title = %q, want %q", res.Site.Title, "Cut Off")
	}
	assertURLs(t, res.Feeds, "https://example.com/posts.xml")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.gets) != 2 {
		t.Errorf("fetched %v, want the page and its advertised feed only", f.gets)
	}
}

func TestDiscoverInputIsFeed(t *testing.T) {
	tests := []struct {
		name     string
		link     string
		wantKey  string
		wantURL  string
		feedName string
	}{
		{"site link", "https://blog.example.org/posts", "blog.example.org", "https://blog.example.org/", "X Feed"},
		{"no site link", "", "feeds.example.net", "https://feeds.example.net/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeFetcher{responses: map[string]*fetch.Response{
				"https://feeds.example.net/x.xml": rssResp(tt.feedName, tt.link),
			}}
			d := &Discoverer{Fetcher: f}

			res, err := d.Discover(context.Background(), mustURL(t, "https://feeds.example.net/x.xml"))
			if err != nil {
				t.Fatal(err)
			}
			if res.Site.Key != tt.wantKey || res.Site.URL != tt.wantURL {
				t.Errorf("Site = %+v, want Key %q URL %q", res.Site, tt.wantKey, tt.wantURL)
			}
			wantTitle := tt.feedName
			if wantTitle == "" {
				wantTitle = tt.wantKey
			}
			if res.Site.Title != wantTitle {
				t.Errorf("Site.Title = %q, want %q", res.Site.Title, wantTitle)
			}
			assertURLs(t, res.Feeds, "https://feeds.example.net/x.xml")
			if len(res.Feeds) == 1 {
				assertItems(t, res.Feeds[0].Items, "g1:First", "g2:Second")
			}
			if len(f.gets) != 1 {
				t.Errorf("fetched %v, want only the feed", f.gets)
			}
		})
	}
}

func TestDiscoverHomePageErrors(t *testing.T) {
	fetchErr := errors.New("dial failed")
	t.Run("fetch error", func(t *testing.T) {
		d := &Discoverer{Fetcher: &fakeFetcher{errs: map[string]error{"https://example.com/": fetchErr}}}
		_, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
		if !errors.Is(err, fetchErr) {
			t.Errorf("err = %v, want %v", err, fetchErr)
		}
	})
	t.Run("500", func(t *testing.T) {
		d := &Discoverer{Fetcher: &fakeFetcher{responses: map[string]*fetch.Response{
			"https://example.com/": {StatusCode: http.StatusInternalServerError},
		}}}
		_, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("err = %v, want ErrUnreachable", err)
		}
	})
}

func TestDiscoverRedirect(t *testing.T) {
	r := htmlResp(`<title>WWW</title>`)
	r.URL = mustURL(t, "https://WWW.Example.com:443/home")
	f := &fakeFetcher{responses: map[string]*fetch.Response{"https://example.com/": r}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Site.Key != "example.com" || res.Site.URL != "https://www.example.com/" {
		t.Errorf("Site = %+v", res.Site)
	}
	// Common paths are resolved against the final origin.
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.gets, "https://www.example.com/feed") {
		t.Errorf("gets = %v, want common paths on https://www.example.com", f.gets)
	}
}

func TestDiscoverDeduplicatesFinalURLs(t *testing.T) {
	redirected := rssResp("Feed", "https://example.com/")
	redirected.URL = mustURL(t, "https://EXAMPLE.com/feed.xml#top")
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/": htmlResp(`
			<link rel="alternate" type="application/rss+xml" href="/feed.xml">
			<link rel="alternate" type="application/rss+xml" href="/old-feed">
			<link rel="alternate" type="application/atom+xml" href="/feed.xml?ref=atom">`),
		"https://example.com/feed.xml":          rssResp("Feed", "https://example.com/"),
		"https://example.com/old-feed":          redirected,
		"https://example.com/feed.xml?ref=atom": rssResp("Other", "https://example.com/"),
	}}
	d := &Discoverer{Fetcher: f}

	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	assertURLs(t, res.Feeds, "https://example.com/feed.xml", "https://example.com/feed.xml?ref=atom")
}

func TestDiscoverLimits(t *testing.T) {
	var links strings.Builder
	responses := map[string]*fetch.Response{}
	for i := range 20 {
		fmt.Fprintf(&links, `<link rel="alternate" type="application/rss+xml" href="/f%d.xml">`, i)
		responses[fmt.Sprintf("https://example.com/f%d.xml", i)] = rssResp(fmt.Sprintf("F%d", i), "")
	}
	responses["https://example.com/"] = htmlResp(links.String())

	t.Run("MaxFeeds", func(t *testing.T) {
		d := &Discoverer{Fetcher: &fakeFetcher{responses: responses}, MaxFeeds: 3, Concurrency: 8}
		res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
		if err != nil {
			t.Fatal(err)
		}
		assertURLs(t, res.Feeds, "https://example.com/f0.xml", "https://example.com/f1.xml", "https://example.com/f2.xml")
	})

	t.Run("defaults", func(t *testing.T) {
		f := &fakeFetcher{responses: responses}
		d := &Discoverer{Fetcher: f}
		res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Feeds) != defaultMaxFeeds {
			t.Errorf("got %d feeds, want %d", len(res.Feeds), defaultMaxFeeds)
		}
		if len(f.gets) != 1+defaultMaxCandidates {
			t.Errorf("fetched %d URLs, want %d", len(f.gets), 1+defaultMaxCandidates)
		}
	})

	t.Run("Concurrency", func(t *testing.T) {
		var mu sync.Mutex
		inFlight, peak := 0, 0
		release := make(chan struct{})
		var once sync.Once
		f := &fakeFetcher{responses: responses}
		f.onGet = func(rawURL string) {
			if rawURL == "https://example.com/" {
				return
			}
			mu.Lock()
			inFlight++
			peak = max(peak, inFlight)
			n := inFlight
			mu.Unlock()
			if n == 2 {
				once.Do(func() { close(release) })
			}
			<-release
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
		d := &Discoverer{Fetcher: f, Concurrency: 2}
		if _, err := d.Discover(context.Background(), mustURL(t, "https://example.com/")); err != nil {
			t.Fatal(err)
		}
		if peak != 2 {
			t.Errorf("peak concurrency = %d, want 2", peak)
		}
	})
}

func TestDiscoverCanceled(t *testing.T) {
	t.Run("before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		d := &Discoverer{Fetcher: &fakeFetcher{responses: map[string]*fetch.Response{
			"https://example.com/": htmlResp(``),
		}}}
		if _, err := d.Discover(ctx, mustURL(t, "https://example.com/")); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("while checking candidates", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://example.com/": htmlResp(`<title>T</title>`),
		}}
		f.onGet = func(rawURL string) {
			if rawURL == "https://example.com/feed" {
				cancel()
			}
		}
		d := &Discoverer{Fetcher: f, Concurrency: 1}
		res, err := d.Discover(ctx, mustURL(t, "https://example.com/"))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v (result %+v), want context.Canceled", err, res)
		}
	})
}

func TestDiscoverWithFetchClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<title>Local</title><link rel="alternate" type="application/rss+xml" href="/old">`)
	})
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/feed.xml", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Local Feed</title><link>/</link></channel></rss>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	d := &Discoverer{Fetcher: fetch.New(fetch.Options{AllowPrivate: true})}
	res, err := d.Discover(context.Background(), mustURL(t, srv.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Site.Title != "Local" || res.Site.URL != srv.URL+"/" {
		t.Errorf("Site = %+v", res.Site)
	}
	assertURLs(t, res.Feeds, srv.URL+"/feed.xml")
	if len(res.Feeds) == 1 && (res.Feeds[0].Title != "Local Feed" || res.Feeds[0].SiteURL != srv.URL+"/") {
		t.Errorf("Feeds[0] = %+v", res.Feeds[0])
	}
}
