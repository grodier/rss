package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestRateLimiter() *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		entries: make(map[string]*slidingWindow),
		now:     time.Now,
		done:    make(chan struct{}),
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	rl := newTestRateLimiter()

	for i := range 5 {
		allowed, err := rl.Allow("key", 5, time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestAllow_AtLimit(t *testing.T) {
	rl := newTestRateLimiter()

	for range 5 {
		rl.Allow("key", 5, time.Minute)
	}

	allowed, err := rl.Allow("key", 5, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("request should be denied after reaching limit")
	}
}

func TestAllow_AfterWindowExpires(t *testing.T) {
	now := time.Now()
	rl := newTestRateLimiter()
	rl.now = func() time.Time { return now }

	for range 5 {
		rl.Allow("key", 5, time.Minute)
	}

	// Advance past two full windows so both prev and curr expire.
	now = now.Add(2*time.Minute + time.Second)

	allowed, err := rl.Allow("key", 5, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("request should be allowed after window expires")
	}
}

func TestAllow_DifferentKeysAreIndependent(t *testing.T) {
	rl := newTestRateLimiter()

	for range 5 {
		rl.Allow("key-a", 5, time.Minute)
	}

	allowed, _ := rl.Allow("key-a", 5, time.Minute)
	if allowed {
		t.Fatal("key-a should be denied")
	}

	allowed, err := rl.Allow("key-b", 5, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("key-b should be allowed (different key)")
	}
}

func TestAllow_ConcurrentAccess(t *testing.T) {
	rl := newTestRateLimiter()

	var wg sync.WaitGroup
	var allowed atomic.Int64

	limit := 10
	goroutines := 50

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := rl.Allow("key", limit, time.Minute)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}()
	}

	wg.Wait()

	got := int(allowed.Load())
	if got > limit {
		t.Errorf("allowed %d requests, limit is %d", got, limit)
	}
}

func TestAllow_SlidingWindowWeighting(t *testing.T) {
	now := time.Now()
	rl := newTestRateLimiter()
	rl.now = func() time.Time { return now }

	// Fill 8 requests in the first window (limit 10).
	for range 8 {
		rl.Allow("key", 10, time.Minute)
	}

	// Advance 30s into the next window.
	// Previous window: 8 requests, weight = (60-30)/60 = 0.5.
	// Estimated count: 8 * 0.5 + 0 = 4, which is under limit 10.
	now = now.Add(time.Minute + 30*time.Second)

	allowed, err := rl.Allow("key", 10, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("request should be allowed: weighted estimate is 4, limit is 10")
	}
}

func TestAllow_SlidingWindowDeniesWhenWeightedCountExceedsLimit(t *testing.T) {
	now := time.Now()
	rl := newTestRateLimiter()
	rl.now = func() time.Time { return now }

	// Fill 10 requests in the first window (limit 10).
	for range 10 {
		rl.Allow("key", 10, time.Minute)
	}

	// Advance only 6s into the next window.
	// Previous window: 10 requests, weight = (60-6)/60 = 0.9.
	// Estimated count: 10 * 0.9 + 0 = 9, which is under 10 — allowed.
	now = now.Add(time.Minute + 6*time.Second)

	allowed, err := rl.Allow("key", 10, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("first request in new window should be allowed (estimate 9)")
	}

	// Now currCount = 1, estimated = 10*0.9 + 1 = 10 — at limit, denied.
	allowed, err = rl.Allow("key", 10, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("request should be denied: weighted estimate is 10, at limit")
	}
}

func TestRateLimitMiddleware_Allowed(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	rl := newTestRateLimiter()

	called := false
	handler := s.RateLimit(rl, 5, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusOK)
	}

	if !called {
		t.Error("next handler was not called")
	}
}

func TestRateLimitMiddleware_Denied(t *testing.T) {
	s := newTestServer(&testServerOptions{})
	rl := newTestRateLimiter()

	handler := s.RateLimit(rl, 2, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the limit.
	for range 2 {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(rr, req)
	}

	// Next request should be denied.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	if retryAfter := rr.Header().Get("Retry-After"); retryAfter != "60" {
		t.Errorf("Retry-After header: got %q, want %q", retryAfter, "60")
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}

	if got["error_code"] != "RATE_LIMITED" {
		t.Errorf("error_code: got %q, want %q", got["error_code"], "RATE_LIMITED")
	}

	if got["message"] != "you have exceeded the rate limit, please try again later" {
		t.Errorf("message: got %q, want %q", got["message"], "you have exceeded the rate limit, please try again later")
	}

	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("details: expected map, got %T", got["details"])
	}

	if len(details) != 0 {
		t.Errorf("details: expected empty map, got %v", details)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"ipv4 with port", "192.168.1.1:12345", "", "192.168.1.1"},
		{"ipv4 without port", "192.168.1.1", "", "192.168.1.1"},
		{"ipv6 with port", "[::1]:12345", "", "::1"},
		{"xff single ip", "10.0.0.1:1234", "203.0.113.50", "203.0.113.50"},
		{"xff multiple ips", "10.0.0.1:1234", "203.0.113.50, 70.41.3.18, 150.172.238.178", "203.0.113.50"},
		{"xff with spaces", "10.0.0.1:1234", " 203.0.113.50 , 70.41.3.18", "203.0.113.50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := clientIP(req)
			if got != tt.want {
				t.Errorf("clientIP: got %q, want %q", got, tt.want)
			}
		})
	}
}
