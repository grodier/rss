package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBlocked(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"192.0.0.1", true},
		{"198.18.0.1", true},
		{"240.0.0.1", true},
		{"255.255.255.255", true},
		{"224.0.0.1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"fe80::1%eth0", true},
		{"ff02::1", true},
		{"::", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"64:ff9b::7f00:1", true},
		{"2001:db8::1", true},

		{"93.184.216.34", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := blocked(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Errorf("blocked(%s) = %v; want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestBlocksLoopbackServer(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	c := New(Options{})
	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		_, err := c.Get(context.Background(), u)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("Get(%s) error = %v; want ErrBlockedAddress", u, err)
		}
	}
	if hits != 0 {
		t.Errorf("server received %d requests; want 0", hits)
	}
}

func newTestClient(opts Options) *Client {
	opts.AllowPrivate = true
	return New(opts)
}

func TestGetOK(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/feed.xml", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, "<rss></rss>")
	}))
	defer srv.Close()

	resp, err := newTestClient(Options{}).Get(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d; want 200", resp.StatusCode)
	}
	if string(resp.Body) != "<rss></rss>" {
		t.Errorf("Body = %q", resp.Body)
	}
	if want := srv.URL + "/feed.xml"; resp.URL.String() != want {
		t.Errorf("URL = %s; want %s", resp.URL, want)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/rss+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	if gotUA != defaultUserAgent {
		t.Errorf("User-Agent = %q; want %q", gotUA, defaultUserAgent)
	}
	if gotAccept != acceptHeader {
		t.Errorf("Accept = %q; want %q", gotAccept, acceptHeader)
	}
}

func TestCustomUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	if _, err := newTestClient(Options{UserAgent: "test-agent"}).Get(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if gotUA != "test-agent" {
		t.Errorf("User-Agent = %q; want test-agent", gotUA)
	}
}

func TestNotFoundIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	resp, err := newTestClient(Options{}).Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d; want 404", resp.StatusCode)
	}
}

// redirectServer redirects /n to /n+1 until /stop, which returns 200.
func redirectServer(t *testing.T, stop int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if n >= stop {
			return
		}
		http.Redirect(w, r, "/"+strconv.Itoa(n+1), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRedirectLimit(t *testing.T) {
	c := newTestClient(Options{MaxRedirects: 3})

	srv := redirectServer(t, 3)
	if _, err := c.Get(context.Background(), srv.URL+"/0"); err != nil {
		t.Errorf("3 redirects: error = %v; want nil", err)
	}

	srv = redirectServer(t, 4)
	_, err := c.Get(context.Background(), srv.URL+"/0")
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("4 redirects: error = %v; want ErrTooManyRedirects", err)
	}
}

func TestRedirectToUnsupportedScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/feed.xml", http.StatusFound)
	}))
	defer srv.Close()

	_, err := newTestClient(Options{}).Get(context.Background(), srv.URL)
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Errorf("error = %v; want ErrUnsupportedScheme", err)
	}
}

func TestBodyLimit(t *testing.T) {
	const max = 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		w.Write(bytes.Repeat([]byte("a"), n))
	}))
	defer srv.Close()

	c := newTestClient(Options{MaxBodyBytes: max})

	resp, err := c.Get(context.Background(), srv.URL+"?n="+strconv.Itoa(max))
	if err != nil {
		t.Fatalf("exactly MaxBodyBytes: error = %v", err)
	}
	if len(resp.Body) != max {
		t.Errorf("len(Body) = %d; want %d", len(resp.Body), max)
	}

	_, err = c.Get(context.Background(), srv.URL+"?n="+strconv.Itoa(max+1))
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("MaxBodyBytes+1: error = %v; want ErrTooLarge", err)
	}
}

func slowServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTimeout(t *testing.T) {
	srv := slowServer(t)

	start := time.Now()
	_, err := newTestClient(Options{Timeout: 100 * time.Millisecond}).Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("error = nil; want timeout")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Get took %v; want about 100ms", d)
	}
}

func TestContextCancel(t *testing.T) {
	srv := slowServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := newTestClient(Options{}).Get(ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v; want context.DeadlineExceeded", err)
	}
}

// These are rejected before any network access: the client allows private
// addresses, so a dial would succeed if one happened.
func TestRejectedURLs(t *testing.T) {
	tests := []struct {
		url  string
		want error
	}{
		{"file:///etc/passwd", ErrUnsupportedScheme},
		{"ftp://example.com/", ErrUnsupportedScheme},
		{"javascript:alert(1)", ErrUnsupportedScheme},
		{"example.com/feed", ErrUnsupportedScheme},
		{"http://user:pw@example.com/", errCredentials},
		{"http://user@example.com/", errCredentials},
	}
	c := newTestClient(Options{})
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			_, err := c.Get(context.Background(), tt.url)
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v; want %v", err, tt.want)
			}
		})
	}
}
