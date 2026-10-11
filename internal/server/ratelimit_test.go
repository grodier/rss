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
	tests := []struct {
		name string
		hit  func(l *rateLimiter, key string)
		want int
	}{
		{"Allow", func(l *rateLimiter, key string) { l.Allow(key) }, 1},
		{"Record", func(l *rateLimiter, key string) { l.Record(key) }, 1},
		{"Exceeded", func(l *rateLimiter, key string) { l.Exceeded(key) }, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, now := testLimiter(1, time.Minute)
			for i := range maxRateLimitKeys + 1 {
				l.Allow(fmt.Sprint(i))
			}
			*now = now.Add(2 * time.Minute)
			tt.hit(l, "fresh")
			if len(l.hits) != tt.want {
				t.Errorf("len(hits) = %d after sweep, want %d", len(l.hits), tt.want)
			}
		})
	}
}

func TestRateLimiterExceeded(t *testing.T) {
	l, now := testLimiter(3, time.Minute)

	// Exceeded never records a hit, however often it's called.
	for i := 1; i <= 5; i++ {
		if exceeded, _ := l.Exceeded("a"); exceeded {
			t.Fatalf("call %d with no hits: exceeded, want not", i)
		}
	}

	l.Record("a")
	l.Record("a")
	for i := 1; i <= 5; i++ {
		if exceeded, _ := l.Exceeded("a"); exceeded {
			t.Fatalf("call %d with 2 of 3 hits: exceeded, want not", i)
		}
	}

	*now = now.Add(20 * time.Second)
	l.Record("a")
	exceeded, retry := l.Exceeded("a")
	if !exceeded {
		t.Fatal("not exceeded with 3 of 3 hits")
	}
	if retry != 40*time.Second {
		t.Errorf("retryAfter = %v, want 40s", retry)
	}
	if exceeded, _ := l.Exceeded("b"); exceeded {
		t.Error("key b exceeded after key a hit its limit")
	}

	*now = now.Add(40 * time.Second)
	if exceeded, _ := l.Exceeded("a"); exceeded {
		t.Error("exceeded after the window passed")
	}
}

func TestRateLimiterRecord(t *testing.T) {
	l, _ := testLimiter(2, time.Minute)

	// Hits from Record count toward AllowWithRetry.
	l.Record("a")
	if ok, _ := l.AllowWithRetry("a"); !ok {
		t.Fatal("key a: hit 2 denied, want allowed")
	}
	if ok, _ := l.AllowWithRetry("a"); ok {
		t.Error("key a: hit 3 allowed after Record, want denied")
	}

	// And hits from AllowWithRetry count alongside Record's.
	l.AllowWithRetry("b")
	if exceeded, _ := l.Exceeded("b"); exceeded {
		t.Fatal("key b: exceeded with 1 of 2 hits")
	}
	l.Record("b")
	if exceeded, _ := l.Exceeded("b"); !exceeded {
		t.Error("key b: not exceeded after AllowWithRetry and Record")
	}
}

func TestTooManyAttemptsMessage(t *testing.T) {
	tests := []struct {
		retryAfter time.Duration
		want       string
	}{
		{30 * time.Second, "Too many attempts. Try again in 1 minute."},
		{60 * time.Second, "Too many attempts. Try again in 1 minute."},
		{61 * time.Second, "Too many attempts. Try again in 2 minutes."},
		{10 * time.Minute, "Too many attempts. Try again in 10 minutes."},
	}
	for _, tt := range tests {
		if got := tooManyAttemptsMessage(tt.retryAfter); got != tt.want {
			t.Errorf("tooManyAttemptsMessage(%v) = %q, want %q", tt.retryAfter, got, tt.want)
		}
	}
}
