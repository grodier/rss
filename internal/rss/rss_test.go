package rss

import (
	"strings"
	"testing"
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
