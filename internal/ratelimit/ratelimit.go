// Package ratelimit counts hits per key in fixed windows, for both servers'
// rate limits, and words the response to a denied request.
package ratelimit

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// maxRateLimitKeys is the map size above which the limiter sweeps expired
// entries.
const maxRateLimitKeys = 10_000

type windowCount struct {
	count int
	start time.Time
}

// Limiter is an in-memory fixed-window limiter: each key may make limit
// hits per window. State is per process; it resets on restart and isn't shared
// between instances.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string]*windowCount
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   make(map[string]*windowCount),
	}
}

// Allow records a hit for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	ok, _ := l.AllowWithRetry(key)
	return ok
}

// AllowWithRetry is like Allow and also returns how long until key's window
// ends, which is how long a denied caller should wait.
func (l *Limiter) AllowWithRetry(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	c := l.currentOrStart(key, now)
	retryAfter = c.start.Add(l.window).Sub(now)
	if c.count >= l.limit {
		return false, retryAfter
	}
	c.count++
	return true, retryAfter
}

// Refund removes a hit recorded for key, for an attempt that turned out not
// to count. It does nothing if key has no current window or no hits in it.
func (l *Limiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if c := l.current(key, l.now()); c != nil && c.count > 0 {
		c.count--
	}
}

// current returns key's window if it hasn't ended, or nil. It first sweeps
// expired entries when the map is over maxRateLimitKeys. The caller must hold
// l.mu.
func (l *Limiter) current(key string, now time.Time) *windowCount {
	if len(l.hits) > maxRateLimitKeys {
		l.sweep(now)
	}
	c := l.hits[key]
	if c == nil || !now.Before(c.start.Add(l.window)) {
		return nil
	}
	return c
}

// currentOrStart is like current, but starts a new window for key when it has
// none or its window has ended. The caller must hold l.mu.
func (l *Limiter) currentOrStart(key string, now time.Time) *windowCount {
	c := l.current(key, now)
	if c == nil {
		c = &windowCount{start: now}
		l.hits[key] = c
	}
	return c
}

// sweep drops entries whose window has passed. The caller must hold l.mu.
func (l *Limiter) sweep(now time.Time) {
	for k, c := range l.hits {
		if !now.Before(c.start.Add(l.window)) {
			delete(l.hits, k)
		}
	}
}

// SetRetryAfter sets the Retry-After header to d in whole seconds, rounded up.
func SetRetryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
}

// TooManyAttemptsMessage tells a rate-limited user how long to wait,
// in whole minutes rounded up (at least 1).
func TooManyAttemptsMessage(retryAfter time.Duration) string {
	minutes := max(int(math.Ceil(retryAfter.Minutes())), 1)
	if minutes == 1 {
		return "Too many attempts. Try again in 1 minute."
	}
	return fmt.Sprintf("Too many attempts. Try again in %d minutes.", minutes)
}
