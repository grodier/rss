package rss

import (
	"strings"
	"testing"
	"time"
)

func TestArticleDisplayTitle(t *testing.T) {
	long := strings.Repeat("word ", 30)
	tests := []struct {
		name string
		a    Article
		want string
	}{
		{"title wins", Article{Title: "T", PageTitle: "P", Excerpt: "e"}, "T"},
		{"page title when untitled", Article{PageTitle: "P", Excerpt: "e"}, "P"},
		{"short excerpt", Article{Excerpt: "short text", PageDescription: "d"}, "short text"},
		{"long excerpt cut at word", Article{Excerpt: long}, strings.TrimSpace(strings.Repeat("word ", 16)) + "…"},
		{"page description when no excerpt", Article{PageDescription: long}, strings.TrimSpace(strings.Repeat("word ", 16)) + "…"},
		{"neither", Article{}, "(untitled)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.DisplayTitle(); got != tt.want {
				t.Errorf("DisplayTitle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArticleDisplayExcerpt(t *testing.T) {
	tests := []struct {
		name string
		a    Article
		want string
	}{
		{"excerpt wins", Article{Excerpt: "e", PageDescription: "d"}, "e"},
		{"page description", Article{PageDescription: "d"}, "d"},
		{"neither", Article{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.DisplayExcerpt(); got != tt.want {
				t.Errorf("DisplayExcerpt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArticleByline(t *testing.T) {
	tests := []struct {
		authors []string
		want    string
	}{
		{nil, ""},
		{[]string{"A"}, "A"},
		{[]string{"A", "B"}, "A and B"},
		{[]string{"A", "B", "C"}, "A, B and C"},
	}
	for _, tt := range tests {
		if got := (Article{Authors: tt.authors}).Byline(); got != tt.want {
			t.Errorf("Byline of %q = %q, want %q", tt.authors, got, tt.want)
		}
	}
}

func TestArticleCardImageURL(t *testing.T) {
	tests := []struct {
		name string
		a    Article
		want string
	}{
		{"feed image wins", Article{ImageURL: "https://a/feed.jpg", PageImageURL: "https://a/page.jpg"}, "https://a/feed.jpg"},
		{"page image", Article{PageImageURL: "https://a/page.jpg"}, "https://a/page.jpg"},
		{"neither", Article{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.CardImageURL(); got != tt.want {
				t.Errorf("CardImageURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArticleFeedSortAt(t *testing.T) {
	published := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	tests := []struct {
		name string
		a    Article
		want time.Time
	}{
		{"published", Article{PublishedAt: published, CreatedAt: created}, published},
		{"undated uses created", Article{CreatedAt: created}, created},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.FeedSortAt(); !got.Equal(tt.want) {
				t.Errorf("FeedSortAt = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestArticleURLHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.example.com/a", "example.com"},
		{"http://blog.example.com", "blog.example.com"},
		{"https://example.com:8443/a", "example.com"},
		{"", ""},
		{"mailto:someone@example.com", ""},
		{"/relative/path", ""},
		{"https://exa mple.com/", ""},
	}
	for _, tt := range tests {
		if got := (Article{URL: tt.url}).URLHost(); got != tt.want {
			t.Errorf("URLHost(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

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
		{"zero mark", TimelineMark{}, time.Time{}},
		{"first visit", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-10 * time.Minute)}, time.Time{}},
		{"same visit", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-10 * time.Minute), DividerAt: divider}, divider},
		{"new visit at exactly the gap", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-VisitGap), DividerAt: divider}, upTo},
		{"new visit days later", TimelineMark{SeenUpTo: upTo, SeenAt: now.Add(-48 * time.Hour), DividerAt: divider}, upTo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mark.Divider(now); !got.Equal(tt.want) {
				t.Errorf("Divider = %v, want %v", got, tt.want)
			}
		})
	}
}
