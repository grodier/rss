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

func (f *fakeFetcher) Get(ctx context.Context, rawURL string, _ http.Header) (*fetch.Response, error) {
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
		// It would key to rss.NewsletterSiteHost, the newsletters' site.
		{"site link to an invalid host", "http://www.newsletters/", "feeds.example.net", "https://feeds.example.net/", "X Feed"},
		{"site link to an IP address", "http://192.0.2.1/", "feeds.example.net", "https://feeds.example.net/", "X Feed"},
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

// HomePage says whether the site's details came from its home page, which
// alone may replace an existing site's (psql.upsertSite).
func TestDiscoverHomePage(t *testing.T) {
	page := `<html><head><title>T</title><link rel="alternate" type="application/rss+xml" href="/rss.xml"></head></html>`
	redirected := htmlResp(page)
	redirected.URL = mustURL(t, "https://www.example.com/")
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/":            htmlResp(page),
		"https://example.com/?ref=x":      htmlResp(page),
		"https://example.com/about":       htmlResp(page),
		"https://example.com/index.html":  redirected,
		"https://example.com/rss.xml":     rssResp("Posts", "https://example.com/"),
		"https://www.example.com/rss.xml": rssResp("Posts", "https://example.com/"),
	}}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/", true},
		{"https://example.com/index.html", true}, // led to the home page
		{"https://example.com/?ref=x", false},
		{"https://example.com/about", false},
		{"https://example.com/rss.xml", false}, // a feed
	}
	for _, tt := range tests {
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, tt.url))
		if err != nil {
			t.Fatalf("%s: %v", tt.url, err)
		}
		if res.HomePage != tt.want {
			t.Errorf("%s: HomePage = %v, want %v", tt.url, res.HomePage, tt.want)
		}
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

// checkOutcomes returns Checked's "URL source: outcome" strings.
func checkOutcomes(checked []CandidateCheck) []string {
	var out []string
	for _, c := range checked {
		out = append(out, c.Candidate.URL.String()+" "+c.Source+": "+c.Outcome)
	}
	return out
}

func assertOutcomes(t *testing.T, got []CandidateCheck, want ...string) {
	t.Helper()
	if g := checkOutcomes(got); !slices.Equal(g, want) {
		t.Errorf("Checked =\n  %s\nwant\n  %s", strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestDiscoverChecked(t *testing.T) {
	f := &fakeFetcher{
		responses: map[string]*fetch.Response{
			"https://example.com/": htmlResp(`
				<link rel="alternate" type="application/rss+xml" href="/good.xml">
				<link rel="alternate" type="application/rss+xml" href="/missing.xml">
				<link rel="alternate" type="application/rss+xml" href="/page.xml">
				<link rel="alternate" type="application/rss+xml" href="/broken.xml">
				<link rel="alternate" type="application/rss+xml" href="/moved.xml">`),
			"https://example.com/good.xml": rssResp("Good", "https://example.com/"),
			"https://example.com/page.xml": htmlResp(`<html><title>Not a feed</title></html>`),
			"https://example.com/moved.xml": func() *fetch.Response {
				r := rssResp("Good", "https://example.com/")
				r.URL = mustURL(t, "https://example.com/good.xml")
				return r
			}(),
		},
		errs: map[string]error{"https://example.com/broken.xml": errors.New("connection reset")},
	}
	res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Direct {
		t.Error("Direct is set for an HTML page")
	}
	_, parseErr := feedparse.Parse(mustURL(t, "https://example.com/page.xml"), []byte(`<html><title>Not a feed</title></html>`))
	assertOutcomes(t, res.Checked,
		"https://example.com/good.xml page: feed",
		"https://example.com/missing.xml page: status 404",
		"https://example.com/page.xml page: "+parseErr.Error(),
		"https://example.com/broken.xml page: connection reset",
		"https://example.com/moved.xml page: duplicate",
	)
	good, moved, missing, broken := res.Checked[0], res.Checked[4], res.Checked[1], res.Checked[3]
	if good.Feed != "https://example.com/good.xml" || good.Status != 200 || good.FinalURL != "https://example.com/good.xml" {
		t.Errorf("good = %+v", good)
	}
	if moved.FinalURL != "https://example.com/good.xml" || moved.Feed != "https://example.com/good.xml" {
		t.Errorf("moved = %+v, want its final URL and the feed it duplicates", moved)
	}
	if missing.Status != 404 || missing.Feed != "" {
		t.Errorf("missing = %+v", missing)
	}
	if broken.Status != 0 || broken.FinalURL != "" {
		t.Errorf("broken = %+v, want no status or final URL", broken)
	}
}

func TestDiscoverCheckedCommonPaths(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/":        htmlResp(`<title>No links</title>`),
		"https://example.com/rss.xml": rssResp("Fallback", "https://example.com/"),
	}}
	res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Checked) != len(CommonFeedPaths) {
		t.Fatalf("Checked has %d entries, want %d", len(res.Checked), len(CommonFeedPaths))
	}
	for i, c := range res.Checked {
		if c.Source != SourceCommonPath {
			t.Errorf("Checked[%d].Source = %q, want %q", i, c.Source, SourceCommonPath)
		}
	}
	if c := res.Checked[3]; c.Candidate.URL.String() != "https://example.com/rss.xml" || c.Outcome != OutcomeFeed {
		t.Errorf("Checked[3] = %+v, want /rss.xml as a feed", c)
	}
}

func TestDiscoverCheckedLimits(t *testing.T) {
	var links strings.Builder
	responses := map[string]*fetch.Response{}
	for i := range 6 {
		fmt.Fprintf(&links, `<link rel="alternate" type="application/rss+xml" href="/f%d.xml">`, i)
		responses[fmt.Sprintf("https://example.com/f%d.xml", i)] = rssResp(fmt.Sprintf("F%d", i), "")
	}
	responses["https://example.com/"] = htmlResp(links.String())

	d := &Discoverer{Fetcher: &fakeFetcher{responses: responses}, MaxCandidates: 4, MaxFeeds: 2}
	res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcomes(t, res.Checked,
		"https://example.com/f0.xml page: feed",
		"https://example.com/f1.xml page: feed",
		"https://example.com/f2.xml page: over the feed limit",
		"https://example.com/f3.xml page: over the feed limit",
		"https://example.com/f4.xml page: not fetched (over the candidate limit)",
		"https://example.com/f5.xml page: not fetched (over the candidate limit)",
	)
}

func TestDiscoverCheckedDirect(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/feed.xml": rssResp("Direct", "https://example.com/"),
	}}
	res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://example.com/feed.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Direct || len(res.Checked) != 0 {
		t.Errorf("Direct = %v, Checked = %+v; want a direct feed and nothing checked", res.Direct, res.Checked)
	}
}

func TestDiscoverCheckedDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://example.com/": htmlResp(`<title>T</title>
			<link rel="alternate" type="application/rss+xml" href="/a.xml">
			<link rel="alternate" type="application/rss+xml" href="/b.xml">
			<link rel="alternate" type="application/rss+xml" href="/c.xml">`),
		"https://example.com/a.xml": rssResp("A", ""),
	}}
	f.onGet = func(rawURL string) {
		if rawURL == "https://example.com/b.xml" {
			cancel()
		}
	}
	res, err := (&Discoverer{Fetcher: f, Concurrency: 1}).Discover(ctx, mustURL(t, "https://example.com/"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Site.Title != "T" || len(res.Feeds) != 0 {
		t.Errorf("Site = %+v, Feeds = %+v; want the site and no feeds", res.Site, res.Feeds)
	}
	assertOutcomes(t, res.Checked,
		"https://example.com/a.xml page: feed",
		"https://example.com/b.xml page: "+context.Canceled.Error(),
		"https://example.com/c.xml page: not fetched (deadline)",
	)
}

func TestDiscoverAppLink(t *testing.T) {
	shared := htmlResp(`<title>Picks</title><link rel="alternate" type="application/atom+xml" href="/shared/abc/feed.atom">`)

	t.Run("address on the app", func(t *testing.T) {
		for _, addr := range []string{"https://paperround.app/shared/abc", "https://www.PaperRound.app/shared/abc", "https://paperround.app/@x"} {
			f := &fakeFetcher{responses: map[string]*fetch.Response{addr: shared}}
			d := &Discoverer{Fetcher: f, AppSiteKey: "paperround.app"}
			_, err := d.Discover(context.Background(), mustURL(t, addr))
			if !errors.Is(err, ErrAppLink) {
				t.Errorf("%s: err = %v, want ErrAppLink", addr, err)
			}
			if len(f.gets) != 0 {
				t.Errorf("%s: gets = %v, want none", addr, f.gets)
			}
		}
	})

	t.Run("redirect to the app", func(t *testing.T) {
		// One server answers as both hosts: "localhost" redirects to
		// "127.0.0.1", which stands in for the app.
		mux := http.NewServeMux()
		var srvURL *url.URL
		mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:"+srvURL.Port()+"/shared/abc", http.StatusFound)
		})
		mux.HandleFunc("/shared/abc", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<title>Picks</title><link rel="alternate" type="application/atom+xml" href="/shared/abc/feed.atom">`)
		})
		mux.HandleFunc("/shared/abc/feed.atom", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/atom+xml")
			fmt.Fprint(w, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Picks</title></feed>`)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		srvURL = mustURL(t, srv.URL)

		d := &Discoverer{Fetcher: fetch.New(fetch.Options{AllowPrivate: true}), AppSiteKey: "127.0.0.1"}
		res, err := d.Discover(context.Background(), mustURL(t, "http://localhost:"+srvURL.Port()+"/short"))
		if !errors.Is(err, ErrAppLink) {
			t.Errorf("err = %v (site %+v), want ErrAppLink", err, res.Site)
		}
	})

	t.Run("check off", func(t *testing.T) {
		addr := "https://paperround.app/shared/abc"
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			addr: shared,
			"https://paperround.app/shared/abc/feed.atom": rssResp("Picks", "https://paperround.app/shared/abc"),
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, addr))
		if err != nil {
			t.Fatal(err)
		}
		assertURLs(t, res.Feeds, "https://paperround.app/shared/abc/feed.atom")
	})

	t.Run("another site", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://example.com/":         htmlResp(`<link rel="alternate" type="application/rss+xml" href="/feed.xml">`),
			"https://example.com/feed.xml": rssResp("Example", "https://example.com/"),
		}}
		d := &Discoverer{Fetcher: f, AppSiteKey: "paperround.app"}
		res, err := d.Discover(context.Background(), mustURL(t, "https://example.com/"))
		if err != nil {
			t.Fatal(err)
		}
		assertURLs(t, res.Feeds, "https://example.com/feed.xml")
	})
}

func atomResp(title, link string) *fetch.Response {
	body := fmt.Sprintf(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
		<title>%s</title><link rel="alternate" href="%s"/>
		<entry><id>g1</id><title>First</title><link href="https://example.com/1"/></entry>
		</feed>`, title, link)
	return &fetch.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/atom+xml"}},
		Body:       []byte(body),
	}
}

// A site on a shared host is keyed by the path that names it, whether its
// page or its feed is looked up.
func TestDiscoverSharedHostSite(t *testing.T) {
	page := htmlResp(`<title>Alice – Medium</title><link rel="alternate" type="application/rss+xml" href="https://medium.com/feed/@alice">`)
	feed := rssResp("Stories by Alice on Medium", "https://medium.com/@alice?source=rss-abc------2")
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://medium.com/@alice":        page,
		"https://medium.com/@alice/a-post": page,
		"https://medium.com/feed/@alice":   feed,
	}}
	tests := []struct {
		url          string
		wantHomePage bool
	}{
		{"https://medium.com/@alice", true},
		{"https://medium.com/@alice/a-post", false},
		{"https://medium.com/feed/@alice", false}, // direct
	}
	for _, tt := range tests {
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, tt.url))
		if err != nil {
			t.Fatalf("%s: %v", tt.url, err)
		}
		if res.Site.Key != "medium.com/@alice" || res.Site.URL != "https://medium.com/@alice" {
			t.Errorf("%s: Site = %+v, want Key medium.com/@alice URL https://medium.com/@alice", tt.url, res.Site)
		}
		if res.HomePage != tt.wantHomePage {
			t.Errorf("%s: HomePage = %v, want %v", tt.url, res.HomePage, tt.wantHomePage)
		}
		assertURLs(t, res.Feeds, "https://medium.com/feed/@alice")
	}

	t.Run("feed without a link", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://medium.com/feed/@alice": rssResp("Alice", ""),
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://medium.com/feed/@alice"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Site.Key != "medium.com/@alice" || res.Site.URL != "https://medium.com/@alice" {
			t.Errorf("Site = %+v, want Key medium.com/@alice URL https://medium.com/@alice", res.Site)
		}
	})
}

// A page whose key has a path takes its first feed's site when that is
// another key with a path on the same host.
func TestDiscoverSharedHostFeedSite(t *testing.T) {
	const channel = "https://www.youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw"
	const feedURL = "https://www.youtube.com/feeds/videos.xml?channel_id=UC_x5XG1OV2P6uZZ5FSM9Ttw"
	page := htmlResp(`<title>Google for Developers - YouTube</title><link rel="alternate" type="application/rss+xml" href="` + feedURL + `">`)
	tests := []struct {
		name     string
		pageURL  string
		feedLink string
		wantKey  string
		wantURL  string
	}{
		{"handle to channel", "https://www.youtube.com/@GoogleDevelopers", channel,
			"youtube.com/channel/UC_x5XG1OV2P6uZZ5FSM9Ttw", channel},
		{"feed on the host's own site", "https://www.youtube.com/@GoogleDevelopers", "https://www.youtube.com/",
			"youtube.com/@googledevelopers", "https://www.youtube.com/@GoogleDevelopers"},
		{"feed on another host", "https://www.youtube.com/@GoogleDevelopers", "https://developers.google.com/@x",
			"youtube.com/@googledevelopers", "https://www.youtube.com/@GoogleDevelopers"},
		{"page on the host's own site", "https://www.youtube.com/", channel,
			"youtube.com", "https://www.youtube.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeFetcher{responses: map[string]*fetch.Response{
				tt.pageURL: page,
				feedURL:    atomResp("Google for Developers", tt.feedLink),
			}}
			res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, tt.pageURL))
			if err != nil {
				t.Fatal(err)
			}
			if res.Site.Key != tt.wantKey || res.Site.URL != tt.wantURL {
				t.Errorf("Site = %+v, want Key %q URL %q", res.Site, tt.wantKey, tt.wantURL)
			}
			// A key with a path names the site as it names itself (OwnName);
			// the host's own site keeps the page's title.
			wantTitle := "Google for Developers"
			if tt.wantKey == "youtube.com" {
				wantTitle = "Google for Developers - YouTube"
			}
			if res.Site.Title != wantTitle {
				t.Errorf("Site.Title = %q, want %q", res.Site.Title, wantTitle)
			}
			assertURLs(t, res.Feeds, feedURL)
		})
	}
}

// A site on a shared host doesn't try the common feed paths, which would
// find the platform's own feed.
func TestDiscoverSharedHostNoCommonPaths(t *testing.T) {
	f := &fakeFetcher{responses: map[string]*fetch.Response{
		"https://medium.com/@alice": htmlResp(`<title>Alice</title>`),
		"https://medium.com/feed":   rssResp("Medium", "https://medium.com/"),
	}}
	res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://medium.com/@alice"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Feeds) != 0 {
		t.Errorf("Feeds = %v, want none", feedURLs(res.Feeds))
	}
	for _, g := range f.gets {
		for _, p := range CommonFeedPaths {
			if strings.HasSuffix(g, p) {
				t.Errorf("fetched common path %s", g)
			}
		}
	}
	if res.Site.Key != "medium.com/@alice" {
		t.Errorf("Site.Key = %q, want medium.com/@alice", res.Site.Key)
	}
}

// checkedSummary is each check's "source candidate outcome".
func checkedSummary(checked []CandidateCheck) []string {
	var s []string
	for _, c := range checked {
		s = append(s, c.Source+" "+c.Candidate.URL.String()+" "+c.Outcome)
	}
	return s
}

func TestDiscoverPlatformFeeds(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		responses map[string]*fetch.Response
		checked   []string // checkedSummary
		feeds     []string
		key       string
	}{
		{
			name:  "Medium author",
			input: "https://medium.com/@alice",
			responses: map[string]*fetch.Response{
				"https://medium.com/@alice":      htmlResp(`<title>Alice – Medium</title>`),
				"https://medium.com/feed/@alice": rssResp("Stories by Alice on Medium", "https://medium.com/@alice?source=rss"),
			},
			checked: []string{"platform https://medium.com/feed/@alice feed"},
			feeds:   []string{"https://medium.com/feed/@alice"},
			key:     "medium.com/@alice",
		},
		{
			name:  "GitHub repository",
			input: "https://github.com/owner/repo",
			responses: map[string]*fetch.Response{
				"https://github.com/owner/repo":               htmlResp(`<title>GitHub - owner/repo: A tool</title>`),
				"https://github.com/owner/repo/releases.atom": atomResp("Release notes from repo", "https://github.com/owner/repo/releases"),
				// tags.atom is not in responses: 404.
				"https://github.com/owner/repo/commits.atom": atomResp("Recent Commits to repo:main", "https://github.com/owner/repo/commits/main"),
			},
			checked: []string{
				"platform https://github.com/owner/repo/releases.atom feed",
				"platform https://github.com/owner/repo/tags.atom status 404",
				"platform https://github.com/owner/repo/commits.atom feed",
			},
			feeds: []string{"https://github.com/owner/repo/releases.atom", "https://github.com/owner/repo/commits.atom"},
			key:   "github.com/owner/repo",
		},
		{
			name:  "Mastodon account",
			input: "https://mastodon.social/@bob",
			responses: map[string]*fetch.Response{
				"https://mastodon.social/@bob":     htmlResp(`<title>Bob (@bob@mastodon.social)</title>`),
				"https://mastodon.social/@bob.rss": rssResp("Bob", "https://mastodon.social/@bob"),
			},
			checked: []string{"platform https://mastodon.social/@bob.rss feed"},
			feeds:   []string{"https://mastodon.social/@bob.rss"},
			key:     "mastodon.social/@bob",
		},
		{
			name:  "a page that lists a feed",
			input: "https://medium.com/@alice",
			responses: map[string]*fetch.Response{
				"https://medium.com/@alice":      htmlResp(`<title>Alice</title><link rel="alternate" type="application/rss+xml" href="/listed.xml">`),
				"https://medium.com/listed.xml":  rssResp("Listed", "https://medium.com/@alice"),
				"https://medium.com/feed/@alice": rssResp("Stories by Alice on Medium", "https://medium.com/@alice"),
			},
			checked: []string{"page https://medium.com/listed.xml feed"},
			feeds:   []string{"https://medium.com/listed.xml"},
			key:     "medium.com/@alice",
		},
		{
			name:  "a bare host",
			input: "https://example.com/",
			responses: map[string]*fetch.Response{
				"https://example.com/":        htmlResp(`<title>Example</title>`),
				"https://example.com/rss.xml": rssResp("Example", "https://example.com/"),
			},
			feeds: []string{"https://example.com/rss.xml"},
			key:   "example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeFetcher{responses: tt.responses}
			res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, tt.input))
			if err != nil {
				t.Fatal(err)
			}
			assertURLs(t, res.Feeds, tt.feeds...)
			if res.Site.Key != tt.key {
				t.Errorf("Site.Key = %q, want %q", res.Site.Key, tt.key)
			}
			if !res.HomePage {
				t.Error("HomePage = false, want true: the page was read")
			}
			if tt.checked == nil {
				// The bare host tries the common paths, as before.
				for _, c := range res.Checked {
					if c.Source != SourceCommonPath {
						t.Errorf("Checked has %s from %q, want only common paths", c.Candidate.URL, c.Source)
					}
				}
				if len(res.Checked) != len(CommonFeedPaths) {
					t.Errorf("Checked %d candidates, want %d", len(res.Checked), len(CommonFeedPaths))
				}
				return
			}
			if got := checkedSummary(res.Checked); !slices.Equal(got, tt.checked) {
				t.Errorf("Checked = %q, want %q", got, tt.checked)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if want := 1 + len(tt.checked); len(f.gets) != want {
				t.Errorf("gets = %v, want %d", f.gets, want)
			}
		})
	}
}

func TestDiscoverPlatformPageRefused(t *testing.T) {
	forbidden := &fetch.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"text/html"}}, Body: []byte("<title>Just a moment</title>")}

	t.Run("its feed answers", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://medium.com/@alice":      forbidden,
			"https://medium.com/feed/@alice": rssResp("Stories by Alice on Medium", "https://medium.com/@alice?source=rss-abc"),
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://medium.com/@alice"))
		if err != nil {
			t.Fatal(err)
		}
		assertURLs(t, res.Feeds, "https://medium.com/feed/@alice")
		want := SiteInfo{Key: "medium.com/@alice", URL: "https://medium.com/@alice", Title: "Alice", Description: "About Stories by Alice on Medium"}
		if res.Site != want {
			t.Errorf("Site = %+v, want %+v", res.Site, want)
		}
		if res.HomePage || res.Direct {
			t.Errorf("HomePage = %v, Direct = %v, want both false", res.HomePage, res.Direct)
		}
		if got, want := checkedSummary(res.Checked), []string{"platform https://medium.com/feed/@alice feed"}; !slices.Equal(got, want) {
			t.Errorf("Checked = %q, want %q", got, want)
		}
	})

	t.Run("a feed's title keeps its dash", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://www.youtube.com/channel/UCabc":                     forbidden,
			"https://www.youtube.com/feeds/videos.xml?channel_id=UCabc": atomResp("Kurzgesagt – In a Nutshell", "https://www.youtube.com/channel/UCabc"),
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://www.youtube.com/channel/UCabc"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Site.Key != "youtube.com/channel/UCabc" || res.Site.Title != "Kurzgesagt – In a Nutshell" {
			t.Errorf("Site = %+v, want youtube.com/channel/UCabc named Kurzgesagt – In a Nutshell", res.Site)
		}
	})

	t.Run("its feed doesn't answer", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://medium.com/@alice": forbidden,
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://medium.com/@alice"))
		if !errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
		if !strings.Contains(err.Error(), "status 403") {
			t.Errorf("err = %v, want the page's status", err)
		}
		if got, want := checkedSummary(res.Checked), []string{"platform https://medium.com/feed/@alice status 404"}; !slices.Equal(got, want) {
			t.Errorf("Checked = %q, want %q", got, want)
		}
	})

	t.Run("a bare host", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://example.com/": forbidden,
		}}
		res, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://example.com/"))
		if !errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
		if len(res.Checked) != 0 || !slices.Equal(f.gets, []string{"https://example.com/"}) {
			t.Errorf("Checked = %v, gets = %v, want only the page", res.Checked, f.gets)
		}
	})

	t.Run("a network error", func(t *testing.T) {
		f := &fakeFetcher{
			errs: map[string]error{"https://medium.com/@alice": errors.New("connection refused")},
			responses: map[string]*fetch.Response{
				"https://medium.com/feed/@alice": rssResp("Stories by Alice on Medium", "https://medium.com/@alice"),
			},
		}
		_, err := (&Discoverer{Fetcher: f}).Discover(context.Background(), mustURL(t, "https://medium.com/@alice"))
		if err == nil || errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want the fetch error", err)
		}
		if !slices.Equal(f.gets, []string{"https://medium.com/@alice"}) {
			t.Errorf("gets = %v, want only the page", f.gets)
		}
	})

	t.Run("its feed names the app", func(t *testing.T) {
		f := &fakeFetcher{responses: map[string]*fetch.Response{
			"https://medium.com/@alice":      forbidden,
			"https://medium.com/feed/@alice": rssResp("Shared", "https://paperround.app/shared/x"),
		}}
		d := &Discoverer{Fetcher: f, AppSiteKey: "paperround.app"}
		if _, err := d.Discover(context.Background(), mustURL(t, "https://medium.com/@alice")); !errors.Is(err, ErrAppLink) {
			t.Errorf("err = %v, want ErrAppLink", err)
		}
	})
}
