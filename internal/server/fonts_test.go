package server

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/web"
	"golang.org/x/net/html"
)

// fontPages renders a page of each kind, by name: logged out, an error page,
// and logged in with and without reading text.
func fontPages(t *testing.T) map[string]string {
	t.Helper()
	pages := map[string]string{}
	get := func(target string) string {
		rr := httptest.NewRecorder()
		newTestServer(t).router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		return rr.Body.String()
	}
	pages["landing"] = get("/")
	pages["login"] = get("/login")
	pages["404"] = get("/no-such-page")

	timeline := newTestServerWith(t, Services{ReadService: &fakeReadStore{}, SaveService: &fakeSaveStore{}, ArticleService: &fakeArticleStore{
		listTimelineFn: func(ctx context.Context, userID string, filter rss.TimelineFilter, before rss.ArticleCursor, limit int) ([]rss.ArticleWithFeed, error) {
			return timelineItems(3), nil
		},
	}})
	pages["timeline"] = getHome(t, timeline, "/", true).Body.String()

	pages["article"] = serveArticle(t, articleServer(t, rss.Article{Title: "T", Content: "<p>Text</p>"}, nil, nil, nil), testArticleID).Body.String()

	s := newTestServer(t)
	pages["settings"] = serveSettingsPage(t, s, s.settingsHandler, settingsSession).Body.String()

	for name, body := range pages {
		if !strings.Contains(body, "</html>") {
			t.Fatalf("%s: didn't render a page: %s", name, body)
		}
	}
	return pages
}

// fontPreload is a <link rel="preload" as="font">'s attributes.
type fontPreload struct {
	href, typ   string
	crossorigin bool
}

// headLinks returns the font preloads and the stylesheet hrefs of an HTML page.
func headLinks(t *testing.T, body string) (preloads []fontPreload, stylesheets []string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "link" {
			continue
		}
		attrs := map[string]string{}
		cors := false
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
			if a.Key == "crossorigin" {
				cors = true
			}
		}
		switch attrs["rel"] {
		case "stylesheet":
			stylesheets = append(stylesheets, attrs["href"])
		case "preload":
			if attrs["as"] != "font" {
				t.Errorf("preload of %q has as=%q, want font", attrs["href"], attrs["as"])
				continue
			}
			preloads = append(preloads, fontPreload{href: attrs["href"], typ: attrs["type"], crossorigin: cors})
		}
	}
	return preloads, stylesheets
}

// TestFontPreloadsMatchCSS checks that each font a page preloads is one its
// stylesheets use, at the same URL (else the browser downloads it twice), and
// is preloaded the way fonts must be: as="font", type="font/woff2" and
// crossorigin (fonts are fetched in CORS mode).
func TestFontPreloadsMatchCSS(t *testing.T) {
	for name, body := range fontPages(t) {
		t.Run(name, func(t *testing.T) {
			preloads, stylesheets := headLinks(t, body)
			if len(stylesheets) == 0 {
				t.Fatal("links no stylesheet")
			}
			var css strings.Builder
			for _, href := range stylesheets {
				b, err := fs.ReadFile(web.App, strings.TrimPrefix(href, "/static/"))
				if err != nil {
					t.Fatalf("read the page's stylesheet: %v", err)
				}
				css.Write(b)
			}
			if len(preloads) == 0 {
				t.Fatal("preloads no font")
			}
			for _, p := range preloads {
				if !strings.Contains(css.String(), "url("+p.href+")") {
					t.Errorf("preloads %q, which isn't a url() in %q", p.href, stylesheets)
				}
				if p.typ != "font/woff2" {
					t.Errorf("preload of %q has type=%q, want font/woff2", p.href, p.typ)
				}
				if !p.crossorigin {
					t.Errorf("preload of %q lacks crossorigin", p.href)
				}
			}
		})
	}
}

// TestReadingFontPreloaded checks which pages preload Newsreader: those with
// reading text on their first screen. None preloads the italic, which only
// some articles use.
func TestReadingFontPreloaded(t *testing.T) {
	newsreader, err := asset("shared/fonts/newsreader.woff2")
	if err != nil {
		t.Fatal(err)
	}
	italic, err := asset("shared/fonts/newsreader-italic.woff2")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"timeline": true, "article": true, "landing": false, "login": false, "404": false, "settings": false}
	for name, body := range fontPages(t) {
		preloads, _ := headLinks(t, body)
		var got bool
		for _, p := range preloads {
			switch p.href {
			case newsreader:
				got = true
			case italic:
				t.Errorf("%s preloads the italic", name)
			}
		}
		if w, ok := want[name]; !ok {
			t.Errorf("no expectation for %s", name)
		} else if got != w {
			t.Errorf("%s preloads Newsreader = %v, want %v", name, got, w)
		}
	}
}
