package server

import (
	"testing"

	"github.com/grodier/rss/internal/rss"
)

func TestArticleRowByline(t *testing.T) {
	feed := rss.Feed{ID: "feed-1", Title: "Ada Example"}
	tests := []struct {
		name    string
		feed    rss.Feed
		authors []string
		want    string
	}{
		{"no authors", feed, nil, ""},
		{"one author who isn't the feed", feed, []string{"Grace Hopper"}, "Grace Hopper"},
		{"one author who is the feed", feed, []string{"Ada Example"}, ""},
		{"the feed's name in another case, with spaces", feed, []string{"  ada EXAMPLE "}, ""},
		{"the feed's own page, which doesn't name it", rss.Feed{}, []string{"Ada Example"}, "Ada Example"},
		{"two authors, one of them the feed", feed, []string{"Ada Example", "Grace Hopper"}, "Ada Example and Grace Hopper"},
		{"three authors", feed, []string{"Ada", "Grace", "Linus"}, "Ada and 2 others"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := articleRow{Article: rss.Article{Authors: tt.authors}, Feed: tt.feed}
			if got := r.Byline(); got != tt.want {
				t.Errorf("Byline() = %q, want %q", got, tt.want)
			}
		})
	}
}
