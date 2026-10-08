package ingest

import (
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const (
	// RefreshInterval is how long after a successful fetch a feed is due again.
	RefreshInterval = time.Hour
	// MaxRetryDelay caps the backoff after failed fetches.
	MaxRetryDelay = 24 * time.Hour
)

// NextFetch returns when a feed fetched successfully at now is due again:
// now + RefreshInterval, ±10% jitter.
func NextFetch(now time.Time) time.Time {
	return now.Add(jitter(RefreshInterval))
}

// RetryAt returns when a feed whose last failures attempts failed in a row is
// due again: now + RefreshInterval·2^(failures-1), capped at MaxRetryDelay,
// ±10% jitter. failures < 1 is treated as 1.
func RetryAt(now time.Time, failures int) time.Time {
	// Doubling until the cap, rather than shifting, can't overflow.
	delay := RefreshInterval
	for i := 1; i < failures && delay < MaxRetryDelay; i++ {
		delay *= 2
	}
	delay = min(delay, MaxRetryDelay)
	return now.Add(jitter(delay))
}

// jitter returns d scaled by a uniform random factor in [0.9, 1.1].
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64()))
}

// maxDeltaSeconds caps delta-seconds values in headers, as RFC 9111 §1.2.2
// allows, so converting them to a time.Duration can't overflow.
const maxDeltaSeconds = 1<<31 - 1

// retryAfter parses a Retry-After header (delta-seconds or HTTP-date) and
// returns how long to wait from now; 0 if absent, invalid or in the past.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, ok := deltaSeconds(v); ok {
		return secs
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	return max(t.Sub(now), 0)
}

// freshness returns how long a response stays fresh: Cache-Control max-age
// if present (s-maxage is ignored), else Expires minus Date (or now when
// Date is missing); 0 if neither applies or the value is invalid. A
// response with no-cache or no-store is never fresh.
func freshness(h http.Header, now time.Time) time.Duration {
	maxAge, hasMaxAge := "", false
	for _, line := range h.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			name, value, _ := strings.Cut(strings.TrimSpace(directive), "=")
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "no-cache", "no-store":
				return 0
			case "max-age":
				if !hasMaxAge {
					maxAge, hasMaxAge = strings.Trim(strings.TrimSpace(value), `"`), true
				}
			}
		}
	}
	if hasMaxAge {
		secs, _ := deltaSeconds(maxAge)
		return secs
	}

	v := h.Get("Expires")
	if v == "" {
		return 0
	}
	expires, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	date := now
	if d, err := http.ParseTime(h.Get("Date")); err == nil {
		date = d
	}
	return max(expires.Sub(date), 0)
}

// deltaSeconds parses a non-negative integer number of seconds, capped at
// maxDeltaSeconds.
func deltaSeconds(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	var secs int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		secs = min(secs*10+int64(c-'0'), maxDeltaSeconds)
	}
	return time.Duration(secs) * time.Second, true
}
