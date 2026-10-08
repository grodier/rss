package ingest

import (
	"net/http"
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

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name  string
		value string // "" leaves the header unset
		want  time.Duration
	}{
		{"seconds", "120", 2 * time.Minute},
		{"seconds with spaces", " 120 ", 2 * time.Minute},
		{"zero", "0", 0},
		{"huge seconds capped", "99999999999999999999", maxDeltaSeconds * time.Second},
		{"HTTP-date", now.Add(2 * time.Hour).Format(http.TimeFormat), 2 * time.Hour},
		{"past date", now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"negative", "-5", 0},
		{"signed", "+5", 0},
		{"fraction", "1.5", 0},
		{"garbage", "soon", 0},
		{"absent", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.value != "" {
				h.Set("Retry-After", tt.value)
			}
			if got := retryAfter(h, now); got != tt.want {
				t.Errorf("retryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFreshness(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	date := func(d time.Duration) string { return now.Add(d).Format(http.TimeFormat) }
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"max-age", http.Header{"Cache-Control": {"max-age=3600"}}, time.Hour},
		{"max-age with other directives", http.Header{"Cache-Control": {"public, s-maxage=60, MAX-AGE=600, must-revalidate"}}, 10 * time.Minute},
		{"quoted max-age", http.Header{"Cache-Control": {`max-age="600"`}}, 10 * time.Minute},
		{"max-age across header lines", http.Header{"Cache-Control": {"public", "max-age=600"}}, 10 * time.Minute},
		{"first max-age wins", http.Header{"Cache-Control": {"max-age=600, max-age=60"}}, 10 * time.Minute},
		{"max-age=0", http.Header{"Cache-Control": {"max-age=0"}}, 0},
		{"max-age wins over Expires", http.Header{"Cache-Control": {"max-age=600"}, "Expires": {date(5 * time.Hour)}}, 10 * time.Minute},
		{"s-maxage ignored", http.Header{"Cache-Control": {"s-maxage=600"}}, 0},
		{"no-cache", http.Header{"Cache-Control": {"no-cache"}}, 0},
		{"no-cache with max-age", http.Header{"Cache-Control": {"max-age=600, no-cache"}, "Expires": {date(5 * time.Hour)}}, 0},
		{"no-store", http.Header{"Cache-Control": {"no-store"}}, 0},
		{"invalid max-age", http.Header{"Cache-Control": {"max-age=soon"}, "Expires": {date(5 * time.Hour)}}, 0},
		{"Expires with Date", http.Header{"Date": {date(-time.Hour)}, "Expires": {date(time.Hour)}}, 2 * time.Hour},
		{"Expires without Date", http.Header{"Expires": {date(time.Hour)}}, time.Hour},
		{"Expires with invalid Date", http.Header{"Date": {"yesterday"}, "Expires": {date(time.Hour)}}, time.Hour},
		{"Expires in the past", http.Header{"Expires": {date(-time.Hour)}}, 0},
		{"invalid Expires", http.Header{"Expires": {"0"}}, 0},
		{"absent", http.Header{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := freshness(tt.header, now); got != tt.want {
				t.Errorf("freshness(%v) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
