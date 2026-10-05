package server

import (
	"sync"
	"time"
)

// maxRateLimitKeys is the map size above which Allow sweeps expired entries.
const maxRateLimitKeys = 10_000

type windowCount struct {
	count int
	start time.Time
}

// rateLimiter is an in-memory fixed-window limiter: each key may make limit
// hits per window. State is per process; it resets on restart and isn't shared
// between instances.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string]*windowCount
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   make(map[string]*windowCount),
	}
}

// Allow records a hit for key and reports whether it is within the limit.
func (l *rateLimiter) Allow(key string) bool {
	ok, _ := l.AllowWithRetry(key)
	return ok
}

// AllowWithRetry is like Allow and also returns how long until key's window
// ends, which is how long a denied caller should wait.
func (l *rateLimiter) AllowWithRetry(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if len(l.hits) > maxRateLimitKeys {
		l.sweep(now)
	}

	c := l.hits[key]
	if c == nil || !now.Before(c.start.Add(l.window)) {
		c = &windowCount{start: now}
		l.hits[key] = c
	}
	retryAfter = c.start.Add(l.window).Sub(now)
	if c.count >= l.limit {
		return false, retryAfter
	}
	c.count++
	return true, retryAfter
}

// sweep drops entries whose window has passed. The caller must hold l.mu.
func (l *rateLimiter) sweep(now time.Time) {
	for k, c := range l.hits {
		if !now.Before(c.start.Add(l.window)) {
			delete(l.hits, k)
		}
	}
}
