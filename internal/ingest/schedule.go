package ingest

import (
	"math/rand/v2"
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
