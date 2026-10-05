package server

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func testLimiter(limit int, window time.Duration) (*rateLimiter, *time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newRateLimiter(limit, window)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestRateLimiterLimit(t *testing.T) {
	l, _ := testLimiter(10, 10*time.Minute)
	for i := 1; i <= 10; i++ {
		if !l.Allow("a") {
			t.Fatalf("hit %d denied, want allowed", i)
		}
	}
	if l.Allow("a") {
		t.Error("hit 11 allowed, want denied")
	}
}

func TestRateLimiterKeysIndependent(t *testing.T) {
	l, _ := testLimiter(1, time.Minute)
	if !l.Allow("a") || l.Allow("a") {
		t.Fatal("key a: want first allowed, second denied")
	}
	if !l.Allow("b") {
		t.Error("key b denied after key a hit its limit")
	}
}

func TestRateLimiterWindowResets(t *testing.T) {
	l, now := testLimiter(1, time.Minute)
	l.Allow("a")
	*now = now.Add(30 * time.Second)
	ok, retry := l.AllowWithRetry("a")
	if ok {
		t.Fatal("allowed inside the window")
	}
	if retry != 30*time.Second {
		t.Errorf("retryAfter = %v, want 30s", retry)
	}
	*now = now.Add(30 * time.Second)
	if !l.Allow("a") {
		t.Error("denied after the window passed")
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	l := newRateLimiter(100, time.Hour)
	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("a") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 100 {
		t.Errorf("allowed = %d, want 100", allowed)
	}
}

func TestRateLimiterSweepsExpiredKeys(t *testing.T) {
	l, now := testLimiter(1, time.Minute)
	for i := range maxRateLimitKeys + 1 {
		l.Allow(fmt.Sprint(i))
	}
	*now = now.Add(2 * time.Minute)
	l.Allow("fresh")
	if len(l.hits) != 1 {
		t.Errorf("len(hits) = %d after sweep, want 1", len(l.hits))
	}
}
