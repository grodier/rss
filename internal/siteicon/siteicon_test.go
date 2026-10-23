package siteicon

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/fetch"
	"github.com/grodier/rss/internal/rss"
)

// call is one Store write.
type call struct {
	method  string // SaveIcon, RemoveIcon or KeepIcon
	siteID  string
	icon    rss.SiteIcon
	checked bool
	next    time.Time
}

type fakeStore struct {
	mu    sync.Mutex
	calls []call
	claim func(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error)
}

func (s *fakeStore) ClaimIconCheck(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error) {
	return s.claim(ctx, lease)
}

func (s *fakeStore) record(c call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	return nil
}

func (s *fakeStore) SaveIcon(ctx context.Context, icon rss.SiteIcon, next time.Time) error {
	return s.record(call{method: "SaveIcon", siteID: icon.SiteID, icon: icon, next: next})
}

func (s *fakeStore) RemoveIcon(ctx context.Context, siteID string, next time.Time) error {
	return s.record(call{method: "RemoveIcon", siteID: siteID, next: next})
}

func (s *fakeStore) KeepIcon(ctx context.Context, siteID string, checked bool, next time.Time) error {
	return s.record(call{method: "KeepIcon", siteID: siteID, checked: checked, next: next})
}

// site is an httptest server standing in for a site. Its handlers are set
// per test; it records the paths requested and their headers.
type site struct {
	*httptest.Server
	mux *http.ServeMux

	mu       sync.Mutex
	requests map[string]http.Header
}

// newSite returns a site whose home page is the HTML home.
func newSite(t *testing.T, home string) *site {
	t.Helper()
	s := newBareSite(t)
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(home))
	})
	return s
}

// newBareSite returns a site with no handlers.
func newBareSite(t *testing.T) *site {
	t.Helper()
	s := &site{mux: http.NewServeMux(), requests: map[string]http.Header{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.URL.Path] = r.Header.Clone()
		s.mu.Unlock()
		s.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// serve serves body at path with contentType.
func (s *site) serve(path, contentType string, body []byte) {
	s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	})
}

// status answers path with status.
func (s *site) status(path string, status int) {
	s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	})
}

func (s *site) requested(path string) (http.Header, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.requests[path]
	return h, ok
}

func newRunner(store *fakeStore) *Runner {
	r := &Runner{
		Store:       store,
		Fetcher:     fetch.New(fetch.Options{AllowPrivate: true}),
		IconFetcher: fetch.New(fetch.Options{AllowPrivate: true, MaxBodyBytes: MaxBytes}),
	}
	r.setDefaults()
	return r
}

// check runs one check of the site at srv with current as its icon and
// returns the one Store write it made.
func check(t *testing.T, srv *site, current rss.SiteIcon) call {
	t.Helper()
	store := &fakeStore{}
	newRunner(store).check(t.Context(), rss.Site{ID: "site-1", Host: "example.com", URL: srv.URL + "/"}, current)
	if len(store.calls) != 1 {
		t.Fatalf("Store calls = %+v; want exactly one", store.calls)
	}
	c := store.calls[0]
	if c.siteID != "site-1" {
		t.Errorf("site ID = %q; want site-1", c.siteID)
	}
	return c
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildICO returns an ICO file with one PNG image per size (square).
// A size of 256 is written as 0, as the format requires.
func buildICO(t *testing.T, sizes ...int) []byte {
	t.Helper()
	var images [][]byte
	for _, s := range sizes {
		images = append(images, encodePNG(t, s, s))
	}
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 1, 0})
	binary.Write(&buf, binary.LittleEndian, uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		b := byte(s)
		if s >= 256 {
			b = 0
		}
		buf.Write([]byte{b, b, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(images[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(images[i])
	}
	for _, img := range images {
		buf.Write(img)
	}
	return buf.Bytes()
}

func wantSaved(t *testing.T, c call, srv *site, path, contentType string, w, h int) {
	t.Helper()
	if c.method != "SaveIcon" {
		t.Fatalf("got %s; want SaveIcon", c.method)
	}
	if got := c.icon; got.SourceURL != srv.URL+path || got.ContentType != contentType || got.Width != w || got.Height != h {
		t.Errorf("saved %s %s %dx%d; want %s %s %dx%d", got.SourceURL, got.ContentType, got.Width, got.Height, srv.URL+path, contentType, w, h)
	}
	if len(c.icon.Data) == 0 || len(c.icon.Hash) != 64 {
		t.Errorf("saved %d bytes, hash %q; want data and a hex SHA-256", len(c.icon.Data), c.icon.Hash)
	}
}

func wantKept(t *testing.T, c call, checked bool) {
	t.Helper()
	if c.method != "KeepIcon" || c.checked != checked {
		t.Fatalf("got %s (checked %v); want KeepIcon (checked %v)", c.method, c.checked, checked)
	}
}

func TestCheckAppleTouchIconSaved(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" sizes="180x180" href="/touch.png"><link rel="icon" href="/favicon.ico">`)
	srv.serve("/touch.png", "image/png", encodePNG(t, 180, 180))

	c := check(t, srv, rss.SiteIcon{})
	wantSaved(t, c, srv, "/touch.png", "image/png", 180, 180)
	if _, ok := srv.requested("/favicon.ico"); ok {
		t.Error("/favicon.ico was downloaded after a usable icon")
	}
}

func TestCheckLargestAppleTouchIconFirst(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" href="/unknown.png">
		<link rel="apple-touch-icon" sizes="120x120" href="/120.png">
		<link rel="apple-touch-icon" sizes="180x180" href="/180.png">`)
	srv.serve("/180.png", "image/png", encodePNG(t, 180, 180))
	srv.serve("/120.png", "image/png", encodePNG(t, 120, 120))
	srv.serve("/unknown.png", "image/png", encodePNG(t, 64, 64))

	wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/180.png", "image/png", 180, 180)
}

func TestCheckJPEGAndGIFSaved(t *testing.T) {
	for _, tt := range []struct {
		name, contentType string
		size              int
		body              []byte
	}{
		{"jpeg", "image/jpeg", 64, encodeJPEG(t, 64, 64)},
		{"gif", "image/gif", 48, encodeGIF(t, 48, 48)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSite(t, `<link rel="icon" href="/icon">`)
			// The Content-Type header is wrong on purpose: the bytes decide.
			srv.serve("/icon", "application/octet-stream", tt.body)
			wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/icon", tt.contentType, tt.size, tt.size)
		})
	}
}

func TestCheckSmallFaviconRemoved(t *testing.T) {
	srv := newSite(t, `<title>No icons</title>`)
	srv.serve("/favicon.ico", "image/x-icon", buildICO(t, 16))

	if c := check(t, srv, rss.SiteIcon{SourceURL: srv.URL + "/old.png"}); c.method != "RemoveIcon" {
		t.Errorf("got %s; want RemoveIcon", c.method)
	}
}

func TestCheckICOLargestEntrySaved(t *testing.T) {
	srv := newSite(t, ``)
	srv.serve("/favicon.ico", "image/vnd.microsoft.icon", buildICO(t, 16, 64))

	wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/favicon.ico", "image/x-icon", 64, 64)
}

func TestCheckSmallDeclaredIconNotDownloaded(t *testing.T) {
	srv := newSite(t, `<link rel="icon" sizes="16x16" href="/16.png">`)
	srv.serve("/16.png", "image/png", encodePNG(t, 64, 64))
	srv.serve("/favicon.ico", "image/x-icon", buildICO(t, 32))

	wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/favicon.ico", "image/x-icon", 32, 32)
	if _, ok := srv.requested("/16.png"); ok {
		t.Error("an icon declared 16x16 was downloaded")
	}
}

func TestCheckSVGSkipped(t *testing.T) {
	srv := newSite(t, `<link rel="icon" type="image/svg+xml" href="/icon"><link rel="icon" href="/logo.SVG"><link rel="icon" href="/icon.png">`)
	srv.serve("/icon", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	srv.serve("/logo.SVG", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	srv.serve("/icon.png", "image/png", encodePNG(t, 96, 96))

	wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/icon.png", "image/png", 96, 96)
	for _, p := range []string{"/icon", "/logo.SVG"} {
		if _, ok := srv.requested(p); ok {
			t.Errorf("SVG icon %s was downloaded", p)
		}
	}
}

func TestCheckRejected(t *testing.T) {
	big := append(encodePNG(t, 64, 64), make([]byte, MaxBytes)...)
	for _, tt := range []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"too wide", "image/png", encodePNG(t, 600, 100)},
		{"too tall", "image/png", encodePNG(t, 40, 100)},
		{"over 256 KiB", "image/png", big},
		{"HTML", "image/x-icon", []byte("<!doctype html><html><body>Not found</body></html>")},
		{"truncated ICO", "image/x-icon", buildICO(t, 64)[:10]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSite(t, ``)
			srv.serve("/favicon.ico", tt.contentType, tt.body)
			if c := check(t, srv, rss.SiteIcon{}); c.method != "RemoveIcon" {
				t.Errorf("got %s; want RemoveIcon", c.method)
			}
		})
	}

	t.Run("the next candidate is used", func(t *testing.T) {
		srv := newSite(t, `<link rel="apple-touch-icon" href="/wide.png">`)
		srv.serve("/wide.png", "image/png", encodePNG(t, 600, 100))
		srv.serve("/apple-touch-icon.png", "image/png", big)
		srv.serve("/favicon.ico", "image/x-icon", buildICO(t, 48))
		wantSaved(t, check(t, srv, rss.SiteIcon{}), srv, "/favicon.ico", "image/x-icon", 48, 48)
	})
}

func TestCheckAtMostFourDownloads(t *testing.T) {
	srv := newSite(t, `<link rel="icon" href="/1.png"><link rel="icon" href="/2.png"><link rel="icon" href="/3.png"><link rel="icon" href="/4.png">`)
	if c := check(t, srv, rss.SiteIcon{}); c.method != "RemoveIcon" {
		t.Errorf("got %s; want RemoveIcon", c.method)
	}
	for _, p := range []string{"/1.png", "/4.png"} {
		if _, ok := srv.requested(p); !ok {
			t.Errorf("%s wasn't downloaded", p)
		}
	}
	for _, p := range []string{"/apple-touch-icon.png", "/favicon.ico"} {
		if _, ok := srv.requested(p); ok {
			t.Errorf("%s was downloaded after four candidates", p)
		}
	}
}

func TestCheckCurrentIconNotModified(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" href="/touch.png">`)
	srv.mux.HandleFunc("GET /touch.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(encodePNG(t, 180, 180))
	})

	current := rss.SiteIcon{SiteID: "site-1", SourceURL: srv.URL + "/touch.png", ETag: `"v1"`, LastModified: "Mon, 02 Jan 2006 15:04:05 GMT"}
	c := check(t, srv, current)
	wantKept(t, c, true)
	wantNextMonth(t, c.next)

	h, _ := srv.requested("/touch.png")
	if h.Get("If-None-Match") != `"v1"` || h.Get("If-Modified-Since") != current.LastModified {
		t.Errorf("validators sent: If-None-Match %q, If-Modified-Since %q; want %q, %q",
			h.Get("If-None-Match"), h.Get("If-Modified-Since"), `"v1"`, current.LastModified)
	}
}

func TestCheckNewIconSavedWithValidators(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" href="/touch.png">`)
	srv.mux.HandleFunc("GET /touch.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("a new icon URL was sent If-None-Match %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Last-Modified", "Tue, 03 Jan 2006 15:04:05 GMT")
		w.Write(encodePNG(t, 180, 180))
	})

	c := check(t, srv, rss.SiteIcon{SiteID: "site-1", SourceURL: srv.URL + "/old.png", ETag: `"v1"`})
	wantSaved(t, c, srv, "/touch.png", "image/png", 180, 180)
	if c.icon.ETag != `"v2"` || c.icon.LastModified != "Tue, 03 Jan 2006 15:04:05 GMT" {
		t.Errorf("saved ETag %q, Last-Modified %q; want the response's", c.icon.ETag, c.icon.LastModified)
	}
	wantNextMonth(t, c.next)
}

func TestCheckHomePageFails(t *testing.T) {
	srv := newBareSite(t)
	srv.status("/{$}", http.StatusInternalServerError)

	before := time.Now()
	c := check(t, srv, rss.SiteIcon{})
	wantKept(t, c, false)
	if lo, hi := before.Add(RetryDelay), time.Now().Add(RetryDelay); c.next.Before(lo) || c.next.After(hi) {
		t.Errorf("next check %v; want a day out, within [%v, %v]", c.next, lo, hi)
	}
}

func TestCheckHomePageUnreachable(t *testing.T) {
	srv := newSite(t, ``)
	srv.Close()
	wantKept(t, check(t, srv, rss.SiteIcon{}), false)
}

func TestCheckNonHTMLHomePageDeclaresNoIcons(t *testing.T) {
	srv := newSite(t, ``)
	srv.mux.HandleFunc("GET /feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<rss><link rel="icon" href="/declared.png"/></rss>`))
	})
	srv.serve("/declared.png", "image/png", encodePNG(t, 64, 64))

	store := &fakeStore{}
	newRunner(store).check(t.Context(), rss.Site{ID: "site-1", URL: srv.URL + "/feed"}, rss.SiteIcon{})
	if len(store.calls) != 1 || store.calls[0].method != "RemoveIcon" {
		t.Errorf("Store calls = %+v; want one RemoveIcon", store.calls)
	}
	if _, ok := srv.requested("/declared.png"); ok {
		t.Error("an icon in a non-HTML body was downloaded")
	}
}

func TestCheckCandidateUnavailableKeepsIcon(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := newSite(t, `<link rel="apple-touch-icon" href="/touch.png">`)
			srv.status("/touch.png", status)
			srv.status("/apple-touch-icon.png", http.StatusNotFound)
			srv.status("/favicon.ico", http.StatusNotFound)
			wantKept(t, check(t, srv, rss.SiteIcon{}), false)
		})
	}
}

func TestCheckAllNotFoundRemoved(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" href="/touch.png"><link rel="icon" href="/icon.png">`)
	for _, p := range []string{"/touch.png", "/icon.png", "/apple-touch-icon.png", "/favicon.ico"} {
		srv.status(p, http.StatusNotFound)
	}

	c := check(t, srv, rss.SiteIcon{SourceURL: srv.URL + "/touch.png", ETag: `"v1"`})
	if c.method != "RemoveIcon" {
		t.Fatalf("got %s; want RemoveIcon", c.method)
	}
	wantNextMonth(t, c.next)
}

func TestCheckCanceledContextRecordsNothing(t *testing.T) {
	srv := newSite(t, `<link rel="apple-touch-icon" href="/touch.png">`)
	srv.serve("/touch.png", "image/png", encodePNG(t, 180, 180))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store := &fakeStore{}
	newRunner(store).check(ctx, rss.Site{ID: "site-1", URL: srv.URL + "/"}, rss.SiteIcon{})
	if len(store.calls) != 0 {
		t.Errorf("Store calls = %+v; want none", store.calls)
	}
}

func wantNextMonth(t *testing.T, next time.Time) {
	t.Helper()
	if d := time.Until(next); d < 27*24*time.Hour || d > 33*24*time.Hour {
		t.Errorf("next check in %v; want 27–33 days", d)
	}
}

func TestNextCheck(t *testing.T) {
	now := time.Now()
	for range 1000 {
		if d := nextCheck(now).Sub(now); d < 27*24*time.Hour || d > 33*24*time.Hour {
			t.Fatalf("nextCheck in %v; want 27–33 days", d)
		}
	}
}

func TestRunChecksClaimedSites(t *testing.T) {
	srv := newSite(t, ``)
	srv.serve("/favicon.ico", "image/x-icon", buildICO(t, 64))

	var mu sync.Mutex
	sites := []rss.Site{{ID: "a", URL: srv.URL + "/"}, {ID: "b", URL: srv.URL + "/"}}
	store := &fakeStore{claim: func(ctx context.Context, lease time.Duration) (rss.Site, rss.SiteIcon, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(sites) == 0 {
			return rss.Site{}, rss.SiteIcon{}, rss.ErrNoRecord
		}
		s := sites[0]
		sites = sites[1:]
		return s, rss.SiteIcon{}, nil
	}}
	r := newRunner(store)
	r.PollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		n := len(store.calls)
		store.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := map[string]string{}
	for _, c := range store.calls {
		got[c.siteID] = c.method
	}
	if len(store.calls) != 2 || got["a"] != "SaveIcon" || got["b"] != "SaveIcon" {
		t.Errorf("Store calls = %+v; want SaveIcon for a and b", store.calls)
	}
}

func TestRunLeaseMustExceedJobTimeout(t *testing.T) {
	r := &Runner{Store: &fakeStore{}, JobTimeout: time.Minute, Lease: time.Minute}
	if err := r.Run(t.Context()); err == nil {
		t.Error("Run() error = nil; want an error")
	}
}
