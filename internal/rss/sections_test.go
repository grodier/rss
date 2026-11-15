package rss

import "testing"

func TestDiscoverFeedNames(t *testing.T) {
	tests := []struct {
		name                         string
		feed                         DiscoverFeed
		wantName, wantTitle, wantLbl string
	}{
		{"title is the site's name", DiscoverFeed{Feed: Feed{Title: "bake club "}, Site: Site{Title: "Bake Club", Host: "bake.example"}}, "Bake Club", "", "Bake Club"},
		{"no feed title", DiscoverFeed{Site: Site{Title: "Bake Club"}}, "Bake Club", "", "Bake Club"},
		{"title includes the site's name", DiscoverFeed{Feed: Feed{Title: "Bitwise: Security"}, Site: Site{Title: "Bitwise"}}, "Bitwise", "Bitwise: Security", "Bitwise: Security"},
		{"a title of its own", DiscoverFeed{Feed: Feed{Title: "Recipes"}, Site: Site{Title: "Bake Club"}}, "Bake Club", "Recipes", "Bake Club – Recipes"},
		{"untitled site", DiscoverFeed{Feed: Feed{Title: "Recipes"}, Site: Site{Host: "cook.example"}}, "cook.example", "Recipes", "cook.example – Recipes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.feed.Name(); got != tt.wantName {
				t.Errorf("Name = %q, want %q", got, tt.wantName)
			}
			if got := tt.feed.FeedTitle(); got != tt.wantTitle {
				t.Errorf("FeedTitle = %q, want %q", got, tt.wantTitle)
			}
			if got := tt.feed.Label(); got != tt.wantLbl {
				t.Errorf("Label = %q, want %q", got, tt.wantLbl)
			}
		})
	}
}
