// Package fetch is the only way the app makes outbound HTTP requests to
// user-influenced URLs. It blocks private and internal addresses, limits
// redirects, response size and time.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

var (
	ErrBlockedAddress    = errors.New("fetch: address not allowed")
	ErrUnsupportedScheme = errors.New("fetch: only http and https are allowed")
	ErrTooLarge          = errors.New("fetch: response too large")
	ErrTooManyRedirects  = errors.New("fetch: too many redirects")
)

var errCredentials = errors.New("fetch: URLs with credentials are not allowed")

const (
	defaultTimeout      = 10 * time.Second
	defaultMaxBodyBytes = 5 << 20
	defaultMaxRedirects = 5
	defaultUserAgent    = "rss (+https://github.com/grodier/rss)"

	acceptHeader = "application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, text/html;q=0.8, */*;q=0.5"
)

// Options configures a Client. Zero values use the defaults.
type Options struct {
	Timeout      time.Duration // whole request incl. body; default 10s
	MaxBodyBytes int64         // default 5 << 20 (5 MiB)
	MaxRedirects int           // default 5
	UserAgent    string        // default "rss (+https://github.com/grodier/rss)"
	// AllowPrivate disables the address check. Tests only (httptest servers
	// listen on 127.0.0.1). Never set it in cmd/www.
	AllowPrivate bool
}

// Client fetches user-supplied URLs safely. It is safe for concurrent use.
type Client struct {
	http *http.Client
	opts Options
}

// New returns a Client configured by opts.
func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = defaultMaxRedirects
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !opts.AllowPrivate {
		dialer.Control = checkAddress
	}

	transport := &http.Transport{
		// No environment proxy: the dialer would check the proxy's address
		// instead of the target's.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}

	c := &Client{opts: opts}
	c.http = &http.Client{
		Transport:     transport,
		Timeout:       opts.Timeout,
		CheckRedirect: c.checkRedirect,
	}
	return c
}

// Response is a fetched response with its body fully read.
type Response struct {
	URL        *url.URL // final URL after redirects
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Get fetches rawURL. Non-2xx statuses are returned as a Response, not an
// error; callers decide what to do with them.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if err := checkURL(u); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", acceptHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.opts.MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch: reading body: %w", err)
	}
	if int64(len(body)) > c.opts.MaxBodyBytes {
		return nil, ErrTooLarge
	}

	return &Response{
		URL:        resp.Request.URL,
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
	}, nil
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	// via holds the requests already made, so this allows exactly
	// MaxRedirects redirects.
	if len(via) > c.opts.MaxRedirects {
		return ErrTooManyRedirects
	}
	return checkURL(req.URL)
}

// checkURL rejects URLs that aren't plain http(s) or that carry credentials.
func checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrUnsupportedScheme
	}
	if u.User != nil {
		return errCredentials
	}
	if u.Host == "" {
		return fmt.Errorf("fetch: URL has no host")
	}
	return nil
}

// checkAddress is a net.Dialer Control func. It runs after DNS resolution,
// for every connection attempt (including redirects), so it checks the IP
// actually being dialled. That defeats DNS rebinding.
func checkAddress(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
	}
	if blocked(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ap.Addr())
	}
	return nil
}

// blockedPrefixes are special-purpose ranges not covered by netip's Is*
// methods.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64
	netip.MustParsePrefix("2001:db8::/32"),
}

// blocked reports whether addr is private, internal or otherwise not a
// public unicast address.
func blocked(addr netip.Addr) bool {
	// Unmap so ::ffff:127.0.0.1 is checked as 127.0.0.1. Drop the zone:
	// a zoned address never matches a prefix.
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() ||
		addr.IsUnspecified() ||
		addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
