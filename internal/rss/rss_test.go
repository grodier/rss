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
		{"title wins", Article{Title: "T", Excerpt: "e"}, "T"},
		{"short excerpt", Article{Excerpt: "short text"}, "short text"},
		{"long excerpt cut at word", Article{Excerpt: long}, strings.TrimSpace(strings.Repeat("word ", 16)) + "…"},
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
