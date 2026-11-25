package rss

import (
	"testing"
	"time"
)

func TestQueueStatusBehind(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		q    QueueStatus
		want bool
	}{
		{"empty", QueueStatus{}, false},
		{"due now", QueueStatus{Jobs: 3, Oldest: now}, false},
		{"exactly the limit", QueueStatus{Jobs: 3, Oldest: now.Add(-QueueBehindAfter)}, false},
		{"a second over", QueueStatus{Jobs: 3, Oldest: now.Add(-QueueBehindAfter - time.Second)}, true},
		{"due later", QueueStatus{Jobs: 1, Oldest: now.Add(time.Minute)}, false},
	} {
		if got := tt.q.Behind(now); got != tt.want {
			t.Errorf("%s: Behind = %v, want %v", tt.name, got, tt.want)
		}
	}
}
