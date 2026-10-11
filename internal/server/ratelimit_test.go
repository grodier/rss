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
		{"Refund", func(l *rateLimiter, key string) { l.Refund(key) }, 0},
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

func TestRateLimiterRefund(t *testing.T) {
	// allowed counts how many hits key gets before it is denied.
	allowed := func(l *rateLimiter, key string) int {
		n := 0
		for n < 10 {
			if ok, _ := l.AllowWithRetry(key); !ok {
				break
			}
			n++
		}
		return n
	}

	t.Run("gives back a hit", func(t *testing.T) {
		l, _ := testLimiter(2, time.Minute)
		if n := allowed(l, "a"); n != 2 {
			t.Fatalf("allowed %d hits, want 2", n)
		}
		l.Refund("a")
		if n := allowed(l, "a"); n != 1 {
			t.Errorf("allowed %d hits after Refund, want 1", n)
		}
	})

	t.Run("no window", func(t *testing.T) {
		l, _ := testLimiter(2, time.Minute)
		l.Refund("a")
		if n := allowed(l, "a"); n != 2 {
			t.Errorf("allowed %d hits, want 2", n)
		}
	})

	t.Run("not below zero", func(t *testing.T) {
		l, _ := testLimiter(2, time.Minute)
		l.AllowWithRetry("a")
		l.Refund("a")
		l.Refund("a")
		if n := allowed(l, "a"); n != 2 {
			t.Errorf("allowed %d hits, want 2", n)
		}
	})

	t.Run("after the window ends", func(t *testing.T) {
		l, now := testLimiter(2, time.Minute)
		allowed(l, "a")
		*now = now.Add(time.Minute)
		l.Refund("a")
		if n := allowed(l, "a"); n != 2 {
			t.Errorf("allowed %d hits in the new window, want 2", n)
		}
	})
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
