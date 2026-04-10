package server

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(key string) (bool, error)
	Window() time.Duration
}

type slidingWindow struct {
	prevCount int
	currCount int
	currStart time.Time
}

type InMemoryRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	entries  map[string]*slidingWindow
	now      func() time.Time
	done     chan struct{}
	stopOnce sync.Once
}

// NewInMemoryRateLimiter creates a rate limiter. When cleanupInterval is
// positive a background goroutine evicts expired entries; call Stop to release
// resources. Panics if limit or window are not positive.
func NewInMemoryRateLimiter(limit int, window, cleanupInterval time.Duration) *InMemoryRateLimiter {
	if limit <= 0 {
		panic("ratelimit: limit must be positive")
	}
	if window <= 0 {
		panic("ratelimit: window must be positive")
	}
	rl := &InMemoryRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*slidingWindow),
		now:     time.Now,
		done:    make(chan struct{}),
	}
	if cleanupInterval > 0 {
		go rl.cleanup(cleanupInterval)
	}
	return rl
}

func (rl *InMemoryRateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.done) })
}

func (rl *InMemoryRateLimiter) Window() time.Duration {
	return rl.window
}

func (rl *InMemoryRateLimiter) Allow(key string) (bool, error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	entry, exists := rl.entries[key]

	if !exists {
		rl.entries[key] = &slidingWindow{
			currCount: 1,
			currStart: now,
		}
		return true, nil
	}

	elapsed := now.Sub(entry.currStart)

	if elapsed >= 2*rl.window {
		entry.prevCount = 0
		entry.currCount = 1
		entry.currStart = now
		return true, nil
	}

	if elapsed >= rl.window {
		entry.prevCount = entry.currCount
		entry.currCount = 0
		entry.currStart = entry.currStart.Add(rl.window)
		elapsed = now.Sub(entry.currStart)
	}

	weight := float64(rl.window-elapsed) / float64(rl.window)
	estimated := float64(entry.prevCount)*weight + float64(entry.currCount)

	if estimated >= float64(rl.limit) {
		return false, nil
	}

	entry.currCount++
	return true, nil
}

func (rl *InMemoryRateLimiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.performCleanup()
		}
	}
}

func (rl *InMemoryRateLimiter) performCleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	for key, entry := range rl.entries {
		if now.Sub(entry.currStart) >= 2*rl.window {
			delete(rl.entries, key)
		}
	}
}

func (s *Server) RateLimit(limiter RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			allowed, err := limiter.Allow(ip)
			if err != nil {
				s.serverErrorResponse(w, r, err)
				return
			}

			if !allowed {
				secs := int(math.Ceil(limiter.Window().Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				s.rateLimitedResponse(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		raw := xff
		if ip, _, ok := strings.Cut(xff, ","); ok {
			raw = ip
		}
		raw = strings.TrimSpace(raw)
		if parsed := net.ParseIP(raw); parsed != nil {
			return parsed.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
