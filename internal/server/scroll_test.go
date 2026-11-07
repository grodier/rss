package server

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
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
		{"timeline", getHome(t, timeline, "/", true).Body.String(), "/timelines/all"},
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

// TestYouWereHereMarkup checks the markup app.js's "You were here" beacon
// and infinite scroll rely on: a timeline's first page puts data-seen-up-to
// and data-timeline on .article-list itself, app.js sends them as up_to and
// timeline (and nothing else) when the page is shown and when it's hidden,
// and the divider is a .timeline-divider directly inside .article-list, so
// appending a later page's children in order keeps it in place.
func TestYouWereHereMarkup(t *testing.T) {
	items := timelineItems(3)
	s := newTestServerWith(t, Services{
		ReadService: &fakeReadStore{timelineLineFn: func(context.Context, string, string) (rss.TimelineMark, error) {
			return rss.TimelineMark{SeenAt: time.Now(), DividerAt: items[1].Position}, nil
		}},
		SaveService: &fakeSaveStore{},
		ArticleService: &fakeArticleStore{listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
			return items, nil
		}},
	})
	doc, err := html.Parse(strings.NewReader(getHome(t, s, "/", true).Body.String()))
	if err != nil {
		t.Fatal(err)
	}

	lists := findAll(doc, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "article-list") })
	if len(lists) != 1 {
		t.Fatalf("found %d .article-list, want 1", len(lists))
	}
	for _, name := range []string{"data-seen-up-to", "data-timeline"} {
		if !hasAttr(lists[0], name) {
			t.Errorf(".article-list has no %s", name)
		}
	}
	var children []string
	for c := lists[0].FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			children = append(children, attr(c, "class"))
		}
	}
	want := []string{"article-row", "timeline-divider", "article-row", "article-row"}
	if len(children) != len(want) {
		t.Fatalf(".article-list children = %q, want classes %q", children, want)
	}
	for i, c := range children {
		if !slices.Contains(strings.Fields(c), want[i]) {
			t.Errorf("child %d class = %q, want %s", i, c, want[i])
		}
	}

	// The beacon reports the timeline and the position, under the names
	// timelineSeenHandler reads, and nothing else.
	js, err := fs.ReadFile(ui.Static, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	beacon := regexp.MustCompile(`(?s)sendBeacon\("/timeline/seen", new URLSearchParams\(\{(.*?)\}\)\)`).FindSubmatch(js)
	if beacon == nil {
		t.Fatal("app.js doesn't send the beacon to /timeline/seen")
	}
	if got, want := strings.Join(strings.Fields(string(beacon[1])), " "), "timeline: seen.dataset.timeline, up_to: seen.dataset.seenUpTo,"; got != want {
		t.Errorf("app.js's beacon sends %q, want %q only", got, want)
	}
	// It reports when the page is shown (positions of what arrives next are
	// set against the latest report) as well as when it's hidden.
	for _, when := range []string{`"visible"`, `"pageshow"`, `"hidden"`} {
		if !strings.Contains(string(js), when) {
			t.Errorf("app.js doesn't report on %s", when)
		}
	}
	// And every ten minutes while the reader is using the page, so a long
	// read of one page stays one visit (rss.VisitGap is 30 minutes).
	heartbeat := regexp.MustCompile(`(?s)setInterval\(\(\) => \{\s*if \(used && document\.visibilityState === "visible"\) \{.*?report\(\);.*?\}, 10 \* 60 \* 1000\)`)
	if !heartbeat.Match(js) {
		t.Error("app.js doesn't report every ten minutes while the page is used and visible")
	}
	for _, use := range []string{`"scroll"`, `"keydown"`, `"pointerdown"`} {
		if !strings.Contains(string(js), use) {
			t.Errorf("app.js doesn't count %s as using the page", use)
		}
	}
	if 10*time.Minute >= rss.VisitGap {
		t.Errorf("the ten-minute report doesn't keep a visit going: rss.VisitGap is %v", rss.VisitGap)
	}
}

// TestToggleButtonMarkup checks the markup app.js's Save and Star buttons
// rely on: each row has a form[data-toggle] posting to /articles/{id}/save
// (or /unsave when saved) and one posting to /articles/{id}/star (or /unstar
// when starred), each with a return_to input and a button whose aria-pressed
// is the state. The Star button is named "Star" and holds the star-burst SVG
// main.css animates, hidden from assistive technology.
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

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	stars := findAll(doc, func(n *html.Node) bool { return n.Data == "button" && hasClass(n, "star-button") })
	if len(stars) != len(items) {
		t.Errorf("found %d Star buttons, want %d", len(stars), len(items))
	}
	for _, b := range stars {
		if b.Parent == nil || b.Parent.Data != "form" || !hasAttr(b.Parent, "data-toggle") {
			t.Error("Star button isn't in a form[data-toggle]")
		}
		if !hasAttr(b, "aria-pressed") || attr(b, "aria-label") != "Star" {
			t.Errorf("Star button aria-pressed=%q aria-label=%q, want aria-pressed and aria-label=\"Star\"", attr(b, "aria-pressed"), attr(b, "aria-label"))
		}
		bursts := findAll(b, func(n *html.Node) bool { return n.Data == "svg" && hasClass(n, "star-burst") })
		if len(bursts) != 1 || attr(bursts[0], "aria-hidden") != "true" {
			t.Errorf("Star button: want one svg.star-burst with aria-hidden=\"true\", found %d", len(bursts))
		}
	}
}

// TestShareButtonMarkup checks the markup app.js's Share buttons rely on:
// each row of an article with a canonical URL has one hidden
// button[data-share-url] with its URL and a data-share-title, named
// "Share", holding the aria-hidden .copied-label showCopied fills.
func TestShareButtonMarkup(t *testing.T) {
	items := timelineItems(2)
	for i := range items {
		items[i].Article.CanonicalURL = fmt.Sprintf("https://example.com/%d", i)
	}
	s := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(context.Context, string, rss.TimelineFilter, rss.ArticleCursor, int) ([]rss.ArticleWithFeed, error) {
			return items, nil
		},
	}})
	doc, err := html.Parse(strings.NewReader(getHome(t, s, "/", true).Body.String()))
	if err != nil {
		t.Fatal(err)
	}

	rows := findAll(doc, func(n *html.Node) bool { return n.Data == "article" && hasClass(n, "article-row") })
	if len(rows) != len(items) {
		t.Fatalf("found %d rows, want %d", len(rows), len(items))
	}
	for i, row := range rows {
		buttons := findAll(row, func(n *html.Node) bool { return n.Data == "button" && hasAttr(n, "data-share-url") })
		if len(buttons) != 1 {
			t.Errorf("row %d: found %d button[data-share-url], want 1", i, len(buttons))
			continue
		}
		b := buttons[0]
		if got, want := attr(b, "data-share-url"), items[i].Article.CanonicalURL; got != want {
			t.Errorf("row %d: data-share-url = %q, want %q", i, got, want)
		}
		if got, want := attr(b, "data-share-title"), items[i].Article.DisplayTitle(); got != want {
			t.Errorf("row %d: data-share-title = %q, want %q", i, got, want)
		}
		if !hasAttr(b, "hidden") {
			t.Errorf("row %d: Share button isn't hidden", i)
		}
		if got := attr(b, "aria-label"); got != "Share" {
			t.Errorf("row %d: aria-label = %q, want %q", i, got, "Share")
		}
		labels := findAll(b, func(n *html.Node) bool { return n.Data == "span" && hasClass(n, "copied-label") })
		if len(labels) != 1 {
			t.Errorf("row %d: found %d .copied-label in the Share button, want 1", i, len(labels))
		} else if got := attr(labels[0], "aria-hidden"); got != "true" {
			t.Errorf("row %d: .copied-label aria-hidden = %q, want %q", i, got, "true")
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

// TestBusyFormsMarkup checks the markup app.js's busy forms rely on: each
// slow form is form[data-busy], and its submit button has the busy label in
// data-busy-label and its own label in a [data-busy-text] span, so the
// script swaps only the text and keeps any icon, and each page loads
// app.js. Fast forms aren't marked.
func TestBusyFormsMarkup(t *testing.T) {
	get := func(t *testing.T, s *Server, target string) string {
		t.Helper()
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", target, rr.Code, http.StatusOK)
		}
		return rr.Body.String()
	}
	feedServices := func(sub *fakeSubscriptionStore) Services {
		return Services{
			SubscriptionService: sub,
			ReadService:         &fakeReadStore{},
			SaveService:         &fakeSaveStore{},
			ArticleService:      &fakeArticleStore{},
			FeedService: &fakeFeedStore{getByIDFn: func(ctx context.Context, id string) (rss.Feed, error) {
				return rss.Feed{ID: id, Title: "T", Url: "https://example.com/feed.xml", SiteID: testSiteID}, nil
			}},
			SiteService: &fakeSiteStore{getByIDFn: func(ctx context.Context, id string) (rss.Site, error) {
				return rss.Site{ID: id, Host: "example.com"}, nil
			}},
		}
	}
	subscribed := &fakeSubscriptionStore{getFn: func(context.Context, string, string) (rss.Subscription, error) {
		return rss.Subscription{}, nil
	}}
	notSubscribed := &fakeSubscriptionStore{}

	page := func(name string) func(t *testing.T) string {
		switch name {
		case "search":
			return func(t *testing.T) string {
				s := newTestServerWith(t, Services{SearchService: &fakeSearchStore{searchFn: func(context.Context, string, string, int) ([]rss.SiteWithFeeds, error) {
					return nil, nil
				}}})
				return searchRequest(t, s, "example.com").Body.String()
			}
		case "site":
			return func(t *testing.T) string {
				s := siteServerWithLookup(t, rss.Site{ID: testSiteID, Host: "example.com", URL: "https://www.example.com/"}, nil, nil, nil, rss.Lookup{}, rss.ErrNoRecord)
				return serveSite(t, s, testSiteID).Body.String()
			}
		case "feed":
			return func(t *testing.T) string {
				return serveFeed(t, newTestServerWith(t, feedServices(notSubscribed)), testFeedID).Body.String()
			}
		case "subscribed feed":
			return func(t *testing.T) string {
				return serveFeed(t, newTestServerWith(t, feedServices(subscribed)), testFeedID).Body.String()
			}
		case "import":
			return func(t *testing.T) string {
				rr := httptest.NewRecorder()
				importServer(t, &fakeImportStore{}).settingsImportHandler(rr, requestWithSession(settingsSession))
				return rr.Body.String()
			}
		case "login":
			return func(t *testing.T) string { return get(t, newTestServer(t), "/login") }
		case "signup":
			return func(t *testing.T) string {
				return get(t, newSignupTestServer(t, Services{}), "/signup?invite="+testInviteCode)
			}
		}
		panic("unknown page " + name)
	}

	for _, tt := range []struct {
		page, action string
		label, busy  string // the button's label and busy label; "" for a form that isn't busy
	}{
		{"search", "/lookups", "Look up example.com", "Looking up example.com…"},
		{"site", "/lookups", "Check for new feeds", "Checking for new feeds…"},
		{"feed", "/feeds/" + testFeedID + "/refresh", "Refresh", "Refreshing…"},
		{"subscribed feed", "/feeds/" + testFeedID + "/refresh", "Refresh", "Refreshing…"},
		{"import", "/settings/import", "Import", "Importing…"},
		{"login", "/login", "Log in", "Logging in…"},
		{"signup", "/signup", "Sign up", "Signing up…"},
		{"feed", "/feeds/" + testFeedID + "/subscribe", "", ""},
		{"subscribed feed", "/feeds/" + testFeedID + "/unsubscribe", "", ""},
	} {
		t.Run(tt.page+" "+tt.action, func(t *testing.T) {
			body := page(tt.page)(t)
			if !strings.Contains(body, `<script src="/static/js/app.js?v=`) {
				t.Error("page doesn't load app.js")
			}
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && attr(n, "action") == tt.action })
			if len(forms) != 1 {
				t.Fatalf("found %d forms with action %q, want 1", len(forms), tt.action)
			}
			form := forms[0]
			if tt.busy == "" {
				if hasAttr(form, "data-busy") {
					t.Error("form has data-busy, want none")
				}
				return
			}
			if !hasAttr(form, "data-busy") {
				t.Error("form has no data-busy")
			}
			buttons := findAll(form, func(n *html.Node) bool { return n.Data == "button" && attr(n, "type") == "submit" })
			if len(buttons) != 1 {
				t.Fatalf("found %d submit buttons, want 1", len(buttons))
			}
			if got := attr(buttons[0], "data-busy-label"); got != tt.busy {
				t.Errorf("data-busy-label = %q, want %q", got, tt.busy)
			}
			texts := findAll(buttons[0], func(n *html.Node) bool { return hasAttr(n, "data-busy-text") })
			if len(texts) != 1 {
				t.Fatalf("found %d [data-busy-text] in the button, want 1", len(texts))
			}
			if got := textContent(texts[0]); got != tt.label {
				t.Errorf("[data-busy-text] = %q, want %q", got, tt.label)
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
