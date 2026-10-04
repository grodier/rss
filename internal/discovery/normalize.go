// Package discovery finds the feeds a website publishes.
package discovery

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
)

// maxInputLen is the longest search input ParseInput accepts, in bytes.
const maxInputLen = 2048

// ErrNotURL is returned by ParseInput when the input is not a website address.
var ErrNotURL = errors.New("discovery: not a website address")

// LooksLikeURL reports whether q should be offered as a website lookup.
// It is ParseInput(q) == nil.
func LooksLikeURL(q string) bool {
	_, err := ParseInput(q)
	return err == nil
}

// ParseInput turns user input into an absolute http(s) URL to fetch. Input
// without a scheme is treated as https. It returns ErrNotURL if q is not a
// website address: anything other than http(s), userinfo, IP literals and
// hosts that aren't a dotted domain name are rejected.
func ParseInput(q string) (*url.URL, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > maxInputLen || strings.IndexFunc(q, unicode.IsSpace) >= 0 {
		return nil, ErrNotURL
	}

	if scheme, _, ok := strings.Cut(q, "://"); ok {
		scheme = strings.ToLower(scheme)
		if scheme != "http" && scheme != "https" {
			return nil, ErrNotURL
		}
	} else {
		q = "https://" + q
	}

	u, err := url.Parse(q)
	if err != nil || u.User != nil || !validHost(strings.ToLower(u.Hostname())) {
		return nil, ErrNotURL
	}

	canonicalize(u)
	return u, nil
}

// SiteKey identifies a site: the lowercase host without port and without a
// leading "www.". "https://www.Example.com:443/a" -> "example.com".
func SiteKey(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// CanonicalFeedURL returns the form used as feeds.url: lowercase scheme and
// host, default port removed, fragment removed, empty path -> "/". Path and
// query are otherwise kept as-is. Does not change http <-> https.
func CanonicalFeedURL(u *url.URL) string {
	c := *u
	canonicalize(&c)
	return c.String()
}

// canonicalize lowercases u's scheme and host, drops the scheme's default
// port and the fragment, and sets an empty path to "/".
func canonicalize(u *url.URL) {
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if port := u.Port(); (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
		u.RawPath = ""
	}
}

// validHost reports whether host (already lowercase) is a domain name with at
// least two labels, each 1–63 characters of [a-z0-9-] not starting or ending
// with "-", whose last label is at least two letters or starts with "xn--".
// IP literals are rejected: sites are identified by name.
func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !validLabel(label) {
			return false
		}
	}
	tld := labels[len(labels)-1]
	return strings.HasPrefix(tld, "xn--") || (len(tld) >= 2 && isLetters(tld))
}

func validLabel(label string) bool {
	if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range []byte(label) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func isLetters(s string) bool {
	for _, c := range []byte(s) {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
