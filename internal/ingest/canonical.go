package ingest

import (
	"net/url"
	"strings"
)

// trackingParams are query parameters CanonicalURL removes, besides any
// starting with "utm_".
var trackingParams = map[string]bool{
	"fbclid":  true,
	"gclid":   true,
	"dclid":   true,
	"msclkid": true,
	"mc_cid":  true,
	"mc_eid":  true,
	"igshid":  true,
	"_hsenc":  true,
	"_hsmi":   true,
}

// CanonicalURL returns the key used to match copies of one article across
// feeds: rawURL with the scheme and host lowercased, a default port
// (:80 for http, :443 for https) removed, the fragment dropped, and known
// tracking parameters removed from the query (the rest of the query is kept
// in its original order). It returns "" if rawURL is empty, unparseable or
// not http(s).
func CanonicalURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}

	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "http" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	} else {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	u.Fragment = ""
	u.RawFragment = ""
	u.RawQuery = stripTracking(u.RawQuery)
	if u.RawQuery == "" {
		u.ForceQuery = false
	}
	return u.String()
}

// stripTracking removes tracking parameters from a raw query, leaving the
// other parameters byte-for-byte unchanged and in order.
func stripTracking(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	kept := parts[:0]
	for _, p := range parts {
		name, _, _ := strings.Cut(p, "=")
		if strings.HasPrefix(name, "utm_") || trackingParams[name] {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "&")
}
