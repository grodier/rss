package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/ui"
)

var labelForRe = regexp.MustCompile(`<label[^>]*\sfor="([^"]+)"`)

func TestTemplateLabelsMatchInputs(t *testing.T) {
	files, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range labelForRe.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(src, `id="`+m[1]+`"`) {
				t.Errorf("%s: label for=%q has no element with that id", f, m[1])
			}
		}
	}
}

func TestPasswordNotEchoed(t *testing.T) {
	const sentinel = "sentinel-pw-123"
	tests := []struct {
		name     string
		template string
		data     any
	}{
		{
			name:     "login",
			template: "login.html",
			data: struct {
				Flash string
				Form  any
			}{Form: loginForm{Email: "a@b.co", Password: sentinel}},
		},
		{
			name:     "signup",
			template: "signup.html",
			data: struct{ Form any }{
				Form: signupForm{Name: "Ann", Email: "a@b.co", Password: sentinel},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			rr := httptest.NewRecorder()
			if err := s.renderHTML(rr, http.StatusUnprocessableEntity, tt.template, tt.data); err != nil {
				t.Fatal(err)
			}
			body := rr.Body.String()
			if strings.Contains(body, sentinel) {
				t.Error("response body contains the submitted password")
			}
			if !strings.Contains(body, "a@b.co") {
				t.Error("response body no longer echoes the email")
			}
		})
	}
}

// TestAppNav checks the nav partial: the link to /feeds says My feeds, and
// only the current section's link is marked aria-current.
func TestAppNav(t *testing.T) {
	ts, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		section string
		current string // href of the link marked current; "" for none
	}{
		{"home", "/"},
		{"feeds", "/feeds"},
		{"search", "/search"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run("section "+tt.section, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ts["timeline.html"].ExecuteTemplate(&buf, "app-nav", tt.section); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(&buf)
			if err != nil {
				t.Fatal(err)
			}
			links := findAll(doc, func(n *html.Node) bool { return n.Data == "a" && n.Parent != nil && n.Parent.Data == "li" })
			if len(links) != 3 {
				t.Fatalf("found %d nav links, want 3", len(links))
			}
			var current []string
			for _, a := range links {
				href := attr(a, "href")
				if href == "/feeds" {
					if got := strings.TrimSpace(textContent(a)); got != "My feeds" {
						t.Errorf("the nav link to /feeds says %q, want My feeds", got)
					}
				}
				if attr(a, "aria-current") == "page" {
					current = append(current, href)
				}
			}
			var want []string
			if tt.current != "" {
				want = []string{tt.current}
			}
			if !slices.Equal(current, want) {
				t.Errorf("links marked current = %q, want %q", current, want)
			}
		})
	}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

func TestAvatarTone(t *testing.T) {
	seen := map[int]bool{}
	for i := range 100 {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		tone := avatarTone(id)
		if tone < 1 || tone > avatarTones {
			t.Fatalf("avatarTone(%q) = %d, want 1 to %d", id, tone, avatarTones)
		}
		if avatarTone(id) != tone {
			t.Fatalf("avatarTone(%q) is not stable", id)
		}
		seen[tone] = true
	}
	if len(seen) != avatarTones {
		t.Errorf("100 IDs got %d of the %d tones", len(seen), avatarTones)
	}
}

func TestMonogram(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"title", "kernel notes", "K"},
		{"leading punctuation", "“The Slow Web”", "T"},
		{"digit", "99% Invisible", "9"},
		{"non-Latin letter", "ëlan", "Ë"},
		{"https URL", "https://example.com/feed.xml", "E"},
		{"http URL with www", "http://www.blog.example/rss", "B"},
		{"empty", "", ""},
		{"no letters", "—", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := monogram(tt.in); got != tt.want {
				t.Errorf("monogram(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTemplates(t *testing.T) {
	ts, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != len(pages) {
		t.Errorf("parsed %d pages, want %d", len(ts), len(pages))
	}
	for name, tmpl := range ts {
		if tmpl.Lookup("article-row") == nil {
			t.Errorf("%s: article-row is not defined", name)
		}
	}
	if _, ok := ts["feed.html"]; !ok {
		t.Error("feed.html was not parsed")
	}
}
