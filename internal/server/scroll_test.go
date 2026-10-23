package server

import (
	"context"
	"fmt"
	"regexp"
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
	timeline := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
			return timelineItems(31), nil
		},
	}})
	saved := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{
		listSavedFn: func(ctx context.Context, userID string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
			return savedItems(31), nil
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
		{"saved", getSaved(t, saved, "/saved").Body.String(), "/saved"},
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

// TestSaveButtonMarkup checks the markup app.js's Save buttons rely on: each
// row has a form[data-save] posting to /articles/{id}/save (or /unsave when
// saved), with a return_to input and a button whose aria-pressed is the state.
func TestSaveButtonMarkup(t *testing.T) {
	items := timelineItems(2)
	s := newTestServerWith(t, Services{
		ReadService: &fakeReadStore{},
		SaveService: &fakeSaveStore{savedArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return map[string]bool{items[0].Article.ID: true}, nil
		}},
		ArticleService: &fakeArticleStore{
			listTimelineFn: func(context.Context, string, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
				return items, nil
			},
		},
	})

	forms := saveForms(t, getHome(t, s, "/", true).Body.String())

	want := map[string]saveFormMarkup{
		items[0].Article.ID: {Action: "/articles/" + items[0].Article.ID + "/unsave", Pressed: "true", ReturnTo: "/"},
		items[1].Article.ID: {Action: "/articles/" + items[1].Article.ID + "/save", Pressed: "false", ReturnTo: "/"},
	}
	if len(forms) != len(want) {
		t.Errorf("found %d Save forms, want %d: %v", len(forms), len(want), forms)
	}
	for id, w := range want {
		if got := forms[id]; got != w {
			t.Errorf("Save form for %s = %+v, want %+v", id, got, w)
		}
	}
}

// saveFormMarkup is what app.js reads from a Save form.
type saveFormMarkup struct {
	Action   string // the form's action
	Pressed  string // the submit button's aria-pressed
	ReturnTo string // the return_to input's value
}

var saveActionRe = regexp.MustCompile(`^/articles/([^/]+)/(?:un)?save$`)

// saveForms finds the form[data-save] elements in body, keyed by the article
// ID in their action. It fails the test on a form without a POST method, an
// /articles/{id}/save or /unsave action, a return_to input or one submit
// button with aria-pressed.
func saveForms(t *testing.T, body string) map[string]saveFormMarkup {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	forms := map[string]saveFormMarkup{}
	for _, f := range findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-save") }) {
		action := attr(f, "action")
		m := saveActionRe.FindStringSubmatch(action)
		if m == nil || attr(f, "method") != "post" {
			t.Errorf("Save form method=%q action=%q, want post to /articles/{id}/save or /unsave", attr(f, "method"), action)
			continue
		}
		got := saveFormMarkup{Action: action}

		inputs := findAll(f, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "return_to" })
		if len(inputs) != 1 || attr(inputs[0], "type") != "hidden" {
			t.Errorf("Save form %s: want one hidden return_to input", action)
		} else {
			got.ReturnTo = attr(inputs[0], "value")
		}

		buttons := findAll(f, func(n *html.Node) bool {
			return n.Data == "button" && attr(n, "type") == "submit" && hasAttr(n, "aria-pressed")
		})
		if len(buttons) != 1 {
			t.Errorf("Save form %s: found %d submit buttons with aria-pressed, want 1", action, len(buttons))
		} else {
			got.Pressed = attr(buttons[0], "aria-pressed")
		}
		forms[m[1]] = got
	}
	return forms
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

// TestVideoEmbedMarkup checks the markup app.js's video embeds rely on: an
// article page whose content has a YouTube iframe renders
// figure.embed[data-embed="youtube"][data-embed-id] with an <a> inside.
func TestVideoEmbedMarkup(t *testing.T) {
	s := articleServer(t, rss.Article{
		Title:   "A video",
		URL:     "https://example.com/posts/a",
		Content: `<p>Watch:</p><iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"></iframe>`,
	}, nil, nil, nil)
	rr := serveArticle(t, s, testArticleID)
	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatal(err)
	}

	figures := findAll(doc, func(n *html.Node) bool {
		return n.Data == "figure" && hasClass(n, "embed") && attr(n, "data-embed") == "youtube"
	})
	if len(figures) != 1 {
		t.Fatalf("found %d figure.embed[data-embed=youtube], want 1", len(figures))
	}
	if id := attr(figures[0], "data-embed-id"); id != "dQw4w9WgXcQ" {
		t.Errorf("data-embed-id = %q, want dQw4w9WgXcQ", id)
	}
	links := findAll(figures[0], func(n *html.Node) bool { return n.Data == "a" })
	if len(links) != 1 {
		t.Fatalf("found %d <a> in the figure, want 1", len(links))
	}
	if href := attr(links[0], "href"); href != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("href = %q, want https://www.youtube.com/watch?v=dQw4w9WgXcQ", href)
	}
}
