package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/rss"
)

// TestInfiniteScrollMarkup checks the markup app.js's infinite scroll relies
// on: rows are .article-row elements with an article-{id} id directly inside
// .article-list, and the next page is linked by .pager a[rel=next].
func TestInfiniteScrollMarkup(t *testing.T) {
	timeline := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
			return timelineItems(31), nil
		},
	}})
	feed := feedServerWithArticles(t, nil, &fakeArticleStore{
		listByFeedFn: func(ctx context.Context, feedID string, before rss.ArticleCursor, limit int) ([]rss.Article, error) {
			return feedArticles(31), nil
		},
	})

	tests := []struct {
		name     string
		body     string
		nextPath string
	}{
		{"timeline", getHome(t, timeline, "/", true).Body.String(), "/"},
		{"feed page", serveFeed(t, feed, testFeedID).Body.String(), "/feeds/" + testFeedID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}

			lists := findAll(doc, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "article-list") })
			if len(lists) != 1 {
				t.Fatalf("found %d .article-list, want 1", len(lists))
			}
			var ids []string
			for c := lists[0].FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode {
					continue
				}
				if c.Data != "article" || !hasClass(c, "article-row") {
					t.Errorf(".article-list child <%s class=%q>, want <article class=\"article-row\">", c.Data, attr(c, "class"))
					continue
				}
				ids = append(ids, attr(c, "id"))
			}
			if len(ids) != 30 {
				t.Errorf("rows = %d, want 30", len(ids))
			}
			for i, id := range ids {
				if want := "article-" + fmt.Sprintf(timelineUUID, i); id != want {
					t.Errorf("row %d id = %q, want %q", i, id, want)
				}
			}

			links := findAll(doc, func(n *html.Node) bool {
				return n.Data == "a" && attr(n, "rel") == "next" && n.Parent != nil && n.Parent.Data == "nav" && hasClass(n.Parent, "pager")
			})
			if len(links) != 1 {
				t.Fatalf("found %d .pager a[rel=next], want 1", len(links))
			}
			if href := attr(links[0], "href"); !strings.HasPrefix(href, tt.nextPath+"?before=") {
				t.Errorf("next href = %q, want %s?before=…", href, tt.nextPath)
			}
		})
	}
}

func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	for d := range n.Descendants() {
		if d.Type == html.ElementNode && match(d) {
			found = append(found, d)
		}
	}
	return found
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attr(n, "class")), class)
}
