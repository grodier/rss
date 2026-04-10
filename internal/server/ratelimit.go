package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(key string) (bool, error)
}

type slidingWindow struct {
	prevCount int
	currCount int
	currStart time.Time
}

type InMemoryRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]*slidingWindow
	now     func() time.Time
	done    chan struct{}
}

// NewInMemoryRateLimiter starts a background cleanup goroutine. Call Stop to
// release resources.
func NewInMemoryRateLimiter(limit int, window, cleanupInterval time.Duration) *InMemoryRateLimiter {
	rl := &InMemoryRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*slidingWindow),
		now:     time.Now,
		done:    make(chan struct{}),
	}
	go rl.cleanup(cleanupInterval)
	return rl
}

func (rl *InMemoryRateLimiter) Stop() {
	close(rl.done)
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
			rl.mu.Lock()
			now := rl.now()
			for key, entry := range rl.entries {
				if now.Sub(entry.currStart) >= 2*rl.window {
					delete(rl.entries, key)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (s *Server) RateLimit(limiter RateLimiter, retryAfter time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			allowed, err := limiter.Allow(ip)
			if err != nil {
				s.serverErrorResponse(w, r, err)
				return
			}

			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
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
