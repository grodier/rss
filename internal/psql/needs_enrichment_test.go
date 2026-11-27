package psql

import (
	"net/url"
	"os"
	"testing"

	"github.com/grodier/rss/internal/feedparse"
	"github.com/grodier/rss/internal/ingest"
)

// A YouTube video has a title, a thumbnail and, from its media:group's
// description, an excerpt, so its watch page isn't fetched.
func TestYouTubeVideoNotEnriched(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../feedparse/testdata/atom-media-group.xml")
	if err != nil {
		t.Fatal(err)
	}
	feedURL, _ := url.Parse("https://www.youtube.com/feeds/videos.xml?channel_id=UC123")
	feed, err := feedparse.Parse(feedURL, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	articles := ingest.Articles(feed.Items)
	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	if needsEnrichment(articles[0]) {
		t.Errorf("needsEnrichment(%+v) = true, want false", articles[0])
	}
}
