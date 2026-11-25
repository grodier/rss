package rss

import (
	"testing"
	"time"
)

func TestTagName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"AI", "AI"},
		{"  News  ", "News"},
		{"Machine   learning", "Machine learning"},
		{"Machine\tlearning\nand\r\n  more", "Machine learning and more"},
		{"Single spaces stay", "Single spaces stay"},
		{" \t\n ", ""},
	}
	for _, tt := range tests {
		if got := TagName(tt.in); got != tt.want {
			t.Errorf("TagName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTimelineMarkDivider(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	divider := now.Add(-3 * time.Hour)
	upTo := now.Add(-time.Hour)
	tests := []struct {
		name string
		mark TimelineMark
		want time.Time
	}{
		{"no mark", TimelineMark{}, time.Time{}},
		{"first visit going on", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-10 * time.Minute)}, time.Time{}},
		{"a visit going on", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-10 * time.Minute), DividerAt: divider}, divider},
		{"a new visit at exactly the gap", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-VisitGap), DividerAt: divider}, upTo},
		{"a new visit days later", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-48 * time.Hour), DividerAt: divider}, upTo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mark.Divider(now); !got.Equal(tt.want) {
				t.Errorf("Divider = %v, want %v", got, tt.want)
			}
		})
	}
}
