package ingest

import (
	"testing"
	"time"
)

// checkWithin reports an error unless got is d ±10% after a time between
// before and after.
func checkWithin(t *testing.T, name string, got, before, after time.Time, d time.Duration) {
	t.Helper()
	lo := before.Add(d * 9 / 10)
	hi := after.Add(d * 11 / 10)
	if got.Before(lo) || got.After(hi) {
		t.Errorf("%s = %v, want within [%v, %v]", name, got, lo, hi)
	}
}

func TestNextFetch(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for range 500 {
		got := NextFetch(now)
		if d := got.Sub(now); d < 54*time.Minute || d > 66*time.Minute {
			t.Fatalf("NextFetch(now) = now + %v, want within [54m, 66m]", d)
		}
	}
}

func TestRetryAt(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{0, time.Hour},
		{1, time.Hour},
		{2, 2 * time.Hour},
		{3, 4 * time.Hour},
		{5, 16 * time.Hour},
		{6, 24 * time.Hour}, // 32h, capped
		{64, 24 * time.Hour},
		{1000, 24 * time.Hour},
	}
	for _, tt := range tests {
		for range 100 {
			checkWithin(t, "RetryAt", RetryAt(now, tt.failures), now, now, tt.want)
		}
	}
}
