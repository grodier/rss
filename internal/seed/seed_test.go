package seed

import (
	"reflect"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

var testSections = []rss.Section{
	{ID: "s-food", Slug: "food", Name: "Food"},
	{ID: "s-film", Slug: "film-and-tv", Name: "Film and TV"},
	{ID: "s-arts", Slug: "arts", Name: "Arts, design"},
}

func TestParse(t *testing.T) {
	const app = "paperround.app"
	add := func(n int, text, u, key, section string) Line {
		return Line{N: n, Text: text, Entry: Entry{URL: u, SiteKey: key}, SectionID: section, Outcome: LineAdd}
	}
	tests := []struct {
		name, in string
		want     []Line
	}{
		{"empty upload", "", nil},
		{"only blank and comment lines", "\n# nothing yet\n  \n", nil},
		{"a home page", "npr.org", []Line{add(1, "npr.org", "https://npr.org/", "npr.org", "")}},
		{"a feed's address", "https://feeds.npr.org/1001/rss.xml\n", []Line{
			add(1, "https://feeds.npr.org/1001/rss.xml", "https://feeds.npr.org/1001/rss.xml", "feeds.npr.org", ""),
		}},
		{"www. and scheme variants of one site", "example.com\nhttp://www.example.com/news\nhttps://EXAMPLE.com", []Line{
			add(1, "example.com", "https://example.com/", "example.com", ""),
			{N: 2, Text: "http://www.example.com/news", Entry: Entry{URL: "http://www.example.com/news", SiteKey: "example.com"}, Outcome: LineDuplicate, SameAs: 1},
			{N: 3, Text: "https://EXAMPLE.com", Entry: Entry{URL: "https://example.com/", SiteKey: "example.com"}, Outcome: LineDuplicate, SameAs: 1},
		}},
		{"sections by name, slug and any case, after a comma or tab", "a.example, Food\nb.example\tfood\nc.example,FOOD\nd.example, film and tv\ne.example,FILM-AND-TV", []Line{
			add(1, "a.example, Food", "https://a.example/", "a.example", "s-food"),
			add(2, "b.example\tfood", "https://b.example/", "b.example", "s-food"),
			add(3, "c.example,FOOD", "https://c.example/", "c.example", "s-food"),
			add(4, "d.example, film and tv", "https://d.example/", "d.example", "s-film"),
			add(5, "e.example,FILM-AND-TV", "https://e.example/", "e.example", "s-film"),
		}},
		{"a section whose name has a comma", "a.example, arts, design", []Line{
			add(1, "a.example, arts, design", "https://a.example/", "a.example", "s-arts"),
		}},
		{"an unknown section", "a.example, Gardening", []Line{
			{N: 1, Text: "a.example, Gardening", Entry: Entry{URL: "https://a.example/", SiteKey: "a.example"}, Outcome: LineBadSection},
		}},
		{"trailing commas and spaces", "  a.example ,  \nb.example,\t ", []Line{
			add(1, "a.example ,", "https://a.example/", "a.example", ""),
			add(2, "b.example,", "https://b.example/", "b.example", ""),
		}},
		{"blank and comment lines counted in line numbers", "# food\n\na.example\r\n\n# more\nb.example", []Line{
			add(3, "a.example", "https://a.example/", "a.example", ""),
			add(6, "b.example", "https://b.example/", "b.example", ""),
		}},
		{"a bad address", "not an address, Food\nftp://example.com", []Line{
			{N: 1, Text: "not an address, Food", Outcome: LineBadAddress},
			{N: 2, Text: "ftp://example.com", Outcome: LineBadAddress},
		}},
		{"the app's own site", "https://www.paperround.app/discover\npaperround.app/@x", []Line{
			{N: 1, Text: "https://www.paperround.app/discover", Entry: Entry{URL: "https://www.paperround.app/discover", SiteKey: app}, Outcome: LineAppLink},
			{N: 2, Text: "paperround.app/@x", Entry: Entry{URL: "https://paperround.app/@x", SiteKey: app + "/@x"}, Outcome: LineAppLink},
		}},
		{"a duplicate only of a line that can be added", "a.example, Gardening\na.example, Food\nwww.a.example", []Line{
			{N: 1, Text: "a.example, Gardening", Entry: Entry{URL: "https://a.example/", SiteKey: "a.example"}, Outcome: LineBadSection},
			add(2, "a.example, Food", "https://a.example/", "a.example", "s-food"),
			{N: 3, Text: "www.a.example", Entry: Entry{URL: "https://www.a.example/", SiteKey: "a.example"}, Outcome: LineDuplicate, SameAs: 2},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, testSections, app)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestLineSection(t *testing.T) {
	for text, want := range map[string]string{
		"a.example":                 "",
		"a.example, Gardening":      "Gardening",
		"a.example\t Arts, design ": "Arts, design",
	} {
		if got := (Line{Text: text}).Section(); got != want {
			t.Errorf("Line{Text: %q}.Section() = %q, want %q", text, got, want)
		}
	}
}
