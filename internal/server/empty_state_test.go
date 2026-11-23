package server

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// assertEmptyState checks that body renders the empty-state partial once,
// with lead, line (none when empty) and the action links' hrefs in order,
// the first one primary.
func assertEmptyState(t *testing.T, body, lead, line string, hrefs ...string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	states := findAll(doc, func(n *html.Node) bool { return hasClass(n, "empty-state") })
	if len(states) != 1 {
		t.Fatalf("got %d empty states, want 1: %s", len(states), body)
	}
	state := states[0]
	leads := findAll(state, func(n *html.Node) bool { return hasClass(n, "empty-state-lead") })
	if len(leads) != 1 || textContent(leads[0]) != lead {
		t.Errorf("lead = %q, want %q", textsOf(leads), lead)
	}
	var lines []*html.Node
	for c := range state.ChildNodes() {
		if c.Type == html.ElementNode && c.Data == "p" && !hasClass(c, "empty-state-lead") {
			lines = append(lines, c)
		}
	}
	if (line == "" && len(lines) != 0) || (line != "" && (len(lines) != 1 || textContent(lines[0]) != line)) {
		t.Errorf("line = %q, want %q", textsOf(lines), line)
	}
	links := findAll(state, func(n *html.Node) bool { return n.Data == "a" })
	var got []string
	for i, a := range links {
		got = append(got, attr(a, "href"))
		if primary := hasClass(a, "btn-primary"); primary != (i == 0) {
			t.Errorf("action %d (%s): primary = %v, want %v", i, attr(a, "href"), primary, i == 0)
		}
	}
	if !slices.Equal(got, hrefs) {
		t.Errorf("actions = %q, want %q", got, hrefs)
	}
}

func textsOf(ns []*html.Node) []string {
	var s []string
	for _, n := range ns {
		s = append(s, textContent(n))
	}
	return s
}

func TestNewEmptyState(t *testing.T) {
	e, err := newEmptyState("star", "Lead.", "Line.", "One", "/one", "Two", "/two")
	if err != nil {
		t.Fatal(err)
	}
	want := []emptyAction{{Label: "One", Href: "/one", Primary: true}, {Label: "Two", Href: "/two"}}
	if e.Icon != "star" || e.Lead != "Lead." || e.Line != "Line." || !slices.Equal(e.Actions, want) {
		t.Errorf("newEmptyState = %+v", e)
	}

	for _, tt := range []struct {
		name    string
		actions []string
		icon    string
	}{
		{"a label without a link", []string{"One"}, "star"},
		{"three actions", []string{"One", "/one", "Two", "/two", "Three", "/three"}, "star"},
		{"an unknown icon", nil, "stars"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newEmptyState(tt.icon, "Lead.", "", tt.actions...); err == nil {
				t.Error("err = nil, want an error")
			}
		})
	}
}

// TestEmptyStateIcons checks the empty-state partial draws every icon
// newEmptyState accepts, so the list and the partial's names agree.
func TestEmptyStateIcons(t *testing.T) {
	s := newTestServer(t)
	ts, ok := s.templates["saved.html"]
	if !ok {
		t.Fatal("no saved.html template")
	}
	for _, icon := range emptyStateIcons {
		t.Run(icon, func(t *testing.T) {
			e, err := newEmptyState(icon, "Lead.", "")
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			if err := ts.ExecuteTemplate(&b, "empty-state", e); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(b.String()))
			if err != nil {
				t.Fatal(err)
			}
			if svgs := findAll(doc, func(n *html.Node) bool { return n.Data == "svg" }); len(svgs) != 1 {
				t.Errorf("got %d icons, want 1: %s", len(svgs), b.String())
			}
		})
	}
}
