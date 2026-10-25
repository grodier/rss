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
		listTimelineFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
			return timelineItems(31), nil
		},
	}})
	saved := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{
		listSavedFn: func(ctx context.Context, userID, q string, before rss.ArticleCursor, limit int) ([]rss.SavedArticle, error) {
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

// TestToggleButtonMarkup checks the markup app.js's Save and Star buttons
// rely on: each row has a form[data-toggle] posting to /articles/{id}/save
// (or /unsave when saved) and one posting to /articles/{id}/star (or /unstar
// when starred), each with a return_to input and a button whose aria-pressed
// is the state.
func TestToggleButtonMarkup(t *testing.T) {
	items := timelineItems(2)
	s := newTestServerWith(t, Services{
		ReadService: &fakeReadStore{},
		SaveService: &fakeSaveStore{savedArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return map[string]bool{items[0].Article.ID: true}, nil
		}},
		StarService: &fakeStarStore{starredArticleIDsFn: func(context.Context, string, []string) (map[string]bool, error) {
			return map[string]bool{items[1].Article.ID: true}, nil
		}},
		ArticleService: &fakeArticleStore{
			listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
				return items, nil
			},
		},
	})
	body := getHome(t, s, "/", true).Body.String()

	for _, tt := range []struct {
		verb string
		on   string // the article ID that's pressed
	}{
		{"save", items[0].Article.ID},
		{"star", items[1].Article.ID},
	} {
		forms := toggleForms(t, body, tt.verb)
		want := map[string]toggleFormMarkup{}
		for _, item := range items {
			id := item.Article.ID
			if id == tt.on {
				want[id] = toggleFormMarkup{Action: "/articles/" + id + "/un" + tt.verb, Pressed: "true", ReturnTo: "/"}
			} else {
				want[id] = toggleFormMarkup{Action: "/articles/" + id + "/" + tt.verb, Pressed: "false", ReturnTo: "/"}
			}
		}
		if len(forms) != len(want) {
			t.Errorf("found %d %s forms, want %d: %v", len(forms), tt.verb, len(want), forms)
		}
		for id, w := range want {
			if got := forms[id]; got != w {
				t.Errorf("%s form for %s = %+v, want %+v", tt.verb, id, got, w)
			}
		}
	}
}

// toggleFormMarkup is what app.js reads from a Save or Star form.
type toggleFormMarkup struct {
	Action   string // the form's action
	Pressed  string // the submit button's aria-pressed
	ReturnTo string // the return_to input's value
}

// saveForms is toggleForms for the Save forms.
func saveForms(t *testing.T, body string) map[string]toggleFormMarkup {
	t.Helper()
	return toggleForms(t, body, "save")
}

// starForms is toggleForms for the Star forms.
func starForms(t *testing.T, body string) map[string]toggleFormMarkup {
	t.Helper()
	return toggleForms(t, body, "star")
}

var toggleActionRe = regexp.MustCompile(`^/articles/([^/]+)/(?:un)?(save|star)$`)

// toggleForms finds the form[data-toggle] elements in body whose action is
// /articles/{id}/{verb} or /un{verb} ("save" or "star"), keyed by the article
// ID. It fails the test on a form[data-toggle] without a POST method or one
// of those actions, and on one without a return_to input or one submit
// button with aria-pressed.
func toggleForms(t *testing.T, body, verb string) map[string]toggleFormMarkup {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	forms := map[string]toggleFormMarkup{}
	for _, f := range findAll(doc, func(n *html.Node) bool { return n.Data == "form" && hasAttr(n, "data-toggle") }) {
		action := attr(f, "action")
		m := toggleActionRe.FindStringSubmatch(action)
		if m == nil || attr(f, "method") != "post" {
			t.Errorf("toggle form method=%q action=%q, want post to /articles/{id}/save, /unsave, /star or /unstar", attr(f, "method"), action)
			continue
		}
		if m[2] != verb {
			continue
		}
		got := toggleFormMarkup{Action: action}

		inputs := findAll(f, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "return_to" })
		if len(inputs) != 1 || attr(inputs[0], "type") != "hidden" {
			t.Errorf("form %s: want one hidden return_to input", action)
		} else {
			got.ReturnTo = attr(inputs[0], "value")
		}

		buttons := findAll(f, func(n *html.Node) bool {
			return n.Data == "button" && attr(n, "type") == "submit" && hasAttr(n, "aria-pressed")
		})
		if len(buttons) != 1 {
			t.Errorf("form %s: found %d submit buttons with aria-pressed, want 1", action, len(buttons))
		} else {
			got.Pressed = attr(buttons[0], "aria-pressed")
		}
		if _, dup := forms[m[1]]; dup {
			t.Errorf("more than one %s form for %s", verb, m[1])
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
